// Package chunker provides text chunking utilities for splitting documents
// into overlapping chunks suitable for embedding.
package chunker

import (
	"strings"
	"unicode"
)

// DefaultChunkSize is the default target chunk size in characters.
const DefaultChunkSize = 512

// DefaultOverlap is the default overlap between chunks in characters.
const DefaultOverlap = 64

// Chunk represents a piece of text from a document.
type Chunk struct {
	Content  string
	StartPos int
	EndPos   int
}

// Options configures the chunking behavior.
type Options struct {
	ChunkSize int // Target chunk size in characters
	Overlap   int // Overlap between consecutive chunks
}

// DefaultOptions returns sensible default chunking options.
func DefaultOptions() Options {
	return Options{
		ChunkSize: DefaultChunkSize,
		Overlap:   DefaultOverlap,
	}
}

// Split divides text into overlapping chunks that respect semantic boundaries
// (paragraphs, then sentences). Returns nil for empty text.
func Split(text string, opts Options) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if opts.ChunkSize <= 0 {
		opts.ChunkSize = DefaultChunkSize
	}
	if opts.Overlap < 0 {
		opts.Overlap = 0
	}
	if opts.Overlap >= opts.ChunkSize {
		opts.Overlap = opts.ChunkSize / 4
	}

	// If text fits in a single chunk, return it directly.
	if len(text) <= opts.ChunkSize {
		return []Chunk{{Content: text, StartPos: 0, EndPos: len(text)}}
	}

	// Split into paragraphs first, then merge/split to target size.
	paragraphs := splitParagraphs(text)
	return mergeAndSplit(text, paragraphs, opts)
}

// splitParagraphs splits text into paragraph segments, preserving positions.
func splitParagraphs(text string) []segment {
	var segments []segment
	start := 0

	for start < len(text) {
		// Find end of paragraph (double newline or end of text).
		end := strings.Index(text[start:], "\n\n")
		if end == -1 {
			segments = append(segments, segment{
				content:  strings.TrimSpace(text[start:]),
				startPos: start,
				endPos:   len(text),
			})
			break
		}

		end += start
		para := strings.TrimSpace(text[start:end])
		if para != "" {
			segments = append(segments, segment{
				content:  para,
				startPos: start,
				endPos:   end,
			})
		}
		start = end + 2 // Skip past \n\n
	}

	return segments
}

// segment is an internal representation of a text span.
type segment struct {
	content  string
	startPos int
	endPos   int
}

// mergeAndSplit combines paragraphs into chunks of the target size,
// splitting oversized paragraphs at sentence boundaries.
func mergeAndSplit(fullText string, paragraphs []segment, opts Options) []Chunk {
	var chunks []Chunk
	var current strings.Builder
	currentStart := -1

	flush := func() {
		content := strings.TrimSpace(current.String())
		if content != "" {
			chunks = append(chunks, Chunk{
				Content:  content,
				StartPos: currentStart,
				EndPos:   currentStart + current.Len(),
			})
		}
		current.Reset()
		currentStart = -1
	}

	for _, para := range paragraphs {
		// If this paragraph alone exceeds chunk size, split at sentence boundaries.
		if len(para.content) > opts.ChunkSize {
			flush()
			sentenceChunks := splitBySentences(para.content, para.startPos, opts)
			chunks = append(chunks, sentenceChunks...)
			continue
		}

		// If adding this paragraph would exceed chunk size, flush current.
		projectedLen := current.Len()
		if projectedLen > 0 {
			projectedLen += 2 // for "\n\n" separator
		}
		projectedLen += len(para.content)

		if projectedLen > opts.ChunkSize && current.Len() > 0 {
			flush()
		}

		if currentStart == -1 {
			currentStart = para.startPos
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para.content)
	}
	flush()

	// Apply overlap between consecutive chunks.
	if opts.Overlap > 0 && len(chunks) > 1 {
		chunks = applyOverlap(fullText, chunks, opts.Overlap)
	}

	return chunks
}

