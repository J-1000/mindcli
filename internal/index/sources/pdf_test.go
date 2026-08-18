package sources

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/storage"
)

func TestPDFSourceName(t *testing.T) {
	src := NewPDFSource([]string{"/tmp"}, nil)
	if src.Name() != storage.SourcePDF {
		t.Errorf("Name() = %q, want %q", src.Name(), storage.SourcePDF)
	}
}

func TestPDFOCRUsesBoundedPageImages(t *testing.T) {
	originalLookPath := execLookPath
	originalRunCommand := runCommand
	t.Cleanup(func() {
		execLookPath = originalLookPath
		runCommand = originalRunCommand
	})
	execLookPath = func(name string) (string, error) { return name, nil }
	runCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "pdftoppm" {
			prefix := args[len(args)-1]
			if err := os.WriteFile(prefix+"-1.png", []byte("page-one"), 0o600); err != nil {
				return nil, err
			}
			if err := os.WriteFile(prefix+"-2.png", []byte("page-two"), 0o600); err != nil {
				return nil, err
			}
			return nil, nil
		}
		if len(args) == 0 {
			return nil, nil
		}
		if strings.Contains(filepath.Base(args[0]), "-1.png") {
			return []byte("recognized first page"), nil
		}
		return []byte("recognized second page"), nil
	}

	source := NewPDFSource(nil, nil)
	source.SetOCROptions(PDFOCROptions{
		Enabled: true, Command: "tesseract", Renderer: "pdftoppm", Languages: []string{"eng", "deu"},
		MaxPages: 2, Timeout: time.Second, MinTextChars: 10, RenderDPI: 100, MaxRenderedMB: 1,
	})
	pages, truncated, err := source.extractPDFOCR(context.Background(), "fixture.pdf", 3)
	if err != nil {
		t.Fatalf("extractPDFOCR: %v", err)
	}
	if !truncated || len(pages) != 2 || pages[0] != "recognized first page" || pages[1] != "recognized second page" {
		t.Fatalf("pages=%#v truncated=%v", pages, truncated)
	}
	formatted := formatPDFPages(pages)
	if !strings.Contains(formatted, "## Page 1") || !strings.Contains(formatted, "## Page 2") {
		t.Fatalf("formatted pages = %q", formatted)
	}
}

func TestGeneratePreview(t *testing.T) {
	tests := []struct {
		name    string
		content string
		maxLen  int
		want    string
	}{
		{
			name:    "short content",
			content: "Hello world",
			maxLen:  100,
			want:    "Hello world",
		},
		{
			name:    "long content truncated",
			content: "This is a longer piece of text that should be truncated at a word boundary for the preview.",
			maxLen:  50,
			want:    "This is a longer piece of text that should be...",
		},
		{
			name:    "multiline collapsed",
			content: "Line one\n\nLine two\n\nLine three",
			maxLen:  100,
			want:    "Line one Line two Line three",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generatePreview(tt.content, tt.maxLen)
			if got != tt.want {
				t.Errorf("generatePreview() = %q, want %q", got, tt.want)
			}
		})
	}
}
