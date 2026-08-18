package capture

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriterCreatesPrivatePortableMarkdown(t *testing.T) {
	inbox := filepath.Join(t.TempDir(), "inbox")
	now := time.Date(2026, 8, 18, 12, 34, 56, 0, time.UTC)
	writer := Writer{Inbox: inbox, Now: func() time.Time { return now }}
	result, err := writer.Write(context.Background(), Request{
		Content: "# Launch idea\n\nBuild the local workflow.",
		Title:   "Launch / Idea", Tags: []string{"Inbox", "project"},
		Collection: "research", SourceURL: "https://example.com/article",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Duplicate || filepath.Base(result.Path) != "saved-632538290468e7a39c06.md" {
		t.Fatalf("result = %+v", result)
	}
	content, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`title: "Launch / Idea"`, `tags: ["inbox", "project"]`,
		`collection: "research"`, `source_url: "https://example.com/article"`,
		`captured_at: "2026-08-18T12:34:56Z"`, "# Launch idea",
	} {
		if !strings.Contains(string(content), want) {
			t.Errorf("capture missing %q:\n%s", want, content)
		}
	}
	if info, err := os.Stat(result.Path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("capture mode = %v", info.Mode().Perm())
	}
	if info, err := os.Stat(inbox); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o700 {
		t.Fatalf("inbox mode = %v", info.Mode().Perm())
	}
}

func TestWriterPreservesExistingFrontmatter(t *testing.T) {
	writer := Writer{Inbox: t.TempDir(), Now: func() time.Time {
		return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	}}
	content := "---\n# keep this comment\ntitle: User title\ntags: [existing]\ncustom: untouched\n---\nOriginal body\n"
	result, err := writer.Write(context.Background(), Request{
		Content: content, Title: "CLI title", Tags: []string{"newtag"}, Collection: "reading",
	})
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, want := range []string{"# keep this comment", "title: User title", "tags: [existing]", "custom: untouched", "collection: \"reading\"", "Original body", "#newtag"} {
		if !strings.Contains(text, want) {
			t.Errorf("frontmatter-preserving capture missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "title: \"CLI title\"") {
		t.Fatalf("existing title was overwritten:\n%s", text)
	}
}

func TestWriterDeduplicatesNormalizedSourceURL(t *testing.T) {
	writer := Writer{Inbox: t.TempDir()}
	first, err := writer.Write(context.Background(), Request{Content: "first", Title: "First", SourceURL: "https://example.com/page"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := writer.Write(context.Background(), Request{Content: "second", Title: "Changed title", SourceURL: "https://example.com/page"})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.Path != first.Path {
		t.Fatalf("duplicate result = %+v, first = %+v", second, first)
	}
	content, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "second") {
		t.Fatalf("duplicate capture overwrote source file: %s", content)
	}
}

func TestWriterRejectsInvalidInput(t *testing.T) {
	writer := Writer{Inbox: t.TempDir()}
	if _, err := writer.Write(context.Background(), Request{Content: "   "}); err == nil {
		t.Fatal("empty capture succeeded")
	}
	if _, err := writer.Write(context.Background(), Request{Content: "text", Tags: []string{"not a tag"}}); err == nil {
		t.Fatal("invalid tag succeeded")
	}
	if _, err := writer.Write(context.Background(), Request{Content: strings.Repeat("x", MaxCaptureBytes+1)}); err == nil {
		t.Fatal("oversized capture succeeded")
	}
}
