package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/query"
	"github.com/J-1000/mindcli/internal/search"
	"github.com/J-1000/mindcli/internal/storage"
)

func closeTestDB(t *testing.T, db *storage.DB) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Errorf("closing database: %v", err)
	}
}

func closeTestIndex(t *testing.T, index *search.BleveIndex) {
	t.Helper()
	if err := index.Close(); err != nil {
		t.Errorf("closing search index: %v", err)
	}
}

func TestVersionVariables(t *testing.T) {
	// Build-time variables should have default values when not injected.
	if version != "dev" {
		t.Errorf("version = %q, want 'dev'", version)
	}
	if commit != "none" {
		t.Errorf("commit = %q, want 'none'", commit)
	}
	if date != "unknown" {
		t.Errorf("date = %q, want 'unknown'", date)
	}
}

func TestPrintUsage(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	printUsage()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old

	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	output := string(buf)

	expectedSubstrings := []string{
		"MindCLI",
		"mindcli index",
		"mindcli watch",
		"mindcli search",
		"mindcli export",
		"mindcli tag",
		"mindcli clipboard",
		"mindcli ask",
		"mindcli config",
		"mindcli version",
		"mindcli help",
	}

	for _, s := range expectedSubstrings {
		if !contains(output, s) {
			t.Errorf("printUsage() output missing %q", s)
		}
	}
}

func TestTruncatePath(t *testing.T) {
	tests := []struct {
		path   string
		maxLen int
		want   string
	}{
		{"short", 10, "short "},
		{"/a/very/long/path/to/some/file.txt", 20, ".../to/some/file.txt "},
		{"exact", 5, "exact "},
	}

	for _, tt := range tests {
		got := truncatePath(tt.path, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncatePath(%q, %d) = %q, want %q", tt.path, tt.maxLen, got, tt.want)
		}
	}
}

func TestConsoleProgressReporter(t *testing.T) {
	r := &consoleProgressReporter{}

	// These should not panic
	r.OnStart("markdown", 10)
	if r.total != 10 {
		t.Errorf("total = %d, want 10", r.total)
	}

	r.OnProgress("markdown", 5, 10, "/test/file.md")
	if r.current != 5 {
		t.Errorf("current = %d, want 5", r.current)
	}

	r.OnComplete("markdown", 8, 2)
	r.OnError("markdown", "/bad/file.md", os.ErrNotExist)
}

// TestSearchWithTempIndex tests the search flow end-to-end using a temp DB and Bleve index.
func TestSearchWithTempIndex(t *testing.T) {
	tmpDir := t.TempDir()

	// Set up database
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer closeTestDB(t, db)

	// Set up Bleve index
	indexPath := filepath.Join(tmpDir, "search.bleve")
	searchIndex, err := search.NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("Failed to create search index: %v", err)
	}
	defer closeTestIndex(t, searchIndex)

	// Insert test documents
	ctx := context.Background()
	now := time.Now()
	docs := []*storage.Document{
		{ID: "1", Source: storage.SourceMarkdown, Path: "/notes/go.md", Title: "Go Programming", Content: "Go is a compiled language with great concurrency support.", ContentHash: "h1", IndexedAt: now, ModifiedAt: now},
		{ID: "2", Source: storage.SourceEmail, Path: "/mail/msg1.eml", Title: "Meeting Notes", Content: "Let's discuss the project timeline.", ContentHash: "h2", IndexedAt: now, ModifiedAt: now},
	}
	for _, doc := range docs {
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("Failed to insert doc: %v", err)
		}
		if err := searchIndex.Index(ctx, doc); err != nil {
			t.Fatalf("Failed to index doc: %v", err)
		}
	}

	// Search for "Go" — should find the first document
	results, err := searchIndex.Search(ctx, "Go programming", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected at least 1 search result for 'Go programming'")
	}

	// Verify the doc can be fetched
	doc, err := db.GetDocument(ctx, results[0].ID)
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}
	if doc.Title != "Go Programming" {
		t.Errorf("First result title = %q, want 'Go Programming'", doc.Title)
	}
}

