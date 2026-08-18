package sources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/J-1000/mindcli/internal/storage"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	defaultBrowserMaxResponseBytes = int64(2 << 20)
	defaultBrowserRequestTimeout   = 10 * time.Second
	defaultBrowserFetchConcurrency = 4
	defaultBrowserMaxPages         = 5000
	defaultBrowserRetentionDays    = 365
)

var (
	errBrowserDomainBlocked    = errors.New("browser content domain is blocked")
	errBrowserUnsupportedURL   = errors.New("browser content URL is unsupported")
	errBrowserUnsupportedType  = errors.New("browser content type is unsupported")
	errBrowserResponseTooLarge = errors.New("browser content response is too large")
)

// BrowserOptions bounds browser history retention and optional reader-mode
// page fetching. IncludeContent is false by default.
type BrowserOptions struct {
	IncludeContent   bool
	AllowedDomains   []string
	DeniedDomains    []string
	MaxResponseBytes int64
	RequestTimeout   time.Duration
	FetchConcurrency int
	MaxPages         int
	RetentionDays    int
}

// ReaderPage is bounded textual content extracted from one public web URL.
type ReaderPage struct {
	Content     string
	FinalURL    string
	ContentType string
}

// DefaultBrowserOptions returns privacy-preserving, bounded browser defaults.
func DefaultBrowserOptions() BrowserOptions {
	return BrowserOptions{
		IncludeContent:   false,
		MaxResponseBytes: defaultBrowserMaxResponseBytes,
		RequestTimeout:   defaultBrowserRequestTimeout,
		FetchConcurrency: defaultBrowserFetchConcurrency,
		MaxPages:         defaultBrowserMaxPages,
		RetentionDays:    defaultBrowserRetentionDays,
	}
}

// FetchReaderPage reuses the browser source's cookie-free HTTP client, domain
// policy, redirect checks, content-type restrictions, and response bounds for
// deliberate URL capture.
func FetchReaderPage(ctx context.Context, rawURL string, options BrowserOptions) (ReaderPage, error) {
	source := NewBrowserSource(nil)
	source.SetOptions(options)
	content, finalURL, contentType, err := source.fetchReaderContent(ctx, rawURL)
	if err != nil {
		return ReaderPage{}, err
	}
	return ReaderPage{Content: content, FinalURL: finalURL, ContentType: contentType}, nil
}

// SetOptions configures retention and page fetching for a browser source.
func (b *BrowserSource) SetOptions(options BrowserOptions) {
	defaults := DefaultBrowserOptions()
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = defaults.RequestTimeout
	}
	if options.FetchConcurrency <= 0 {
		options.FetchConcurrency = defaults.FetchConcurrency
	}
	if options.MaxPages <= 0 {
		options.MaxPages = defaults.MaxPages
	}
	if options.RetentionDays <= 0 {
		options.RetentionDays = defaults.RetentionDays
	}
	options.AllowedDomains = normalizeDomainList(options.AllowedDomains)
	options.DeniedDomains = normalizeDomainList(options.DeniedDomains)
	b.options = options

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = options.FetchConcurrency
	b.client = &http.Client{
		Transport: transport,
		Timeout:   options.RequestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if !b.urlAllowed(req.URL) {
				return errBrowserDomainBlocked
			}
			return nil
		},
	}
}

func retainBrowserDocuments(docs []*storage.Document, options BrowserOptions, now time.Time) []*storage.Document {
	if len(docs) == 0 {
		return docs
	}
	cutoff := now.AddDate(0, 0, -options.RetentionDays)
	retained := docs[:0]
	for _, doc := range docs {
		if options.RetentionDays > 0 && doc.ModifiedAt.Before(cutoff) {
			continue
		}
		retained = append(retained, doc)
	}
	sort.SliceStable(retained, func(i, j int) bool {
		return retained[i].ModifiedAt.After(retained[j].ModifiedAt)
	})
	if options.MaxPages > 0 && len(retained) > options.MaxPages {
		retained = retained[:options.MaxPages]
	}
	return retained
}

func (b *BrowserSource) enrichBrowserDocuments(ctx context.Context, docs []*storage.Document) error {
	if len(docs) == 0 {
		return nil
	}
	workers := min(b.options.FetchConcurrency, len(docs))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					continue
				}
				b.enrichBrowserDocument(ctx, docs[index])
			}
		}()
	}

sendJobs:
	for index := range docs {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	wg.Wait()
	return ctx.Err()
}

func (b *BrowserSource) enrichBrowserDocument(ctx context.Context, doc *storage.Document) {
	content, finalURL, contentType, err := b.fetchReaderContent(ctx, doc.Metadata["normalized_url"])
	if err != nil {
		doc.Metadata["content_status"] = browserFetchStatus(err)
		return
	}
	if content == "" {
		doc.Metadata["content_status"] = "empty"
		return
	}

	doc.Content = strings.TrimSpace(doc.Title + "\n" + doc.Path + "\n\n" + content)
	doc.Preview = generatePreview(doc.Content, 500)
	hash := sha256.Sum256([]byte(doc.Content))
	doc.ContentHash = fmt.Sprintf("%x", hash[:])
	doc.Metadata["content_status"] = "fetched"
	doc.Metadata["content_type"] = contentType
	doc.Metadata["extraction_method"] = "reader"
	doc.Metadata["fetched_url"] = finalURL
}

