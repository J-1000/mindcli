package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
)

func TestBrowserSourceName(t *testing.T) {
	src := NewBrowserSource(nil)
	if src.Name() != storage.SourceBrowser {
		t.Errorf("Name() = %q, want %q", src.Name(), storage.SourceBrowser)
	}
}

func TestIdentifyBrowser(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/jan/Library/Application Support/Google/Chrome/Default/History", "chrome"},
		{"/home/user/.mozilla/firefox/abc.default/places.sqlite", "firefox"},
		{"/Users/jan/Library/Safari/History.db", "safari"},
		{"/unknown/path.db", ""},
	}

	for _, tt := range tests {
		got := identifyBrowser(tt.path)
		if got != tt.want {
			t.Errorf("identifyBrowser(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestChromeTimeToGo(t *testing.T) {
	// Chrome timestamp for 2024-01-01 00:00:00 UTC
	// 1970 epoch = 11644473600 seconds from chrome epoch
	expected := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	chromeTime := (expected.Unix() + 11644473600) * 1000000

	got := chromeTimeToGo(chromeTime)
	if !got.Equal(expected) {
		t.Errorf("chromeTimeToGo(%d) = %v, want %v", chromeTime, got, expected)
	}
}

func TestBuildBrowserDocument(t *testing.T) {
	entries := []historyEntry{
		{URL: "https://example.com", Title: "Example", VisitCount: 5, Browser: "chrome", Kind: "history"},
		{URL: "https://go.dev", Title: "Go Language", VisitCount: 3, Browser: "chrome", Kind: "bookmark"},
	}

	file := FileInfo{
		Path:       "/fake/chrome/History",
		ModifiedAt: time.Now().Unix(),
	}

	doc := buildBrowserDocument(file, "chrome", entries)

	if doc.Source != storage.SourceBrowser {
		t.Errorf("Source = %q, want %q", doc.Source, storage.SourceBrowser)
	}
	if doc.Metadata["browser"] != "chrome" {
		t.Errorf("browser metadata = %q, want %q", doc.Metadata["browser"], "chrome")
	}
	if doc.Metadata["entry_count"] != "2" {
		t.Errorf("entry_count = %q, want %q", doc.Metadata["entry_count"], "2")
	}
	if doc.Metadata["history_count"] != "1" {
		t.Errorf("history_count = %q, want %q", doc.Metadata["history_count"], "1")
	}
	if doc.Metadata["bookmark_count"] != "1" {
		t.Errorf("bookmark_count = %q, want %q", doc.Metadata["bookmark_count"], "1")
	}
	if doc.Title != "Chrome Browser Data (2 entries)" {
		t.Errorf("Title = %q", doc.Title)
	}
}

func TestReadChromeBookmarks(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "browser-bookmarks-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	bookmarksPath := filepath.Join(tmpDir, "Bookmarks")
	data := `{
  "roots": {
    "bookmark_bar": {
      "children": [
        {"type":"url","name":"Example","url":"https://example.com"},
        {"type":"folder","name":"Folder","children":[
          {"type":"url","name":"Go","url":"https://go.dev"}
        ]}
      ]
    }
  }
}`
	if err := os.WriteFile(bookmarksPath, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	entries, err := readChromeBookmarks(bookmarksPath)
	if err != nil {
		t.Fatalf("readChromeBookmarks() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	for _, e := range entries {
		if e.Kind != "bookmark" {
			t.Fatalf("entry Kind = %q, want bookmark", e.Kind)
		}
	}
}
