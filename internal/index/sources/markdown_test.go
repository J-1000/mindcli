package sources

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMarkdown(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantTitle   string
		wantTags    []string
		wantLinks   []string
		wantFM      map[string]string
		wantHeadings []string
	}{
		{
			name: "frontmatter with title",
			content: `---
title: My Note
date: 2024-01-15
tags: [test, demo]
---

# Heading One

Some content with #tag1 and #tag2.

Link to [[Another Note]] and [External](https://example.com).
`,
			wantTitle: "My Note",
			wantTags:  []string{"tag1", "tag2"},
			wantLinks: []string{"Another Note", "https://example.com"},
			wantFM: map[string]string{
				"title": "My Note",
				"date":  "2024-01-15",
				"tags":  "test, demo",
			},
			wantHeadings: []string{"Heading One"},
		},
		{
			name: "no frontmatter, h1 title",
			content: `# My Title

Some content here with #important tag.

## Section Two

More content.
`,
			wantTitle:    "My Title",
			wantTags:     []string{"important"},
			wantHeadings: []string{"My Title", "Section Two"},
		},
		{
			name:         "no frontmatter, no heading",
			content:      "Just some plain content without any structure.",
			wantTitle:    "",
			wantTags:     nil,
			wantHeadings: nil,
		},
		{
			name: "multiple tags same name",
			content: `Content with #mytag here and #mytag again and #othertag.`,
			wantTags: []string{"mytag", "othertag"},
		},
		{
			name: "wiki links and markdown links",
			content: `Check out [[Wiki Link]] and [[Another Wiki Link]].
Also see [Google](https://google.com) and [GitHub](https://github.com).`,
			wantLinks: []string{
				"Wiki Link",
				"Another Wiki Link",
				"https://google.com",
				"https://github.com",
			},
		},
		{
			name: "code blocks should not extract tags",
			content: "# Title\n\nReal #tag here.\n\n```go\n// #notag\nfunc main() {}\n```\n",
			// Note: Our simple parser doesn't handle code blocks for tags yet
			// This test documents current behavior
			wantTitle:    "Title",
			wantTags:     []string{"tag", "notag"}, // Known limitation
			wantHeadings: []string{"Title"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseMarkdown(tt.content)

			if result.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", result.Title, tt.wantTitle)
			}

			if !slicesEqual(result.Tags, tt.wantTags) {
				t.Errorf("tags = %v, want %v", result.Tags, tt.wantTags)
			}

			if !slicesEqual(result.Links, tt.wantLinks) {
				t.Errorf("links = %v, want %v", result.Links, tt.wantLinks)
			}

			if tt.wantFM != nil {
				for k, v := range tt.wantFM {
					if result.Frontmatter[k] != v {
						t.Errorf("frontmatter[%s] = %q, want %q", k, result.Frontmatter[k], v)
					}
				}
			}

			if !slicesEqual(result.Headings, tt.wantHeadings) {
				t.Errorf("headings = %v, want %v", result.Headings, tt.wantHeadings)
			}
		})
	}
}

func TestCreatePreview(t *testing.T) {
	tests := []struct {
		name    string
		content string
		maxLen  int
		wantContains string
		wantNotContains []string
	}{
		{
			name:    "removes markdown formatting",
			content: "**Bold** and *italic* and `code`.",
			maxLen:  100,
			wantContains: "Bold and italic and",
			wantNotContains: []string{"**", "*", "`"},
		},
		{
			name:    "removes links but keeps text",
			content: "Check [this link](https://example.com) out.",
			maxLen:  100,
			wantContains: "Check this link out",
			wantNotContains: []string{"https://", "[", "]", "(", ")"},
		},
		{
			name:    "truncates long content",
			content: "This is a very long piece of content that should be truncated at some point because it exceeds the maximum length.",
			maxLen:  50,
			wantContains: "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := createPreview(tt.content, tt.maxLen)

			if len(tt.wantContains) > 0 && !contains(result, tt.wantContains) {
				t.Errorf("preview should contain %q, got %q", tt.wantContains, result)
			}

			for _, notWant := range tt.wantNotContains {
				if contains(result, notWant) {
					t.Errorf("preview should not contain %q, got %q", notWant, result)
				}
			}
		})
	}
}

func TestMarkdownSource_Parse(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "markdown-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	content := `---
title: Test Document
author: Test Author
---

# Test Document

This is a test document with some content.

It has #tags and [[links]].
`
	filePath := filepath.Join(tmpDir, "test.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	info, _ := os.Stat(filePath)
	fileInfo := FileInfo{
		Path:       filePath,
		ModifiedAt: info.ModTime().Unix(),
		Size:       info.Size(),
	}

	source := NewMarkdownSource([]string{tmpDir}, []string{".md"}, nil)
	doc, err := source.Parse(context.Background(), fileInfo)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	if doc.Title != "Test Document" {
		t.Errorf("title = %q, want %q", doc.Title, "Test Document")
	}

	if doc.Source != "markdown" {
		t.Errorf("source = %q, want %q", doc.Source, "markdown")
	}

	if doc.Path != filePath {
		t.Errorf("path = %q, want %q", doc.Path, filePath)
	}

	if doc.ContentHash == "" {
		t.Error("content hash should not be empty")
	}

	if doc.Metadata["fm_author"] != "Test Author" {
		t.Errorf("metadata[fm_author] = %q, want %q", doc.Metadata["fm_author"], "Test Author")
	}

	if doc.Metadata["tags"] != "tags" {
		t.Errorf("metadata[tags] = %q, want %q", doc.Metadata["tags"], "tags")
	}
}

func TestMarkdownSource_TitleFallback(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "markdown-title-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// File with no frontmatter and no heading
	content := "Just some plain content without any title."
	filePath := filepath.Join(tmpDir, "my-note.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	info, _ := os.Stat(filePath)
	fileInfo := FileInfo{
		Path:       filePath,
		ModifiedAt: info.ModTime().Unix(),
		Size:       info.Size(),
	}

	source := NewMarkdownSource([]string{tmpDir}, []string{".md"}, nil)
	doc, err := source.Parse(context.Background(), fileInfo)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	// Should use filename as title
	if doc.Title != "my-note" {
		t.Errorf("title = %q, want %q", doc.Title, "my-note")
	}
}

// Helper functions

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