func browserFetchStatus(err error) string {
	switch {
	case errors.Is(err, errBrowserDomainBlocked):
		return "blocked"
	case errors.Is(err, errBrowserUnsupportedURL):
		return "unsupported_url"
	case errors.Is(err, errBrowserUnsupportedType):
		return "unsupported_type"
	case errors.Is(err, errBrowserResponseTooLarge):
		return "too_large"
	default:
		return "failed"
	}
}

func (b *BrowserSource) fetchReaderContent(ctx context.Context, rawURL string) (string, string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !b.urlAllowed(parsed) {
		if parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return "", "", "", errBrowserUnsupportedURL
		}
		return "", "", "", errBrowserDomainBlocked
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain;q=0.8")
	req.Header.Set("User-Agent", "MindCLI/reader")

	response, err := b.client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "text/html" && contentType != "application/xhtml+xml" && contentType != "text/plain" {
		return "", "", "", errBrowserUnsupportedType
	}
	if response.ContentLength > b.options.MaxResponseBytes {
		return "", "", "", errBrowserResponseTooLarge
	}

	limited := io.LimitReader(response.Body, b.options.MaxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", "", "", err
	}
	if int64(len(body)) > b.options.MaxResponseBytes {
		return "", "", "", errBrowserResponseTooLarge
	}

	decoded, err := charset.NewReader(bytes.NewReader(body), response.Header.Get("Content-Type"))
	if err != nil {
		return "", "", "", err
	}
	var content string
	if contentType == "text/plain" {
		text, readErr := io.ReadAll(decoded)
		if readErr != nil {
			return "", "", "", readErr
		}
		content = normalizeReaderText(string(text))
	} else {
		content, err = extractReaderText(decoded)
		if err != nil {
			return "", "", "", err
		}
	}
	return content, response.Request.URL.String(), contentType, nil
}

func (b *BrowserSource) urlAllowed(parsed *url.URL) bool {
	if parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	for _, denied := range b.options.DeniedDomains {
		if domainMatches(host, denied) {
			return false
		}
	}
	if len(b.options.AllowedDomains) == 0 {
		return true
	}
	for _, allowed := range b.options.AllowedDomains {
		if domainMatches(host, allowed) {
			return true
		}
	}
	return false
}

func normalizeDomainList(domains []string) []string {
	seen := make(map[string]struct{}, len(domains))
	result := make([]string, 0, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		domain = strings.TrimPrefix(domain, "*.")
		domain = strings.Trim(domain, ".")
		if domain == "" {
			continue
		}
		if _, exists := seen[domain]; exists {
			continue
		}
		seen[domain] = struct{}{}
		result = append(result, domain)
	}
	return result
}

func domainMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func extractReaderText(reader io.Reader) (string, error) {
	root, err := html.Parse(reader)
	if err != nil {
		return "", err
	}

	var candidates []*html.Node
	var body *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "article", "main":
				candidates = append(candidates, node)
			case "body":
				body = node
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	best := ""
	for _, candidate := range candidates {
		if text := readableNodeText(candidate); len(text) > len(best) {
			best = text
		}
	}
	// Prefer explicit article/main content when it is substantial. Fall back
	// to the body for simple pages without semantic reader-mode structure.
	if len(best) < 200 && body != nil {
		best = readableNodeText(body)
	}
	if best == "" {
		best = readableNodeText(root)
	}
	return best, nil
}

func readableNodeText(root *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && excludedReaderElement(node.Data) {
			return
		}
		if node.Type == html.TextNode {
			text := strings.Join(strings.Fields(node.Data), " ")
			if text != "" {
				if builder.Len() > 0 {
					builder.WriteByte(' ')
				}
				builder.WriteString(text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == html.ElementNode && readerBlockElement(node.Data) {
			builder.WriteByte('\n')
		}
	}
	walk(root)
	return normalizeReaderText(builder.String())
}

func excludedReaderElement(name string) bool {
	switch name {
	case "script", "style", "noscript", "template", "svg", "canvas", "nav", "header", "footer", "aside", "form", "button":
		return true
	default:
		return false
	}
}

func readerBlockElement(name string) bool {
	switch name {
	case "p", "div", "section", "article", "main", "li", "blockquote", "pre", "h1", "h2", "h3", "h4", "h5", "h6", "br":
		return true
	default:
		return false
	}
}

func normalizeReaderText(text string) string {
	lines := strings.Split(text, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.FieldsFunc(line, unicode.IsSpace), " ")
		if line != "" {
			normalized = append(normalized, line)
		}
	}
	return strings.Join(normalized, "\n")
}
