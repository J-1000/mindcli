package sources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/J-1000/mindcli/internal/storage"
	"github.com/ledongthuc/pdf"
)

// PDFSource indexes PDF files.
type PDFSource struct {
	scanner *Scanner
	ocr     PDFOCROptions
}

// PDFOCROptions configures the optional local OCR fallback. OCR is only used
// when ordinary PDF extraction yields fewer than MinTextChars visible chars.
type PDFOCROptions struct {
	Enabled       bool
	Command       string
	Renderer      string
	Languages     []string
	MaxPages      int
	Timeout       time.Duration
	MinTextChars  int
	RenderDPI     int
	MaxRenderedMB int64
}

func DefaultPDFOCROptions() PDFOCROptions {
	return PDFOCROptions{
		Command:       "tesseract",
		Renderer:      "pdftoppm",
		Languages:     []string{"eng"},
		MaxPages:      25,
		Timeout:       2 * time.Minute,
		MinTextChars:  80,
		RenderDPI:     200,
		MaxRenderedMB: 32,
	}
}

// NewPDFSource creates a new PDF source.
func NewPDFSource(paths, ignore []string) *PDFSource {
	return &PDFSource{
		scanner: NewScanner(ScanConfig{
			Paths:      paths,
			Extensions: []string{".pdf"},
			Ignore:     ignore,
		}),
		ocr: DefaultPDFOCROptions(),
	}
}

// SetOCROptions enables or tunes the local OCR fallback.
func (p *PDFSource) SetOCROptions(options PDFOCROptions) {
	defaults := DefaultPDFOCROptions()
	if strings.TrimSpace(options.Command) == "" {
		options.Command = defaults.Command
	}
	if strings.TrimSpace(options.Renderer) == "" {
		options.Renderer = defaults.Renderer
	}
	if len(options.Languages) == 0 {
		options.Languages = defaults.Languages
	}
	if options.MaxPages < 1 {
		options.MaxPages = defaults.MaxPages
	}
	if options.Timeout <= 0 {
		options.Timeout = defaults.Timeout
	}
	if options.MinTextChars < 0 {
		options.MinTextChars = defaults.MinTextChars
	}
	if options.RenderDPI < 1 {
		options.RenderDPI = defaults.RenderDPI
	}
	if options.MaxRenderedMB < 1 {
		options.MaxRenderedMB = defaults.MaxRenderedMB
	}
	p.ocr = options
}

// Name returns the source name.
func (p *PDFSource) Name() storage.Source {
	return storage.SourcePDF
}

// Scan walks configured paths and returns PDF files to index.
func (p *PDFSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	return p.scanner.Scan(ctx)
}

// MatchesPath reports whether this source is configured to handle the path.
func (p *PDFSource) MatchesPath(path string) bool {
	return p.scanner.MatchesPath(path)
}

// Parse reads a PDF file and returns the parsed document.
func (p *PDFSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	pages, failedPages, err := extractPDFPages(file.Path)
	if err != nil {
		return nil, fmt.Errorf("extracting PDF text: %w", err)
	}
	content := formatPDFPages(pages)
	metadata := map[string]string{
		"format":            "pdf",
		"original_path":     file.Path,
		"page_count":        strconv.Itoa(len(pages)),
		"location":          pdfPageRange(len(pages)),
		"extraction_method": "pdf_text",
		"confidence":        "high",
	}
	if len(failedPages) > 0 {
		metadata["failed_pages"] = joinPageNumbers(failedPages)
		metadata["extraction_warning"] = fmt.Sprintf("plain-text extraction failed on %d page(s)", len(failedPages))
	}

	if visibleCharacterCount(content) < p.ocr.MinTextChars {
		if !p.ocr.Enabled {
			metadata["confidence"] = "low"
			metadata["extraction_warning"] = fmt.Sprintf(
				"only %d extractable characters; local OCR is disabled",
				visibleCharacterCount(content),
			)
			metadata["ocr_available"] = "enable sources.pdf.ocr_enabled"
		} else {
			ocrPages, truncated, ocrErr := p.extractPDFOCR(ctx, file.Path, len(pages))
			if ocrErr != nil {
				return nil, fmt.Errorf("OCR fallback: %w", ocrErr)
			}
			content = formatPDFPages(ocrPages)
			if visibleCharacterCount(content) == 0 {
				return nil, fmt.Errorf("OCR produced no readable text")
			}
			metadata["extraction_method"] = "ocr_tesseract"
			metadata["confidence"] = "low"
			metadata["ocr_pages"] = strconv.Itoa(len(ocrPages))
			metadata["ocr_languages"] = strings.Join(p.ocr.Languages, "+")
			metadata["location"] = pdfPageRange(len(ocrPages))
			if truncated {
				metadata["ocr_truncated"] = "true"
				metadata["extraction_warning"] = fmt.Sprintf("OCR limited to the first %d of %d pages", len(ocrPages), len(pages))
			}
		}
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
		Metadata:    metadata,
		ContentHash: hex.EncodeToString(contentHash[:]),
		IndexedAt:   time.Now(),
		ModifiedAt:  modTime,
	}, nil
}

