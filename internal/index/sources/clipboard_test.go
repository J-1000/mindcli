package sources

import (
	"testing"

	"github.com/jankowtf/mindcli/internal/storage"
)

func TestClipboardSourceName(t *testing.T) {
	src := NewClipboardSource(nil, 30, true)
	if src.Name() != storage.SourceClipboard {
		t.Errorf("Name() = %q, want %q", src.Name(), storage.SourceClipboard)
	}
}

func TestLooksLikePassword(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"Passw0rd!", true},
		{"xK9#mQ2!nL", true},
		{"simple", false},
		{"hello world", false},     // has space
		{"line1\nline2", false},    // multiline
		{"ab", false},              // too short
		{"ALL_UPPERCASE_LETTERS", false}, // only 2 classes
		{"this is a normal sentence with some words", false},
	}

	for _, tt := range tests {
		got := looksLikePassword(tt.text)
		if got != tt.want {
			t.Errorf("looksLikePassword(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"hello\nworld", "hello"},
		{"single line", "single line"},
		{"", ""},
		{"  trimmed  \nsecond", "trimmed"},
	}

	for _, tt := range tests {
		got := firstLine(tt.text)
		if got != tt.want {
			t.Errorf("firstLine(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}
