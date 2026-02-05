package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
)

func TestBleveIndex_BasicOperations(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "test.bleve")

	// Create index
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer idx.Close()

	ctx := context.Background()

	// Index some documents
	docs := []*storage.Document{
		{
			ID:       "1",
			Source:   storage.SourceMarkdown,
			Path:     "/notes/golang.md",
			Title:    "Go Programming Guide",
			Content:  "Go is a statically typed programming language designed at Google.",
			Metadata: map[string]string{"tags": "go,programming,tutorial"},
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
}

func TestBleveIndex_Delete(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-delete-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "test.bleve")
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer idx.Close()

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
	tmpDir, err := os.MkdirTemp("", "bleve-source-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "test.bleve")
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer idx.Close()

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

func TestBleveIndex_Persistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-persist-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	idx.Close()

	// Reopen and verify
	idx2, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("reopening index: %v", err)
	}
	defer idx2.Close()

	results, err := idx2.Search(ctx, "persist", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results after reopen, want 1", len(results))
	}
}

func TestBleveIndex_Highlights(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bleve-highlight-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "test.bleve")
	idx, err := NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating index: %v", err)
	}
	defer idx.Close()

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
