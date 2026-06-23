package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jankowtf/mindcli/internal/config"
	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
)

func TestWatcher_IndexesAndRemoves(t *testing.T) {
	if testing.Short() {
		t.Skip("watcher test relies on real filesystem events and debounce timing")
	}

	tmp := t.TempDir()
	notesDir := filepath.Join(tmp, "notes")
	dataDir := filepath.Join(tmp, "data")
	os.MkdirAll(notesDir, 0755)
	os.MkdirAll(dataDir, 0755)

	db, err := storage.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bleve, err := search.NewBleveIndex(filepath.Join(dataDir, "test.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer bleve.Close()

	cfg := &config.Config{
		Sources:  config.SourcesConfig{Markdown: config.MarkdownSourceConfig{Enabled: true, Paths: []string{notesDir}, Extensions: []string{".md"}}},
		Indexing: config.IndexingConfig{Workers: 1},
	}
	indexer := NewIndexer(db, bleve, nil, nil, cfg)

	watcher, err := NewWatcher(indexer, []string{notesDir})
	if err != nil {
		t.Fatal(err)
	}
	watcher.debounceTime = 100 * time.Millisecond // speed up the test

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watcher.Start(ctx)
	time.Sleep(100 * time.Millisecond) // let the watcher register

	notePath := filepath.Join(notesDir, "note.md")
	if err := os.WriteFile(notePath, []byte("# Watched\n\nhello world"), 0644); err != nil {
		t.Fatal(err)
	}

	if !eventually(t, 5*time.Second, func() bool {
		doc, _ := db.GetDocumentByPath(ctx, notePath)
		return doc != nil
	}) {
		t.Fatal("file was not indexed after creation")
	}

	if err := os.Remove(notePath); err != nil {
		t.Fatal(err)
	}
	if !eventually(t, 5*time.Second, func() bool {
		doc, _ := db.GetDocumentByPath(ctx, notePath)
		return doc == nil
	}) {
		t.Fatal("file was not removed from the index after deletion")
	}
}

// eventually polls cond until it returns true or the timeout elapses.
func eventually(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

func TestExpandWatchPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		input string
		want  string
	}{
		{"~/notes", filepath.Join(home, "notes")},
		{"/absolute/path", "/absolute/path"},
		{"relative/path", "relative/path"},
	}

	for _, tt := range tests {
		got := expandWatchPath(tt.input)
		if got != tt.want {
			t.Errorf("expandWatchPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWatcherCreation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "watcher-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create watcher without a real indexer (just test creation).
	watcher, err := NewWatcher(nil, []string{tmpDir})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	if watcher.watcher == nil {
		t.Error("expected non-nil fsnotify watcher")
	}

	watcher.watcher.Close()
}