// TestSearchWithSourceFilter verifies the query parser integrates with search for source filtering.
func TestSearchWithSourceFilter(t *testing.T) {
	tmpDir := t.TempDir()

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer closeTestDB(t, db)

	indexPath := filepath.Join(tmpDir, "search.bleve")
	searchIndex, err := search.NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("Failed to create search index: %v", err)
	}
	defer closeTestIndex(t, searchIndex)

	ctx := context.Background()
	now := time.Now()
	docs := []*storage.Document{
		{ID: "1", Source: storage.SourceMarkdown, Path: "/notes/go.md", Title: "Go Notes", Content: "Go concurrency patterns", ContentHash: "h1", IndexedAt: now, ModifiedAt: now},
		{ID: "2", Source: storage.SourceEmail, Path: "/mail/go.eml", Title: "Go Email", Content: "Go concurrency discussion", ContentHash: "h2", IndexedAt: now, ModifiedAt: now},
	}
	for _, doc := range docs {
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("Failed to insert doc: %v", err)
		}
		if err := searchIndex.Index(ctx, doc); err != nil {
			t.Fatalf("Failed to index doc: %v", err)
		}
	}

	// Parse a query with source filter
	parsed := query.ParseQuery("Go concurrency in my emails")
	if parsed.SourceFilter != "email" {
		t.Fatalf("SourceFilter = %q, want 'email'", parsed.SourceFilter)
	}

	searchQ := parsed.SearchTerms + " source:" + parsed.SourceFilter
	results, err := searchIndex.Search(ctx, searchQ, 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should only find the email doc
	for _, r := range results {
		doc, err := db.GetDocument(ctx, r.ID)
		if err != nil {
			t.Fatalf("GetDocument() error = %v", err)
		}
		if doc != nil && doc.Source != storage.SourceEmail {
			t.Errorf("Source filter not applied: got source %q for doc %q", doc.Source, doc.Title)
		}
	}
}

// TestAskWithNoOllama tests that runAsk falls back gracefully when Ollama is unavailable.
func TestAskFallbackWithoutOllama(t *testing.T) {
	// LLMClient with a bad URL should fail to generate, triggering the fallback path.
	llm := query.NewLLMClient("http://localhost:1", "nonexistent")
	ctx := context.Background()

	_, err := llm.Generate(ctx, "test prompt")
	if err == nil {
		t.Error("Expected error when connecting to unavailable Ollama, got nil")
	}
}

func TestParsePathsOverrideCommaSeparated(t *testing.T) {
	got := parsePathsOverride(" ~/notes ,~/docs,, /tmp/x ")
	want := []string{"~/notes", "~/docs", "/tmp/x"}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestParsePathsOverrideSupportsPathListSeparators(t *testing.T) {
	input := "a" + string(filepath.ListSeparator) + "b"
	got := parsePathsOverride(input)
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestPurgeClipboardDocuments(t *testing.T) {
	tmpDir := t.TempDir()

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer closeTestDB(t, db)

	indexPath := filepath.Join(tmpDir, "search.bleve")
	searchIndex, err := search.NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("Failed to create search index: %v", err)
	}
	defer closeTestIndex(t, searchIndex)

	ctx := context.Background()
	now := time.Now()
	docs := []*storage.Document{
		{ID: "clip-1", Source: storage.SourceClipboard, Path: "clipboard:1", Title: "clip1", Content: "old", ContentHash: "h1", IndexedAt: now, ModifiedAt: now.AddDate(0, 0, -40)},
		{ID: "clip-2", Source: storage.SourceClipboard, Path: "clipboard:2", Title: "clip2", Content: "new", ContentHash: "h2", IndexedAt: now, ModifiedAt: now},
	}
	for _, doc := range docs {
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("InsertDocument() error = %v", err)
		}
		if err := searchIndex.Index(ctx, doc); err != nil {
			t.Fatalf("Index() error = %v", err)
		}
	}

	removed, err := purgeClipboardDocuments(ctx, db, searchIndex, nil, docs, func(doc *storage.Document) bool {
		return doc.ModifiedAt.Before(now.AddDate(0, 0, -30))
	})
	if err != nil {
		t.Fatalf("purgeClipboardDocuments() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	if _, err := db.GetDocument(ctx, "clip-1"); err == nil {
		t.Fatalf("clip-1 should have been deleted")
	}
	if _, err := db.GetDocument(ctx, "clip-2"); err != nil {
		t.Fatalf("clip-2 should remain, get error = %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
