package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
)

func testResults() storage.SearchResults {
	now := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	return storage.SearchResults{
		&storage.SearchResult{
			Document: &storage.Document{
				ID:       "doc1",
				Source:   storage.SourceMarkdown,
				Path:     "/notes/go.md",
				Title:    "Go Programming",
				Preview:  "Go is great for concurrency.",
				Metadata: map[string]string{"tags": "go,concurrency"},
				ModifiedAt: now,
			},
			Score: 0.95,
		},
		&storage.SearchResult{
			Document: &storage.Document{
				ID:       "doc2",
				Source:   storage.SourcePDF,
				Path:     "/docs/rust.pdf",
				Title:    "Rust Overview",
				Preview:  "Rust provides memory safety.",
				Metadata: map[string]string{},
				ModifiedAt: now.Add(-24 * time.Hour),
			},
			Score: 0.72,
		},
	}
}

func TestExportJSON(t *testing.T) {
	var buf bytes.Buffer
	results := testResults()

	if err := exportJSON(&buf, results); err != nil {
		t.Fatalf("exportJSON failed: %v", err)
	}

	// Verify it's valid JSON
	var docs []exportDoc
	if err := json.Unmarshal(buf.Bytes(), &docs); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	if docs[0].Title != "Go Programming" {
		t.Errorf("docs[0].Title = %q, want %q", docs[0].Title, "Go Programming")
	}
	if docs[0].Score != 0.95 {
		t.Errorf("docs[0].Score = %f, want 0.95", docs[0].Score)
	}
	if docs[0].Tags != "go,concurrency" {
		t.Errorf("docs[0].Tags = %q, want %q", docs[0].Tags, "go,concurrency")
	}
	if docs[1].Tags != "" {
		t.Errorf("docs[1].Tags = %q, want empty", docs[1].Tags)
	}
}

func TestExportCSV(t *testing.T) {
	var buf bytes.Buffer
	results := testResults()

	if err := exportCSV(&buf, results); err != nil {
		t.Fatalf("exportCSV failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("expected 3 lines (header + 2 rows), got %d:\n%s", len(lines), buf.String())
	}

	// Verify header
	if !strings.HasPrefix(lines[0], "title,path,source,score,tags,modified_at") {
		t.Errorf("unexpected CSV header: %s", lines[0])
	}

	// Verify first data row contains expected values
	if !strings.Contains(lines[1], "Go Programming") {
		t.Errorf("first row missing title: %s", lines[1])
	}
	if !strings.Contains(lines[1], "0.9500") {
		t.Errorf("first row missing score: %s", lines[1])
	}
}

func TestExportMarkdown(t *testing.T) {
	var buf bytes.Buffer
	results := testResults()

	if err := exportMarkdown(&buf, results); err != nil {
		t.Fatalf("exportMarkdown failed: %v", err)
	}

	output := buf.String()

	// Check structure
	if !strings.Contains(output, "## 1. Go Programming") {
		t.Error("missing first heading")
	}
	if !strings.Contains(output, "## 2. Rust Overview") {
		t.Error("missing second heading")
	}
	if !strings.Contains(output, "**Tags:** go,concurrency") {
		t.Error("missing tags for first doc")
	}
	if !strings.Contains(output, "**Source:** markdown") {
		t.Error("missing source")
	}
	if !strings.Contains(output, "---") {
		t.Error("missing separator")
	}
}

func TestExportEmptyResults(t *testing.T) {
	var buf bytes.Buffer
	results := storage.SearchResults{}

	// JSON: should produce empty array
	if err := exportJSON(&buf, results); err != nil {
		t.Fatalf("exportJSON with empty results failed: %v", err)
	}
	if !strings.Contains(buf.String(), "[]") {
		t.Errorf("expected empty JSON array, got: %s", buf.String())
	}

	// CSV: should produce only header
	buf.Reset()
	if err := exportCSV(&buf, results); err != nil {
		t.Fatalf("exportCSV with empty results failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line (header only), got %d", len(lines))
	}

	// Markdown: should produce nothing
	buf.Reset()
	if err := exportMarkdown(&buf, results); err != nil {
		t.Fatalf("exportMarkdown with empty results failed: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("expected empty markdown output, got: %s", buf.String())
	}
}
