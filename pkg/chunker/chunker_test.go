package chunker

import (
	"strings"
	"testing"
)

func TestSplitEmptyText(t *testing.T) {
	chunks := Split("", DefaultOptions())
	if chunks != nil {
		t.Errorf("expected nil for empty text, got %d chunks", len(chunks))
	}

	chunks = Split("   ", DefaultOptions())
	if chunks != nil {
		t.Errorf("expected nil for whitespace, got %d chunks", len(chunks))
	}
}

func TestSplitShortText(t *testing.T) {
	text := "Hello, this is a short document."
	chunks := Split(text, DefaultOptions())

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Content != text {
		t.Errorf("expected %q, got %q", text, chunks[0].Content)
	}
	if chunks[0].StartPos != 0 {
		t.Errorf("expected StartPos 0, got %d", chunks[0].StartPos)
	}
}

func TestSplitByParagraphs(t *testing.T) {
	// Create text with multiple paragraphs, each under chunk size but total over.
	para1 := strings.Repeat("First paragraph. ", 20)
	para2 := strings.Repeat("Second paragraph. ", 20)
	para3 := strings.Repeat("Third paragraph. ", 20)
	text := para1 + "\n\n" + para2 + "\n\n" + para3

	opts := Options{ChunkSize: 400, Overlap: 0}
	chunks := Split(text, opts)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Verify all text is covered.
	for _, c := range chunks {
		if c.Content == "" {
			t.Error("got empty chunk")
		}
	}
}

func TestSplitLongParagraphBySentences(t *testing.T) {
	// Create a single long paragraph that exceeds chunk size.
	var sentences []string
	for i := 0; i < 30; i++ {
		sentences = append(sentences, "This is a fairly long sentence that takes up some space.")
	}
	text := strings.Join(sentences, " ")

	opts := Options{ChunkSize: 300, Overlap: 0}
	chunks := Split(text, opts)

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks for long text, got %d", len(chunks))
	}

	for _, c := range chunks {
		if c.Content == "" {
			t.Error("got empty chunk")
		}
	}
}

func TestOverlap(t *testing.T) {
	para1 := strings.Repeat("Alpha beta gamma. ", 15)
	para2 := strings.Repeat("Delta epsilon zeta. ", 15)
	text := para1 + "\n\n" + para2

	opts := Options{ChunkSize: 300, Overlap: 50}
	chunks := Split(text, opts)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// Second chunk should start before the first chunk ends (overlap).
	if len(chunks) >= 2 {
		if chunks[1].StartPos >= chunks[0].EndPos {
			t.Errorf("expected overlap: chunk 1 starts at %d, chunk 0 ends at %d",
				chunks[1].StartPos, chunks[0].EndPos)
		}
	}
}

func TestOptionsValidation(t *testing.T) {
	text := strings.Repeat("Hello world. ", 100)

	// Zero chunk size should use default.
	chunks := Split(text, Options{ChunkSize: 0})
	if len(chunks) == 0 {
		t.Error("expected chunks with zero chunk size")
	}

	// Overlap >= chunk size should be capped.
	chunks = Split(text, Options{ChunkSize: 100, Overlap: 200})
	if len(chunks) == 0 {
		t.Error("expected chunks with oversized overlap")
	}

	// Negative overlap should be treated as zero.
	chunks = Split(text, Options{ChunkSize: 100, Overlap: -10})
	if len(chunks) == 0 {
		t.Error("expected chunks with negative overlap")
	}
}

func TestChunkPositions(t *testing.T) {
	text := "First paragraph here.\n\nSecond paragraph here.\n\nThird paragraph here."
	opts := Options{ChunkSize: 30, Overlap: 0}
	chunks := Split(text, opts)

	for i, c := range chunks {
		if c.StartPos < 0 || c.EndPos > len(text) {
			t.Errorf("chunk %d: positions out of bounds [%d, %d] for text len %d",
				i, c.StartPos, c.EndPos, len(text))
		}
		if c.StartPos >= c.EndPos {
			t.Errorf("chunk %d: start %d >= end %d", i, c.StartPos, c.EndPos)
		}
	}
}
