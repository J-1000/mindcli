package sources

import (
	"testing"

	"github.com/jankowtf/mindcli/internal/storage"
)

func TestPDFSourceName(t *testing.T) {
	src := NewPDFSource([]string{"/tmp"}, nil)
	if src.Name() != storage.SourcePDF {
		t.Errorf("Name() = %q, want %q", src.Name(), storage.SourcePDF)
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
