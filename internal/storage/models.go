// Package storage provides database storage for MindCLI documents.
package storage

import (
	"encoding/json"
	"strings"
	"time"
)

// Source represents the type of document source.
type Source string

const (
	SourceMarkdown  Source = "markdown"
	SourcePDF       Source = "pdf"
	SourceEmail     Source = "email"
	SourceBrowser   Source = "browser"
	SourceClipboard Source = "clipboard"
)

// Document represents an indexed document.
type Document struct {
	ID          string            `json:"id"`
	Source      Source            `json:"source"`
	Path        string            `json:"path"`
	Title       string            `json:"title"`
	Content     string            `json:"content"`
	Preview     string            `json:"preview"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ContentHash string            `json:"content_hash"`
	IndexedAt   time.Time         `json:"indexed_at"`
	ModifiedAt  time.Time         `json:"modified_at"`
}

// MetadataJSON returns the metadata as a JSON string.
func (d *Document) MetadataJSON() string {
	if d.Metadata == nil {
		return "{}"
	}
	b, _ := json.Marshal(d.Metadata)
	return string(b)
}

// SetMetadataFromJSON parses JSON into the metadata map.
func (d *Document) SetMetadataFromJSON(jsonStr string) error {
	if jsonStr == "" || jsonStr == "{}" {
		d.Metadata = nil
		return nil
	}
	return json.Unmarshal([]byte(jsonStr), &d.Metadata)
}

// Tags returns the document's source-extracted and database-backed tags as a
// single, de-duplicated list. Source parsers store tags in "tags" while tags
// managed through the CLI/TUI are attached as "stored_tags".
func (d *Document) Tags() []string {
	if d == nil || d.Metadata == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var tags []string
	for _, field := range []string{"tags", "stored_tags"} {
		for _, tag := range strings.Split(d.Metadata[field], ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			key := strings.ToLower(tag)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			tags = append(tags, tag)
		}
	}
	return tags
}

// TagsString returns Tags in the comma-separated representation used by the
// search index and text-based output formats.
func (d *Document) TagsString() string {
	return strings.Join(d.Tags(), ",")
}

// SetStoredTags replaces the database-backed tag projection in Metadata.
func (d *Document) SetStoredTags(tags []string) {
	if len(tags) == 0 {
		if d.Metadata != nil {
			delete(d.Metadata, "stored_tags")
		}
		return
	}
	if d.Metadata == nil {
		d.Metadata = make(map[string]string)
	}
	d.Metadata["stored_tags"] = strings.Join(tags, ",")
}

// Chunk represents a chunk of a document for embedding.
type Chunk struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	Content    string `json:"content"`
	StartPos   int    `json:"start_pos"`
	EndPos     int    `json:"end_pos"`
}

// Collection represents a named group of documents.
type Collection struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Query       string    `json:"query,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// SearchResult represents a search result with scoring information.
type SearchResult struct {
	Document    *Document `json:"document"`
	Score       float64   `json:"score"`
	BM25Score   float64   `json:"bm25_score,omitempty"`
	VectorScore float64   `json:"vector_score,omitempty"`
	Highlights  []string  `json:"highlights,omitempty"`
	ChunkID     string    `json:"chunk_id,omitempty"`
}

// SearchResults is a slice of search results with helper methods.
type SearchResults []*SearchResult

// Len returns the number of results.
func (r SearchResults) Len() int { return len(r) }

// Less compares results by score (descending).
func (r SearchResults) Less(i, j int) bool { return r[i].Score > r[j].Score }

// Swap swaps two results.
func (r SearchResults) Swap(i, j int) { r[i], r[j] = r[j], r[i] }
