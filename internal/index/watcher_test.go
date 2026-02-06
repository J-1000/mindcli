package index

import (
	"os"
	"path/filepath"
	"testing"
)

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
