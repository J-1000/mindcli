package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/storage"
)

func TestExtractReaderTextPrefersArticleContent(t *testing.T) {
	article := strings.Repeat("reader content with useful details ", 12)
	html := `<html><body>
<nav>navigation should disappear</nav>
<main><article><h1>Research</h1><p>` + article + `</p><script>secret script</script></article></main>
<footer>footer should disappear</footer>
</body></html>`

	got, err := extractReaderText(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Research") || !strings.Contains(got, "reader content") {
		t.Fatalf("reader text omitted article content: %q", got)
	}
	for _, unwanted := range []string{"navigation should disappear", "secret script", "footer should disappear"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("reader text contains excluded content %q: %q", unwanted, got)
		}
	}
}

func TestBrowserContentFetchEnrichesDocumentWithoutSessionHeaders(t *testing.T) {
	var sawSensitiveHeader atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			sawSensitiveHeader.Store(true)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><article><h1>Fetched</h1><p>` + strings.Repeat("useful page text ", 20) + `</p></article></body></html>`))
	}))
	defer server.Close()

	source := NewBrowserSource([]string{"chrome"})
	source.SetOptions(BrowserOptions{
		IncludeContent: true, AllowedDomains: []string{"127.0.0.1"},
		MaxResponseBytes: 4096, RequestTimeout: time.Second, FetchConcurrency: 1,
		MaxPages: 10, RetentionDays: 30,
	})
	doc := &storage.Document{
		ID: "page", Path: server.URL, Title: "Browser title", Content: "Browser title\n" + server.URL,
		Metadata: map[string]string{"normalized_url": server.URL}, ContentHash: "old",
	}

	source.enrichBrowserDocument(context.Background(), doc)
	if sawSensitiveHeader.Load() {
		t.Error("page request included cookie or authorization state")
	}
	if doc.Metadata["content_status"] != "fetched" {
		t.Fatalf("content_status = %q, want fetched", doc.Metadata["content_status"])
	}
	if doc.Metadata["extraction_method"] != "reader" || doc.Metadata["content_type"] != "text/html" {
		t.Errorf("fetch metadata = %#v", doc.Metadata)
	}
	if !strings.Contains(doc.Content, "useful page text") || doc.ContentHash == "old" {
		t.Errorf("document was not enriched: content=%q hash=%q", doc.Content, doc.ContentHash)
	}
	if doc.Path != server.URL {
		t.Errorf("openable source path changed to %q", doc.Path)
	}
}

func TestBrowserContentFetchClassifiesRecoverableFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/binary":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("binary"))
		case "/large":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("x", 64)))
		case "/redirect":
			http.Redirect(w, r, "https://blocked.invalid/page", http.StatusFound)
		}
	}))
	defer server.Close()

	tests := []struct {
		name       string
		path       string
		maxBytes   int64
		wantStatus string
	}{
		{name: "unsupported content type", path: "/binary", maxBytes: 1024, wantStatus: "unsupported_type"},
		{name: "response too large", path: "/large", maxBytes: 8, wantStatus: "too_large"},
		{name: "redirect domain blocked", path: "/redirect", maxBytes: 1024, wantStatus: "blocked"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewBrowserSource([]string{"chrome"})
			source.SetOptions(BrowserOptions{
				IncludeContent: true, AllowedDomains: []string{"127.0.0.1"},
				MaxResponseBytes: tt.maxBytes, RequestTimeout: time.Second,
				FetchConcurrency: 1, MaxPages: 10, RetentionDays: 30,
			})
			rawURL := server.URL + tt.path
			doc := &storage.Document{
				ID: "page", Path: rawURL, Title: "Page", Content: "original",
				Metadata: map[string]string{"normalized_url": rawURL}, ContentHash: "original",
			}
			source.enrichBrowserDocument(context.Background(), doc)
			if doc.Metadata["content_status"] != tt.wantStatus {
				t.Fatalf("content_status = %q, want %q", doc.Metadata["content_status"], tt.wantStatus)
			}
			if doc.Content != "original" || doc.ContentHash != "original" {
				t.Error("recoverable fetch failure changed indexed browser metadata content")
			}
		})
	}
}

func TestBrowserDomainPolicy(t *testing.T) {
	source := NewBrowserSource([]string{"chrome"})
	source.SetOptions(BrowserOptions{
		AllowedDomains: []string{"example.com", "*.allowed.test"},
		DeniedDomains:  []string{"private.example.com"},
	})

	tests := []struct {
		raw  string
		want bool
	}{
		{raw: "https://example.com/page", want: true},
		{raw: "https://news.example.com/page", want: true},
		{raw: "https://private.example.com/page", want: false},
		{raw: "https://sub.allowed.test/page", want: true},
		{raw: "https://notexample.com/page", want: false},
		{raw: "file:///tmp/page.html", want: false},
		{raw: "https://user:secret@example.com/page", want: false},
	}
	for _, tt := range tests {
		parsed, err := url.Parse(tt.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := source.urlAllowed(parsed); got != tt.want {
			t.Errorf("urlAllowed(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestBrowserFetchConcurrencyIsBounded(t *testing.T) {
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	source := NewBrowserSource([]string{"chrome"})
	source.SetOptions(BrowserOptions{
		IncludeContent: true, AllowedDomains: []string{"127.0.0.1"},
		MaxResponseBytes: 1024, RequestTimeout: 2 * time.Second,
		FetchConcurrency: 2, MaxPages: 10, RetentionDays: 30,
	})
	docs := make([]*storage.Document, 3)
	for i := range docs {
		rawURL := server.URL + "/" + string(rune('a'+i))
		docs[i] = &storage.Document{ID: rawURL, Path: rawURL, Title: "Page", Metadata: map[string]string{"normalized_url": rawURL}}
	}
	done := make(chan error, 1)
	go func() { done <- source.enrichBrowserDocuments(context.Background(), docs) }()

	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("two fetch workers did not start")
		}
	}
	select {
	case <-entered:
		t.Fatal("third request started before a bounded worker was released")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRetainBrowserDocumentsAppliesAgeAndCountBounds(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	docs := []*storage.Document{
		{ID: "middle", ModifiedAt: now.AddDate(0, 0, -5)},
		{ID: "newest", ModifiedAt: now.AddDate(0, 0, -1)},
		{ID: "expired", ModifiedAt: now.AddDate(0, 0, -60)},
	}
	got := retainBrowserDocuments(docs, BrowserOptions{RetentionDays: 30, MaxPages: 1}, now)
	if len(got) != 1 || got[0].ID != "newest" {
		t.Fatalf("retained documents = %+v, want newest only", got)
	}
}
