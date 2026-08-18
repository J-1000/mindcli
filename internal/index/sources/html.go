package sources

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/storage"
	"golang.org/x/net/html"
)

var (
	webArchiveDataPattern = regexp.MustCompile(`(?is)<key>\s*WebResourceData\s*</key>\s*<data>\s*([^<]+?)\s*</data>`)
	webArchiveURLPattern  = regexp.MustCompile(`(?is)<key>\s*WebResourceURL\s*</key>\s*<string>\s*([^<]+?)\s*</string>`)
)

// HTMLSource indexes HTML pages, MIME web archives, and Safari webarchives.
type HTMLSource struct {
	scanner              *Scanner
	maxFileBytes         int64
	maxDecompressedBytes int64
}

// NewHTMLSource creates a bounded local HTML source.
func NewHTMLSource(paths, ignore []string, maxFileBytes, maxDecompressedBytes int64) *HTMLSource {
	return &HTMLSource{
		scanner: NewScanner(ScanConfig{
			Paths:      paths,
			Extensions: []string{".html", ".htm", ".mhtml", ".mht", ".webarchive"},
			Ignore:     ignore,
		}),
		maxFileBytes:         maxFileBytes,
		maxDecompressedBytes: maxDecompressedBytes,
	}
}

func (h *HTMLSource) Name() storage.Source { return storage.SourceHTML }

func (h *HTMLSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	return h.scanner.Scan(ctx)
}

func (h *HTMLSource) MatchesPath(path string) bool { return h.scanner.MatchesPath(path) }

func (h *HTMLSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := readFileBounded(file.Path, h.maxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("reading HTML artifact: %w", err)
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(file.Path)), ".")
	htmlData := data
	sourceURL := ""
	extractionMethod := "html_text"
	switch ext {
	case "mhtml", "mht":
		htmlData, sourceURL, err = extractMHTML(data, h.maxDecompressedBytes)
		extractionMethod = "mhtml_text"
	case "webarchive":
		htmlData, sourceURL, err = extractWebArchiveHTML(data, h.maxDecompressedBytes)
		extractionMethod = "webarchive_text"
	}
	if err != nil {
		return nil, fmt.Errorf("extracting %s: %w", ext, err)
	}

	title, content, err := extractHTMLText(bytes.NewReader(htmlData))
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	}
	metadata := map[string]string{
		"format":            ext,
		"original_path":     file.Path,
		"location":          "document",
		"extraction_method": extractionMethod,
	}
	if sourceURL != "" {
		metadata["source_url"] = sourceURL
	}

	return extractedDocument(storage.SourceHTML, file, title, content, metadata), nil
}

func extractMHTML(data []byte, maxDecompressedBytes int64) ([]byte, string, error) {
	message, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	budget := newArchiveBudget(maxDecompressedBytes)
	return findMIMEHTML(textproto.MIMEHeader(message.Header), message.Body, budget, 0)
}

func findMIMEHTML(header textproto.MIMEHeader, body io.Reader, budget *archiveBudget, depth int) ([]byte, string, error) {
	if depth > 8 {
		return nil, "", fmt.Errorf("MIME nesting exceeds 8 levels")
	}
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		mediaType = "text/html"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, "", fmt.Errorf("multipart archive has no boundary")
		}
		reader := multipart.NewReader(body, boundary)
		var fallback []byte
		var fallbackURL string
		for {
			part, partErr := reader.NextPart()
			if partErr == io.EOF {
				break
			}
			if partErr != nil {
				return nil, "", partErr
			}
			content, location, childErr := findMIMEHTML(part.Header, part, budget, depth+1)
			_ = part.Close()
			if childErr != nil {
				continue
			}
			childType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if strings.EqualFold(childType, "text/html") || strings.EqualFold(childType, "application/xhtml+xml") {
				return content, location, nil
			}
			if fallback == nil && len(content) > 0 {
				fallback, fallbackURL = content, location
			}
		}
		if fallback != nil {
			return fallback, fallbackURL, nil
		}
		return nil, "", fmt.Errorf("archive contains no textual page")
	}
	if mediaType != "text/html" && mediaType != "application/xhtml+xml" && mediaType != "text/plain" {
		return nil, "", fmt.Errorf("unsupported MIME part %q", mediaType)
	}

	decoded := body
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, body)
	}
	content, err := readBounded(decoded, budget.remaining)
	if err != nil {
		return nil, "", err
	}
	budget.remaining -= int64(len(content))
	if mediaType == "text/plain" {
		content = []byte("<html><body><pre>" + html.EscapeString(string(content)) + "</pre></body></html>")
	}
	return content, strings.TrimSpace(header.Get("Content-Location")), nil
}

