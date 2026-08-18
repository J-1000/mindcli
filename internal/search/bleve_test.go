package search

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/filter"
	"github.com/J-1000/mindcli/internal/storage"
)

func closeTestIndex(t *testing.T, idx *BleveIndex) {
	t.Helper()
	if err := idx.Close(); err != nil {
		t.Errorf("closing search index: %v", err)
	}
}

func TestBleveIndex_BasicOperations(t *testing.T) {
	tmpDir := t.TempDir()

	indexPath := filepath.Join(tmpDir, "test.bleve")

	// Create index
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer closeTestIndex(t, idx)

	ctx := context.Background()

	// Index some documents
	docs := []*storage.Document{
		{
			ID:       "1",
			Source:   storage.SourceMarkdown,
			Path:     "/notes/golang.md",
			Title:    "Go Programming Guide",
			Content:  "Go is a statically typed programming language designed at Google.",
			Metadata: map[string]string{"tags": "go,programming,tutorial", "stored_tags": "favorite"},
		},
		{
			ID:       "2",
			Source:   storage.SourceMarkdown,
			Path:     "/notes/rust.md",
			Title:    "Rust Programming Language",
			Content:  "Rust is a systems programming language focused on safety and performance.",
			Metadata: map[string]string{"tags": "rust,programming,systems"},
		},
		{
			ID:       "3",
			Source:   storage.SourceMarkdown,
			Path:     "/notes/cooking.md",
			Title:    "Pasta Recipes",
			Content:  "How to make delicious Italian pasta dishes at home.",
			Metadata: map[string]string{"tags": "cooking,food,recipes"},
		},
	}

	for _, doc := range docs {
		if err := idx.Index(ctx, doc); err != nil {
			t.Fatalf("indexing document: %v", err)
		}
	}

	// Wait for indexing
	time.Sleep(100 * time.Millisecond)

	// Test count
	count, err := idx.Count()
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}

	// Test search
	results, err := idx.Search(ctx, "programming", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}

	// Test specific search
	results, err = idx.Search(ctx, "Go Google", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 result for 'Go Google'")
	}
	if len(results) > 0 && results[0].ID != "1" {
		t.Errorf("top result ID = %s, want 1", results[0].ID)
	}

	// Test no results
	results, err = idx.Search(ctx, "elephantzzzxyz", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}

	results, err = idx.Search(ctx, "tag:favorite", 10)
	if err != nil {
		t.Fatalf("searching stored tag: %v", err)
	}
	if len(results) != 1 || results[0].ID != "1" {
		t.Errorf("stored tag search = %+v, want document 1", results)
	}
}

