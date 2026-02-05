// Package search provides full-text search capabilities using Bleve.
package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/jankowtf/mindcli/internal/storage"
)

// BleveIndex wraps a Bleve index for document search.
type BleveIndex struct {
	index bleve.Index
	path  string
}

// bleveDocument is the structure indexed by Bleve.
type bleveDocument struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Source   string `json:"source"`
	Path     string `json:"path"`
	Tags     string `json:"tags"`
	Headings string `json:"headings"`
}

// NewBleveIndex creates or opens a Bleve index at the given path.
func NewBleveIndex(indexPath string) (*BleveIndex, error) {
	var idx bleve.Index
	var err error

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(indexPath), 0755); err != nil {
		return nil, fmt.Errorf("creating index directory: %w", err)
	}

	// Try to open existing index
	idx, err = bleve.Open(indexPath)
	if err == bleve.ErrorIndexPathDoesNotExist {
		// Create new index
		idx, err = bleve.New(indexPath, buildIndexMapping())
		if err != nil {
			return nil, fmt.Errorf("creating index: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("opening index: %w", err)
	}

	return &BleveIndex{
		index: idx,
		path:  indexPath,
	}, nil
}

// buildIndexMapping creates the mapping for documents.
func buildIndexMapping() mapping.IndexMapping {
	// Create document mapping
	docMapping := bleve.NewDocumentMapping()

	// Text field mapping with standard analyzer
	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Analyzer = standard.Name

	// Keyword field mapping (not analyzed)
	keywordFieldMapping := bleve.NewKeywordFieldMapping()

	// Configure field mappings
	docMapping.AddFieldMappingsAt("title", textFieldMapping)
	docMapping.AddFieldMappingsAt("content", textFieldMapping)
	docMapping.AddFieldMappingsAt("tags", textFieldMapping)
	docMapping.AddFieldMappingsAt("headings", textFieldMapping)
	docMapping.AddFieldMappingsAt("source", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("path", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("id", keywordFieldMapping)

	// Create index mapping
	indexMapping := bleve.NewIndexMapping()
	indexMapping.DefaultMapping = docMapping
	indexMapping.DefaultAnalyzer = standard.Name

	return indexMapping
}

// Index adds or updates a document in the index.
func (b *BleveIndex) Index(ctx context.Context, doc *storage.Document) error {
	// Convert to bleve document
	bleveDoc := bleveDocument{
		ID:       doc.ID,
		Title:    doc.Title,
		Content:  doc.Content,
		Source:   string(doc.Source),
		Path:     doc.Path,
		Tags:     doc.Metadata["tags"],
		Headings: doc.Metadata["headings"],
	}

	if err := b.index.Index(doc.ID, bleveDoc); err != nil {
		return fmt.Errorf("indexing document: %w", err)
	}

	return nil
}

// Delete removes a document from the index.
func (b *BleveIndex) Delete(ctx context.Context, id string) error {
	if err := b.index.Delete(id); err != nil {
		return fmt.Errorf("deleting document: %w", err)
	}
	return nil
}

// SearchResult represents a search result with score and highlights.
type SearchResult struct {
	ID         string
	Score      float64
	Highlights map[string][]string
}

// Search performs a full-text search and returns matching document IDs with scores.
func (b *BleveIndex) Search(ctx context.Context, queryStr string, limit int) ([]SearchResult, error) {
	// Build query
	q := buildQuery(queryStr)

	// Create search request
	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	req.Fields = []string{"*"}
	req.Highlight = bleve.NewHighlight()
	req.Highlight.AddField("title")
	req.Highlight.AddField("content")

	// Execute search
	result, err := b.index.Search(req)
	if err != nil {
		return nil, fmt.Errorf("searching: %w", err)
	}

	// Convert results
	results := make([]SearchResult, 0, len(result.Hits))
	for _, hit := range result.Hits {
		sr := SearchResult{
			ID:         hit.ID,
			Score:      hit.Score,
			Highlights: make(map[string][]string),
		}

		// Extract highlights
		for field, fragments := range hit.Fragments {
			sr.Highlights[field] = fragments
		}

		results = append(results, sr)
	}

	return results, nil
}

// buildQuery builds a Bleve query from a query string.
func buildQuery(queryStr string) query.Query {
	queryStr = strings.TrimSpace(queryStr)
	if queryStr == "" {
		return bleve.NewMatchAllQuery()
	}

	// Check for special operators
	parts := strings.Fields(queryStr)

	// Check for source filter (source:markdown)
	var sourceFilter string
	var searchTerms []string

	for _, part := range parts {
		if strings.HasPrefix(part, "source:") {
			sourceFilter = strings.TrimPrefix(part, "source:")
		} else if strings.HasPrefix(part, "tag:") {
			// Tag search
			tag := strings.TrimPrefix(part, "tag:")
			searchTerms = append(searchTerms, "tags:"+tag)
		} else {
			searchTerms = append(searchTerms, part)
		}
	}

	// Build main query
	var mainQuery query.Query
	if len(searchTerms) > 0 {
		// Use query string query for flexibility
		qsQuery := bleve.NewQueryStringQuery(strings.Join(searchTerms, " "))
		mainQuery = qsQuery
	} else {
		mainQuery = bleve.NewMatchAllQuery()
	}

	// Apply source filter if present
	if sourceFilter != "" {
		sourceQuery := bleve.NewTermQuery(sourceFilter)
		sourceQuery.SetField("source")

		boolQuery := bleve.NewBooleanQuery()
		boolQuery.AddMust(mainQuery)
		boolQuery.AddMust(sourceQuery)
		mainQuery = boolQuery
	}

	return mainQuery
}

// Count returns the total number of documents in the index.
func (b *BleveIndex) Count() (uint64, error) {
	return b.index.DocCount()
}

// Close closes the index.
func (b *BleveIndex) Close() error {
	return b.index.Close()
}

// DeleteIndex removes the index from disk.
func (b *BleveIndex) DeleteIndex() error {
	if err := b.index.Close(); err != nil {
		return err
	}
	return os.RemoveAll(b.path)
}
