package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
	"github.com/ledongthuc/pdf"
)

// PDFSource indexes PDF files.
type PDFSource struct {
	scanner *Scanner
}

// NewPDFSource creates a new PDF source.
func NewPDFSource(paths, ignore []string) *PDFSource {
	return &PDFSource{
		scanner: NewScanner(ScanConfig{
			Paths:      paths,
			Extensions: []string{".pdf"},
			Ignore:     ignore,
		}),
	}
}

// Name returns the source name.
func (p *PDFSource) Name() storage.Source {
	return storage.SourcePDF
}

// Scan walks configured paths and returns PDF files to index.
func (p *PDFSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	return p.scanner.Scan(ctx)
}

// Parse reads a PDF file and returns the parsed document.
func (p *PDFSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	content, err := extractPDFText(file.Path)
	if err != nil {
		return nil, fmt.Errorf("extracting PDF text: %w", err)
	}

	// Generate stable ID from path.
	pathHash := sha256.Sum256([]byte(file.Path))
	id := hex.EncodeToString(pathHash[:8])

	// Title from filename.
	title := strings.TrimSuffix(filepath.Base(file.Path), ".pdf")

	// Generate preview.
	preview := generatePreview(content, 500)

	// Content hash for change detection.
	contentHash := sha256.Sum256([]byte(content))

	// Get file info for metadata.
	info, _ := os.Stat(file.Path)
	var modTime time.Time
	if info != nil {
		modTime = info.ModTime()
	} else {
		modTime = time.Unix(file.ModifiedAt, 0)
	}

	return &storage.Document{
		ID:          id,
		Source:      storage.SourcePDF,
		Path:        file.Path,
		Title:       title,
		Content:     content,
		Preview:     preview,
		ContentHash: hex.EncodeToString(contentHash[:]),
		IndexedAt:   time.Now(),
		ModifiedAt:  modTime,
	}, nil
}

// extractPDFText extracts plain text from a PDF file.
func extractPDFText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening PDF: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	numPages := r.NumPage()

	for i := 1; i <= numPages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			continue // Skip pages that fail to parse.
		}
		sb.WriteString(text)
		if i < numPages {
			sb.WriteString("\n\n")
		}
	}

	return strings.TrimSpace(sb.String()), nil
}

// generatePreview creates a truncated preview of the content.
func generatePreview(content string, maxLen int) string {
	// Collapse multiple whitespace.
	content = strings.Join(strings.Fields(content), " ")
	if len(content) <= maxLen {
		return content
	}

	// Truncate at word boundary.
	truncated := content[:maxLen]
	if lastSpace := strings.LastIndex(truncated, " "); lastSpace > maxLen/2 {
		truncated = truncated[:lastSpace]
	}
	return truncated + "..."
}