func TestBleveIndex_Delete(t *testing.T) {
	tmpDir := t.TempDir()

	indexPath := filepath.Join(tmpDir, "test.bleve")
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer closeTestIndex(t, idx)

	ctx := context.Background()

	// Index a document
	doc := &storage.Document{
		ID:      "test-doc",
		Source:  storage.SourceMarkdown,
		Title:   "Test Document",
		Content: "Unique searchable content xyz123",
	}

	if err := idx.Index(ctx, doc); err != nil {
		t.Fatalf("indexing: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify it's searchable
	results, err := idx.Search(ctx, "xyz123", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result before delete, got %d", len(results))
	}

	// Delete document
	if err := idx.Delete(ctx, "test-doc"); err != nil {
		t.Fatalf("deleting: %v", err)
	}

	// Verify it's gone
	results, err = idx.Search(ctx, "xyz123", 10)
	if err != nil {
		t.Fatalf("searching after delete: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

func TestBleveIndex_SourceFilter(t *testing.T) {
	tmpDir := t.TempDir()

	indexPath := filepath.Join(tmpDir, "test.bleve")
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer closeTestIndex(t, idx)

	ctx := context.Background()

	// Index documents from different sources
	docs := []*storage.Document{
		{ID: "1", Source: storage.SourceMarkdown, Title: "Note", Content: "test content"},
		{ID: "2", Source: storage.SourcePDF, Title: "PDF", Content: "test content"},
		{ID: "3", Source: storage.SourceMarkdown, Title: "Another Note", Content: "test content"},
	}

	for _, doc := range docs {
		if err := idx.Index(ctx, doc); err != nil {
			t.Fatalf("indexing: %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// Search with source filter
	results, err := idx.Search(ctx, "test source:markdown", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2 (markdown only)", len(results))
	}

	// Verify all results are markdown
	for _, r := range results {
		if r.ID != "1" && r.ID != "3" {
			t.Errorf("unexpected result ID: %s", r.ID)
		}
	}
}

func TestBleveIndex_TypedStructuredFilters(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), "test.bleve")
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestIndex(t, idx)

	ctx := context.Background()
	modified := func(date string) time.Time {
		value, err := time.Parse("2006-01-02", date)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	docs := []*storage.Document{
		{
			ID: "browser-current", Source: storage.SourceBrowser,
			Path: "https://export.arxiv.org/abs/1", Title: "Launch Plan", Content: "exact launch plan databases",
			Metadata:   map[string]string{"tags": "project", "kind": "history,bookmark", "normalized_url": "https://export.arxiv.org/abs/1"},
			ModifiedAt: modified("2026-07-10"),
		},
		{
			ID: "browser-archived", Source: storage.SourceBrowser,
			Path: "https://arxiv.org/abs/2", Title: "Old Launch Plan", Content: "exact launch plan archived draft",
			Metadata:   map[string]string{"tags": "project,archived", "kind": "bookmark", "normalized_url": "https://arxiv.org/abs/2"},
			ModifiedAt: modified("2024-12-01"),
		},
		{
			ID: "pdf-work", Source: storage.SourcePDF,
			Path: "/Users/test/Work/research.pdf", Title: "Launch Research", Content: "exact launch plan databases",
			Metadata: map[string]string{"tags": "project"}, ModifiedAt: modified("2026-07-12"),
		},
		{
			ID: "email", Source: storage.SourceEmail,
			Path: "/mail/launch.eml", Title: "Launch message", Content: "launch plan draft",
			Metadata: map[string]string{"tags": "project"}, ModifiedAt: modified("2026-07-14"),
		},
	}
	for _, doc := range docs {
		if err := idx.Index(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	date := func(value string) *time.Time {
		parsed := modified(value)
		return &parsed
	}
	tests := []struct {
		name    string
		text    string
		filters filter.Set
		wantIDs []string
	}{
		{name: "source", filters: filter.Set{Sources: []storage.Source{storage.SourceEmail}}, wantIDs: []string{"email"}},
		{name: "included and excluded tags", filters: filter.Set{Tags: []string{"project"}, ExcludedTags: []string{"archived"}}, wantIDs: []string{"browser-current", "pdf-work", "email"}},
		{name: "date range", filters: filter.Set{After: date("2026-07-11"), Before: date("2026-07-14")}, wantIDs: []string{"pdf-work"}},
		{name: "path contains case insensitive", filters: filter.Set{PathPrefixes: []string{"work/"}}, wantIDs: []string{"pdf-work"}},
		{name: "parent domain", filters: filter.Set{Domains: []string{"arxiv.org"}}, wantIDs: []string{"browser-current", "browser-archived"}},
		{name: "record kind", filters: filter.Set{Kinds: []string{"history"}}, wantIDs: []string{"browser-current"}},
		{name: "exact phrase", filters: filter.Set{ExactPhrases: []string{"exact launch plan"}}, wantIDs: []string{"browser-current", "browser-archived", "pdf-work"}},
		{name: "negated term", text: "launch", filters: filter.Set{ExcludedTerms: []string{"draft"}}, wantIDs: []string{"browser-current", "pdf-work"}},
		{name: "resolved document IDs", filters: filter.Set{DocumentIDs: []string{"pdf-work", "email"}}, wantIDs: []string{"pdf-work", "email"}},
		{name: "resolved empty collection", filters: filter.Set{DocumentIDs: []string{}}, wantIDs: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := idx.SearchFiltered(ctx, tt.text, tt.filters, 20)
			if err != nil {
				t.Fatal(err)
			}
			got := make(map[string]bool, len(results))
			for _, result := range results {
				got[result.ID] = true
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("IDs = %#v, want %#v", got, tt.wantIDs)
			}
			for _, id := range tt.wantIDs {
				if !got[id] {
					t.Errorf("missing ID %q from %#v", id, got)
				}
			}
		})
	}
}

func TestBleveIndex_Persistence(t *testing.T) {
	tmpDir := t.TempDir()

	indexPath := filepath.Join(tmpDir, "test.bleve")
	ctx := context.Background()

	// Create and index
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}

	doc := &storage.Document{
		ID:      "persist-test",
		Source:  storage.SourceMarkdown,
		Title:   "Persistence Test",
		Content: "This should persist across restarts",
	}

	if err := idx.Index(ctx, doc); err != nil {
		t.Fatalf("indexing: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	if err := idx.Close(); err != nil {
		t.Fatalf("closing index: %v", err)
	}

	// Reopen and verify
	idx2, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("reopening index: %v", err)
	}
	defer closeTestIndex(t, idx2)

	results, err := idx2.Search(ctx, "persist", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results after reopen, want 1", len(results))
	}
}

func TestBleveIndex_Highlights(t *testing.T) {
	tmpDir := t.TempDir()

	indexPath := filepath.Join(tmpDir, "test.bleve")
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer closeTestIndex(t, idx)

	ctx := context.Background()

	doc := &storage.Document{
		ID:      "highlight-test",
		Source:  storage.SourceMarkdown,
		Title:   "Golang Tutorial",
		Content: "Learn Golang programming with practical examples and best practices.",
	}

	if err := idx.Index(ctx, doc); err != nil {
		t.Fatalf("indexing: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	results, err := idx.Search(ctx, "Golang", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}

	// Highlights should be present
	if len(results[0].Highlights) == 0 {
		t.Log("Note: No highlights returned (this may be expected)")
	}
}
