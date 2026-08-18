package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/config"
	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/storage"
)

type fakeDigestAnswerer struct {
	contexts []string
}

func (f *fakeDigestAnswerer) GenerateAnswer(_ context.Context, _ string, contexts []string) (string, error) {
	f.contexts = append([]string(nil), contexts...)
	return "SECRET generated synthesis [1]", nil
}

func TestParseDigestSince(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	for input, want := range map[string]time.Time{
		"24h":        now.Add(-24 * time.Hour),
		"7d":         now.Add(-7 * 24 * time.Hour),
		"2w":         now.Add(-14 * 24 * time.Hour),
		"2026-08-01": time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	} {
		got, err := parseDigestSince(input, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseDigestSince(%q) = %s, %v; want %s", input, got, err, want)
		}
	}
	if got, err := parseDigestSince("2026-08-01T03:04:05Z", now); err != nil || got.Hour() != 3 {
		t.Fatalf("RFC3339 digest since = %s, %v", got, err)
	}
	for _, invalid := range []string{"tomorrow", "0d", "521w", "2027-01-01"} {
		if _, err := parseDigestSince(invalid, now); err == nil {
			t.Errorf("parseDigestSince(%q) succeeded", invalid)
		}
	}
}

func TestDigestSummaryAndMarkdownAreBoundedCitedAndRedacted(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	doc := &storage.Document{
		ID: "stable-id", Source: storage.SourceMarkdown, Path: "/SECRET.md", Title: "SECRET source",
		Content: strings.Repeat("界", maxDigestContextRunes+50), Preview: "SECRET preview",
		Metadata: map[string]string{"tags": "SECRET-tag"}, IndexedAt: now, ModifiedAt: now,
	}
	report := digestReport{Collection: "SECRET research", After: now.Add(-24 * time.Hour), Before: now, Items: []digestItem{{Document: doc, Activity: now.Add(-time.Minute), Reasons: []string{"modified"}}}}
	answerer := &fakeDigestAnswerer{}
	summary, err := generateDigestSummary(context.Background(), answerer, report)
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" || len(answerer.contexts) != 1 || len([]rune(answerer.contexts[0])) > maxDigestContextRunes+100 {
		t.Fatalf("generated digest contexts = %#v, summary=%q", answerer.contexts, summary)
	}
	report.Summary, report.Generated = summary, true
	redactor, errs := privacy.NewRedactor([]string{"SECRET"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	var output bytes.Buffer
	if err := writeDigestMarkdown(&output, report, redactor); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"# Collection digest: [REDACTED] research", "## Generated summary", "first five bounded", "### [1] [REDACTED] source", "ID:** `stable-id`", "modified"} {
		if !strings.Contains(text, want) {
			t.Errorf("digest missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SECRET") {
		t.Fatalf("digest leaked redacted content:\n%s", text)
	}
}

func TestRunDigestWritesPrivateMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"Generated overview [1]","done":true}`))
	}))
	defer server.Close()
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	t.Setenv("MINDCLI_CONFIG_PATH", filepath.Join(tmpDir, "missing.yaml"))
	t.Setenv("MINDCLI_STORAGE_PATH", dataDir)
	t.Setenv("MINDCLI_CAPTURE_INBOX", filepath.Join(tmpDir, "inbox"))
	t.Setenv("MINDCLI_EMBEDDINGS_OLLAMA_URL", server.URL)
	original := activeProfile
	activeProfile = config.DefaultProfileName
	t.Cleanup(func() { activeProfile = original })

	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(dataDir, "mindcli.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	doc := &storage.Document{ID: "recent", Source: storage.SourceMarkdown, Path: "/recent.md", Title: "Recent", Content: "important update", Preview: "important update", ContentHash: "hash", IndexedAt: now, ModifiedAt: now}
	if err := db.InsertDocument(context.Background(), doc); err != nil {
		closeTestDB(t, db)
		t.Fatal(err)
	}
	closeTestDB(t, db)

	outputPath := filepath.Join(tmpDir, "digest.md")
	if err := runDigest([]string{"--since", "24h", "--output", outputPath}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Generated overview [1]") || !strings.Contains(string(content), "### [1] Recent") {
		t.Fatalf("digest output:\n%s", content)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("digest mode = %v", info.Mode().Perm())
	}
}
