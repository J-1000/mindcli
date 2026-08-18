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
	SourceHTML      Source = "html"
	SourceDOCX      Source = "docx"
	SourceEPUB      Source = "epub"
	SourceOrg       Source = "org"
	SourceCode      Source = "code"
)

// KnownSources returns every source accepted by public query surfaces in a
// stable display order. The returned slice is independent and safe to modify.
func KnownSources() []Source {
	return []Source{
		SourceMarkdown,
		SourcePDF,
		SourceEmail,
		SourceBrowser,
		SourceClipboard,
		SourceHTML,
		SourceDOCX,
		SourceEPUB,
		SourceOrg,
		SourceCode,
	}
}

// IsKnownSource reports whether source is a supported public source value.
func IsKnownSource(source Source) bool {
	for _, known := range KnownSources() {
		if source == known {
			return true
		}
	}
	return false
}

// IsFileBackedSource reports whether documents from source must retain a
// local backing artifact. Reconciled child documents use ingestion-scope
// metadata so cleanup can check the owning artifact instead of a virtual path.
func IsFileBackedSource(source Source) bool {
	switch source {
	case SourceMarkdown, SourcePDF, SourceEmail, SourceHTML, SourceDOCX,
		SourceEPUB, SourceOrg, SourceCode:
		return true
	default:
		return false
	}
}

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
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description,omitempty"`
	Query        string     `json:"query,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	LastViewedAt *time.Time `json:"last_viewed_at,omitempty"`
}

// ResearchSession is a named, explicitly persisted research conversation.
// Ordinary TUI conversations remain ephemeral unless a session is resumed.
type ResearchSession struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionCitation snapshots source provenance at answer time so exported
// briefs retain useful citations even if an indexed document is later removed.
type SessionCitation struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Source     Source `json:"source"`
}

// SessionTurn is one persisted question and generated answer.
type SessionTurn struct {
	ID        string            `json:"id"`
	SessionID string            `json:"session_id"`
	Question  string            `json:"question"`
	Answer    string            `json:"answer"`
	Citations []SessionCitation `json:"citations,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// SessionDocumentState controls how a document participates in subsequent
// answers for a persisted session.
type SessionDocumentState string

const (
	SessionDocumentIncluded SessionDocumentState = "included"
	SessionDocumentPinned   SessionDocumentState = "pinned"
	SessionDocumentExcluded SessionDocumentState = "excluded"
)

// SessionDocument joins a live document to its session-specific context state.
type SessionDocument struct {
	Document *Document            `json:"document"`
	State    SessionDocumentState `json:"state"`
	AddedAt  time.Time            `json:"added_at"`
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