// extractPDFText extracts plain text from a PDF file.
func extractPDFText(path string) (string, error) {
	pages, _, err := extractPDFPages(path)
	if err != nil {
		return "", err
	}
	return formatPDFPages(pages), nil
}

// extractPDFPages preserves page boundaries and reports pages that the PDF
// library could not decode instead of silently discarding them.
func extractPDFPages(path string) ([]string, []int, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening PDF: %w", err)
	}
	numPages := r.NumPage()
	pages := make([]string, numPages)
	var failed []int

	for i := 1; i <= numPages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			failed = append(failed, i)
			continue
		}

		text, err := page.GetPlainText(nil)
		if err != nil {
			failed = append(failed, i)
			continue
		}
		pages[i-1] = strings.TrimSpace(text)
	}
	if err := f.Close(); err != nil {
		return nil, nil, fmt.Errorf("closing PDF: %w", err)
	}

	return pages, failed, nil
}

func (p *PDFSource) extractPDFOCR(ctx context.Context, path string, pageCount int) ([]string, bool, error) {
	ocrCommand, err := execLookPath(p.ocr.Command)
	if err != nil {
		return nil, false, fmt.Errorf("OCR command %q not found", p.ocr.Command)
	}
	renderer, err := execLookPath(p.ocr.Renderer)
	if err != nil {
		return nil, false, fmt.Errorf("PDF renderer %q not found", p.ocr.Renderer)
	}
	maxPages := p.ocr.MaxPages
	if pageCount > 0 && maxPages > pageCount {
		maxPages = pageCount
	}
	timedCtx, cancel := context.WithTimeout(ctx, p.ocr.Timeout)
	defer cancel()
	tempDir, err := os.MkdirTemp("", "mindcli-ocr-")
	if err != nil {
		return nil, false, fmt.Errorf("creating private temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	prefix := filepath.Join(tempDir, "page")
	args := []string{"-f", "1", "-l", strconv.Itoa(maxPages), "-r", strconv.Itoa(p.ocr.RenderDPI), "-png", path, prefix}
	if output, runErr := runCommand(timedCtx, renderer, args...); runErr != nil {
		return nil, false, fmt.Errorf("rendering PDF pages: %w: %s", runErr, truncateCommandOutput(output))
	}
	images, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return nil, false, err
	}
	sort.Strings(images)
	if len(images) == 0 {
		return nil, false, fmt.Errorf("renderer produced no page images")
	}
	if len(images) > maxPages {
		images = images[:maxPages]
	}
	var renderedBytes int64
	for _, imagePath := range images {
		info, statErr := os.Stat(imagePath)
		if statErr != nil {
			return nil, false, statErr
		}
		renderedBytes += info.Size()
		if renderedBytes > int64(maxPages)*p.ocr.MaxRenderedMB<<20 {
			return nil, false, fmt.Errorf("rendered page images exceed %d MiB limit", int64(maxPages)*p.ocr.MaxRenderedMB)
		}
	}

	language := strings.Join(p.ocr.Languages, "+")
	pages := make([]string, 0, len(images))
	for index, imagePath := range images {
		args := []string{imagePath, "stdout"}
		if language != "" {
			args = append(args, "-l", language)
		}
		output, runErr := runCommand(timedCtx, ocrCommand, args...)
		if runErr != nil {
			return nil, false, fmt.Errorf("recognizing page %d: %w: %s", index+1, runErr, truncateCommandOutput(output))
		}
		pages = append(pages, strings.TrimSpace(string(output)))
	}
	return pages, pageCount > len(pages), nil
}

var execLookPath = exec.LookPath

var runCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.Output()
	if err != nil && stderr.Len() > 0 {
		return stderr.Bytes(), err
	}
	return stdout, err
}

func formatPDFPages(pages []string) string {
	var content strings.Builder
	for index, page := range pages {
		if strings.TrimSpace(page) == "" {
			continue
		}
		if content.Len() > 0 {
			content.WriteString("\n\n")
		}
		fmt.Fprintf(&content, "## Page %d\n\n%s", index+1, strings.TrimSpace(page))
	}
	return strings.TrimSpace(content.String())
}

func visibleCharacterCount(value string) int {
	count := 0
	for _, char := range value {
		if !unicode.IsSpace(char) && !unicode.IsPunct(char) {
			count++
		}
	}
	return count
}

func pdfPageRange(count int) string {
	if count < 1 {
		return "pages:none"
	}
	return fmt.Sprintf("pages:1-%d", count)
}

func joinPageNumbers(pages []int) string {
	values := make([]string, len(pages))
	for index, page := range pages {
		values[index] = strconv.Itoa(page)
	}
	return strings.Join(values, ",")
}

func truncateCommandOutput(output []byte) string {
	const max = 1000
	value := strings.TrimSpace(string(output))
	if len(value) > max {
		value = value[:max] + "..."
	}
	return value
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