// splitBySentences splits a long paragraph into chunks at sentence boundaries.
func splitBySentences(text string, basePos int, opts Options) []Chunk {
	sentences := findSentences(text)
	if len(sentences) == 0 {
		return []Chunk{{Content: text, StartPos: basePos, EndPos: basePos + len(text)}}
	}

	var chunks []Chunk
	var current strings.Builder
	currentStart := 0

	for _, sent := range sentences {
		projectedLen := current.Len()
		if projectedLen > 0 {
			projectedLen++ // space
		}
		projectedLen += len(sent.content)

		if projectedLen > opts.ChunkSize && current.Len() > 0 {
			content := strings.TrimSpace(current.String())
			if content != "" {
				chunks = append(chunks, Chunk{
					Content:  content,
					StartPos: basePos + currentStart,
					EndPos:   basePos + currentStart + current.Len(),
				})
			}
			current.Reset()
			currentStart = sent.startPos
		}

		if current.Len() == 0 {
			currentStart = sent.startPos
		} else {
			current.WriteByte(' ')
		}
		current.WriteString(sent.content)
	}

	// Flush remaining.
	content := strings.TrimSpace(current.String())
	if content != "" {
		chunks = append(chunks, Chunk{
			Content:  content,
			StartPos: basePos + currentStart,
			EndPos:   basePos + currentStart + current.Len(),
		})
	}

	return chunks
}

// findSentences splits text into sentence-like segments.
func findSentences(text string) []segment {
	var sentences []segment
	start := 0
	runes := []rune(text)

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '.' || r == '!' || r == '?' {
			// Look ahead: if followed by space+uppercase or end, it's a boundary.
			if i+1 >= len(runes) || (i+2 < len(runes) && unicode.IsSpace(runes[i+1]) && unicode.IsUpper(runes[i+2])) {
				byteEnd := len(string(runes[:i+1]))
				byteStart := len(string(runes[:start]))
				sent := strings.TrimSpace(string(runes[start : i+1]))
				if sent != "" {
					sentences = append(sentences, segment{
						content:  sent,
						startPos: byteStart,
						endPos:   byteEnd,
					})
				}
				// Skip whitespace.
				for i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
					i++
				}
				start = i + 1
			}
		}
	}

	// Remaining text.
	if start < len(runes) {
		byteStart := len(string(runes[:start]))
		sent := strings.TrimSpace(string(runes[start:]))
		if sent != "" {
			sentences = append(sentences, segment{
				content:  sent,
				startPos: byteStart,
				endPos:   len(text),
			})
		}
	}

	return sentences
}

// applyOverlap extends each chunk (except the first) to include text from
// the end of the previous chunk, creating overlapping context windows.
func applyOverlap(fullText string, chunks []Chunk, overlap int) []Chunk {
	if len(chunks) <= 1 {
		return chunks
	}

	result := make([]Chunk, len(chunks))
	result[0] = chunks[0]

	for i := 1; i < len(chunks); i++ {
		prevEnd := chunks[i-1].EndPos
		overlapStart := prevEnd - overlap
		if overlapStart < chunks[i-1].StartPos {
			overlapStart = chunks[i-1].StartPos
		}
		if overlapStart < 0 {
			overlapStart = 0
		}

		// Find a word boundary for clean overlap.
		overlapStart = findWordBoundary(fullText, overlapStart, true)

		overlapText := strings.TrimSpace(fullText[overlapStart:prevEnd])
		mainText := strings.TrimSpace(fullText[chunks[i].StartPos:chunks[i].EndPos])

		combined := overlapText + " " + mainText
		result[i] = Chunk{
			Content:  strings.TrimSpace(combined),
			StartPos: overlapStart,
			EndPos:   chunks[i].EndPos,
		}
	}

	return result
}

// findWordBoundary finds the nearest word boundary at or after pos.
func findWordBoundary(text string, pos int, forward bool) int {
	if pos >= len(text) {
		return len(text)
	}
	if pos <= 0 {
		return 0
	}

	if forward {
		for pos < len(text) && !unicode.IsSpace(rune(text[pos])) {
			pos++
		}
		for pos < len(text) && unicode.IsSpace(rune(text[pos])) {
			pos++
		}
	}
	return pos
}