func extractWebArchiveHTML(data []byte, maxDecompressedBytes int64) ([]byte, string, error) {
	if int64(len(data)) > maxDecompressedBytes {
		return nil, "", fmt.Errorf("webarchive exceeds %d-byte expansion limit", maxDecompressedBytes)
	}
	lower := bytes.ToLower(data)
	start := bytes.Index(lower, []byte("<html"))
	if doctype := bytes.Index(lower, []byte("<!doctype html")); doctype >= 0 && (start < 0 || doctype < start) {
		start = doctype
	}
	if start >= 0 {
		end := bytes.Index(lower[start:], []byte("</html>"))
		if end >= 0 {
			end += start + len("</html>")
			return data[start:end], "", nil
		}
	}

	match := webArchiveDataPattern.FindSubmatch(data)
	if len(match) < 2 {
		return nil, "", fmt.Errorf("webarchive contains no HTML main resource")
	}
	encoded := strings.Join(strings.Fields(string(match[1])), "")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", fmt.Errorf("decoding main resource: %w", err)
	}
	if int64(len(decoded)) > maxDecompressedBytes {
		return nil, "", fmt.Errorf("webarchive main resource exceeds %d-byte expansion limit", maxDecompressedBytes)
	}
	sourceURL := ""
	if urlMatch := webArchiveURLPattern.FindSubmatch(data); len(urlMatch) > 1 {
		sourceURL = strings.TrimSpace(html.UnescapeString(string(urlMatch[1])))
	}
	return decoded, sourceURL, nil
}

func extractHTMLText(reader io.Reader) (string, string, error) {
	root, err := html.Parse(reader)
	if err != nil {
		return "", "", err
	}
	var title string
	var text strings.Builder
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, hidden bool) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "script", "style", "noscript", "svg", "template":
				hidden = true
			case "br", "p", "div", "article", "section", "header", "footer", "li", "tr",
				"h1", "h2", "h3", "h4", "h5", "h6", "pre", "blockquote":
				text.WriteByte('\n')
			}
		}
		if node.Type == html.TextNode && !hidden {
			value := strings.TrimSpace(node.Data)
			if value != "" {
				if node.Parent != nil && node.Parent.Type == html.ElementNode && node.Parent.Data == "title" && title == "" {
					title = strings.Join(strings.Fields(value), " ")
				}
				text.WriteString(value)
				text.WriteByte(' ')
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	walk(root, false)
	return title, normalizeExtractedText(text.String()), nil
}

func normalizeExtractedText(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := true
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if !blank {
				out = append(out, "")
				blank = true
			}
			continue
		}
		out = append(out, line)
		blank = false
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func extractedDocument(source storage.Source, file FileInfo, title, content string, metadata map[string]string) *storage.Document {
	return &storage.Document{
		ID:          stableDocumentID(source, file.Path),
		Source:      source,
		Path:        file.Path,
		Title:       title,
		Content:     content,
		Preview:     generatePreview(content, 500),
		Metadata:    metadata,
		ContentHash: hashContent(content),
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Unix(file.ModifiedAt, 0),
	}
}

func stableDocumentID(source storage.Source, parts ...string) string {
	digest := hashContent(string(source) + "\x00" + strings.Join(parts, "\x00"))
	return string(source) + ":" + digest[:32]
}
