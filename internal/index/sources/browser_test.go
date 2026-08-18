package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/storage"
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

func TestBuildBrowserDocumentsDeduplicatesNormalizedURLs(t *testing.T) {
	lastVisit := time.Date(2026, 8, 17, 14, 30, 0, 0, time.UTC)
	file := FileInfo{
		Path:       "/Users/test/Library/Application Support/Google/Chrome/Default/History",
		ModifiedAt: lastVisit.Add(time.Hour).Unix(),
	}
	entries := []historyEntry{
		{
			URL:        "https://Example.com/article?utm_source=newsletter&id=7#intro",
			Title:      "Article",
			VisitCount: 4,
			LastVisit:  lastVisit,
			Browser:    "chrome",
			Kind:       "history",
		},
		{
			URL:     "https://example.com:443/article?id=7",
			Title:   "Saved Article",
			AddedAt: lastVisit.Add(-time.Hour),
			Browser: "chrome",
			Kind:    "bookmark",
		},
		{
			URL:        "https://go.dev/",
			Title:      "Go",
			VisitCount: 2,
			LastVisit:  lastVisit.Add(-time.Hour),
			Browser:    "chrome",
			Kind:       "history",
		},
	}

	docs := buildBrowserDocuments(file, "chrome", entries)
	if len(docs) != 2 {
		t.Fatalf("len(docs) = %d, want 2", len(docs))
	}

	doc := docs[0]
	if doc.Source != storage.SourceBrowser {
		t.Fatalf("Source = %q, want browser", doc.Source)
	}
	if doc.Path != entries[0].URL {
		t.Errorf("Path = %q, want openable URL %q", doc.Path, entries[0].URL)
	}
	if doc.Metadata["normalized_url"] != "https://example.com/article?id=7" {
		t.Errorf("normalized_url = %q", doc.Metadata["normalized_url"])
	}
	if doc.Metadata["profile"] != "Default" {
		t.Errorf("profile = %q, want Default", doc.Metadata["profile"])
	}
	if doc.Metadata["kind"] != "history,bookmark" {
		t.Errorf("kind = %q, want history,bookmark", doc.Metadata["kind"])
	}
	if doc.Metadata["visit_count"] != "4" {
		t.Errorf("visit_count = %q, want 4", doc.Metadata["visit_count"])
	}
	if doc.Metadata["last_visit"] != lastVisit.Format(time.RFC3339) {
		t.Errorf("last_visit = %q", doc.Metadata["last_visit"])
	}
	if doc.ID == "" || doc.ContentHash == "" {
		t.Error("document is missing stable identity or content hash")
	}

	reordered := buildBrowserDocuments(file, "chrome", []historyEntry{entries[1], entries[0]})
	if len(reordered) != 1 || reordered[0].ID != doc.ID {
		t.Fatalf("stable ID changed after entry reordering: %q vs %q", reordered[0].ID, doc.ID)
	}

	otherProfile := file
	otherProfile.Path = "/Users/test/Library/Application Support/Google/Chrome/Profile 1/History"
	otherDocs := buildBrowserDocuments(otherProfile, "chrome", entries[:1])
	if otherDocs[0].ID == doc.ID {
		t.Error("same URL in different browser profiles must have different IDs")
	}
}

func TestNormalizeBrowserURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "canonical host port query and fragment",
			raw:  "HTTPS://Example.COM:443/path?utm_source=x&b=2&a=1#section",
			want: "https://example.com/path?a=1&b=2",
		},
		{name: "root path", raw: "http://example.com", want: "http://example.com/"},
		{name: "credentials omitted", raw: "https://user:pass@example.com/a", want: "https://example.com/a"},
		{name: "invalid", raw: "not a URL", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeBrowserURL(tt.raw); got != tt.want {
				t.Fatalf("normalizeBrowserURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestBrowserSourceRecognizesLegacyReconciliationScope(t *testing.T) {
	src := NewBrowserSource([]string{"chrome"})
	file := FileInfo{Path: "/Users/test/Library/Application Support/Google/Chrome/Default/History"}
	legacy := buildBrowserDocument(file, "chrome", []historyEntry{{
		URL: "https://example.com", Title: "Example", Browser: "chrome", Kind: "history",
	}})

	if got := src.ReconciliationScope(file); got != "chrome:Default" {
		t.Fatalf("ReconciliationScope() = %q, want chrome:Default", got)
	}
	if !src.IsDocumentInScope(file, legacy) {
		t.Error("legacy aggregate document was not recognized in browser profile scope")
	}
	legacy.Metadata["browser"] = "firefox"
	if src.IsDocumentInScope(file, legacy) {
		t.Error("document from another browser was included in profile scope")
	}
}

func TestReadChromeBookmarks(t *testing.T) {
	tmpDir := t.TempDir()

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
