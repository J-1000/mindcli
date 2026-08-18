// Package search provides full-text search capabilities using Bleve.
package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/filter"
	"github.com/J-1000/mindcli/internal/storage"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
)

// BleveIndex wraps a Bleve index for document search.
type BleveIndex struct {
	index bleve.Index
	path  string
}

// bleveDocument is the structure indexed by Bleve.
type bleveDocument struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Source     string   `json:"source"`
	Path       string   `json:"path"`
	Tags       []string `json:"tags"`
	Headings   string   `json:"headings"`
	Domain     []string `json:"domain"`
	Kinds      []string `json:"kind"`
	ModifiedAt int64    `json:"modified_at"`
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
	docMapping.AddFieldMappingsAt("headings", textFieldMapping)
	docMapping.AddFieldMappingsAt("tags", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("source", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("path", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("id", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("domain", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("kind", keywordFieldMapping)
	docMapping.AddFieldMappingsAt("modified_at", bleve.NewNumericFieldMapping())

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
		ID:         doc.ID,
		Title:      doc.Title,
		Content:    doc.Content,
		Source:     string(doc.Source),
		Path:       strings.ToLower(filepath.ToSlash(doc.Path)),
		Tags:       lowerValues(doc.Tags()),
		Headings:   doc.Metadata["headings"],
		Domain:     filter.DocumentDomains(doc),
		Kinds:      filter.DocumentKinds(doc),
		ModifiedAt: doc.ModifiedAt.Unix(),
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

// SearchFiltered performs full-text search with typed Boolean/range filters.
func (b *BleveIndex) SearchFiltered(ctx context.Context, text string, filters filter.Set, limit int) ([]SearchResult, error) {
	q := buildFilteredQuery(text, filters, time.Now())
	req := bleve.NewSearchRequestOptions(q, limit, 0, false)
	req.Fields = []string{"*"}
	req.Highlight = bleve.NewHighlight()
	req.Highlight.AddField("title")
	req.Highlight.AddField("content")

	result, err := b.index.SearchInContext(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("searching: %w", err)
	}
	results := make([]SearchResult, 0, len(result.Hits))
	for _, hit := range result.Hits {
		highlights := make(map[string][]string, len(hit.Fragments))
		for field, fragments := range hit.Fragments {
			highlights[field] = fragments
		}
		results = append(results, SearchResult{ID: hit.ID, Score: hit.Score, Highlights: highlights})
	}
	return results, nil
}

func buildFilteredQuery(text string, filters filter.Set, now time.Time) query.Query {
	filters = filter.ResolveRelativeTime(filters, now)
	boolean := bleve.NewBooleanQuery()
	if strings.TrimSpace(text) == "" {
		boolean.AddMust(bleve.NewMatchAllQuery())
	} else {
		boolean.AddMust(bleve.NewMatchQuery(text))
	}

	for _, phrase := range filters.ExactPhrases {
		boolean.AddMust(textFieldDisjunction(phrase, true))
	}
	for _, term := range filters.ExcludedTerms {
		boolean.AddMustNot(textFieldDisjunction(term, strings.ContainsAny(term, " \t")))
	}
	if len(filters.Sources) > 0 {
		alternatives := make([]query.Query, 0, len(filters.Sources))
		for _, source := range filters.Sources {
			alternatives = append(alternatives, termQuery("source", string(source)))
		}
		boolean.AddMust(bleve.NewDisjunctionQuery(alternatives...))
	}
	for _, tag := range filters.Tags {
		boolean.AddMust(termQuery("tags", tag))
	}
	for _, tag := range filters.ExcludedTags {
		boolean.AddMustNot(termQuery("tags", tag))
	}
	for _, path := range filters.PathPrefixes {
		wildcard := bleve.NewWildcardQuery("*" + escapeWildcard(strings.ToLower(filepath.ToSlash(path))) + "*")
		wildcard.SetField("path")
		boolean.AddMust(wildcard)
	}
	if len(filters.Domains) > 0 {
		alternatives := make([]query.Query, 0, len(filters.Domains))
		for _, domain := range filters.Domains {
			alternatives = append(alternatives, termQuery("domain", domain))
		}
		boolean.AddMust(bleve.NewDisjunctionQuery(alternatives...))
	}
	if len(filters.Kinds) > 0 {
		alternatives := make([]query.Query, 0, len(filters.Kinds))
		for _, kind := range filters.Kinds {
			alternatives = append(alternatives, termQuery("kind", kind))
		}
		boolean.AddMust(bleve.NewDisjunctionQuery(alternatives...))
	}
	if filters.After != nil || filters.Before != nil {
		var minimum, maximum *float64
		if filters.After != nil {
			value := float64(filters.After.Unix())
			minimum = &value
		}
		if filters.Before != nil {
			value := float64(filters.Before.Unix())
			maximum = &value
		}
		minInclusive, maxInclusive := true, false
		rangeQuery := bleve.NewNumericRangeInclusiveQuery(minimum, maximum, &minInclusive, &maxInclusive)
		rangeQuery.SetField("modified_at")
		boolean.AddMust(rangeQuery)
	}
	if filters.DocumentIDs != nil {
		boolean.AddMust(bleve.NewDocIDQuery(filters.DocumentIDs))
	}
	return boolean
}

func termQuery(field, value string) query.Query {
	term := bleve.NewTermQuery(strings.ToLower(value))
	term.SetField(field)
	return term
}

func textFieldDisjunction(value string, phrase bool) query.Query {
	fields := []string{"title", "content"}
	clauses := make([]query.Query, 0, len(fields))
	for _, field := range fields {
		if phrase {
			clause := bleve.NewMatchPhraseQuery(value)
			clause.SetField(field)
			clauses = append(clauses, clause)
		} else {
			clause := bleve.NewMatchQuery(value)
			clause.SetField(field)
			clauses = append(clauses, clause)
		}
	}
	return bleve.NewDisjunctionQuery(clauses...)
}

func escapeWildcard(value string) string {
	return strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`).Replace(value)
}

func lowerValues(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strings.ToLower(value)
	}
	return result
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
