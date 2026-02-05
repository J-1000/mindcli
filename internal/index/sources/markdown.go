package sources

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
)

var (
	// Frontmatter regex (YAML between --- markers)
	frontmatterRegex = regexp.MustCompile(`(?s)^---\n(.+?)\n---\n?`)

	// Heading regex (# through ######)
	headingRegex = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)

	// Tag regex (#tag or #multi-word-tag)
	tagRegex = regexp.MustCompile(`(?:^|\s)#([a-zA-Z][a-zA-Z0-9_-]*)`)

	// Wiki-style link regex [[link]]
	wikiLinkRegex = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

	// Markdown link regex [text](url)
	mdLinkRegex = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// MarkdownSource indexes markdown files.
type MarkdownSource struct {
	scanner *Scanner
}

// NewMarkdownSource creates a new markdown source.
func NewMarkdownSource(paths, extensions, ignore []string) *MarkdownSource {
	return &MarkdownSource{
		scanner: NewScanner(ScanConfig{
			Paths:      paths,
			Extensions: extensions,
			Ignore:     ignore,
		}),
	}
}

// Name returns the source name.
func (m *MarkdownSource) Name() storage.Source {
	return storage.SourceMarkdown
}

// Scan walks configured paths and returns markdown files.
func (m *MarkdownSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	return m.scanner.Scan(ctx)
}

// Parse reads and parses a markdown file into a Document.
func (m *MarkdownSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	content, err := os.ReadFile(file.Path)
	if err != nil {
		return nil, err
	}

	text := string(content)

	// Calculate content hash
	hash := sha256.Sum256(content)
	contentHash := hex.EncodeToString(hash[:])

	// Parse the document
	parsed := parseMarkdown(text)

	// Determine title
	title := parsed.Title
	if title == "" {
		// Use filename without extension as title
		title = strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path))
	}

	// Create preview (first ~500 chars of body content)
	preview := createPreview(parsed.Body, 500)

	// Build metadata
	metadata := make(map[string]string)

	if len(parsed.Tags) > 0 {
		metadata["tags"] = strings.Join(parsed.Tags, ",")
	}
	if len(parsed.Links) > 0 {
		metadata["links"] = strings.Join(parsed.Links, ",")
	}
	if len(parsed.Headings) > 0 {
		metadata["headings"] = strings.Join(parsed.Headings, ",")
	}

	// Include frontmatter fields
	for k, v := range parsed.Frontmatter {
		metadata["fm_"+k] = v
	}

	// Generate ID from path (stable across re-indexing)
	pathHash := sha256.Sum256([]byte(file.Path))
	id := hex.EncodeToString(pathHash[:16])

	return &storage.Document{
		ID:          id,
		Source:      storage.SourceMarkdown,
		Path:        file.Path,
		Title:       title,
		Content:     parsed.Body,
		Preview:     preview,
		Metadata:    metadata,
		ContentHash: contentHash,
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Unix(file.ModifiedAt, 0),
	}, nil
}

// ParsedMarkdown contains parsed markdown content.
type ParsedMarkdown struct {
	Title       string
	Body        string
	Frontmatter map[string]string
	Headings    []string
	Tags        []string
	Links       []string
}

// parseMarkdown extracts structured data from markdown content.
func parseMarkdown(content string) ParsedMarkdown {
	result := ParsedMarkdown{
		Frontmatter: make(map[string]string),
	}

	body := content

	// Extract frontmatter
	if match := frontmatterRegex.FindStringSubmatch(content); len(match) > 1 {
		result.Frontmatter = parseFrontmatter(match[1])
		body = content[len(match[0]):]

		// Get title from frontmatter
		if title, ok := result.Frontmatter["title"]; ok {
			result.Title = title
		}
	}

	// Extract headings
	headingMatches := headingRegex.FindAllStringSubmatch(body, -1)
	for _, match := range headingMatches {
		if len(match) > 2 {
			heading := strings.TrimSpace(match[2])
			result.Headings = append(result.Headings, heading)

			// Use first H1 as title if not set
			if result.Title == "" && match[1] == "#" {
				result.Title = heading
			}
		}
	}

	// Extract tags
	tagMatches := tagRegex.FindAllStringSubmatch(body, -1)
	tagSet := make(map[string]bool)
	for _, match := range tagMatches {
		if len(match) > 1 {
			tag := strings.ToLower(match[1])
			if !tagSet[tag] {
				tagSet[tag] = true
				result.Tags = append(result.Tags, tag)
			}
		}
	}

	// Extract wiki-style links
	wikiMatches := wikiLinkRegex.FindAllStringSubmatch(body, -1)
	for _, match := range wikiMatches {
		if len(match) > 1 {
			result.Links = append(result.Links, match[1])
		}
	}

	// Extract markdown links
	mdMatches := mdLinkRegex.FindAllStringSubmatch(body, -1)
	for _, match := range mdMatches {
		if len(match) > 2 {
			result.Links = append(result.Links, match[2])
		}
	}

	result.Body = body
	return result
}

// parseFrontmatter extracts key-value pairs from YAML frontmatter.
func parseFrontmatter(content string) map[string]string {
	result := make(map[string]string)

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()

		// Simple key: value parsing (doesn't handle nested YAML)
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])

			// Remove quotes
			value = strings.Trim(value, `"'`)

			// Handle simple arrays [a, b, c]
			if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
				value = value[1 : len(value)-1]
			}

			if key != "" && value != "" {
				result[key] = value
			}
		}
	}

	return result
}

// createPreview creates a preview from content.
func createPreview(content string, maxLen int) string {
	// Remove markdown formatting for cleaner preview
	preview := content

	// Remove code blocks
	preview = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(preview, "")

	// Remove inline code
	preview = regexp.MustCompile("`[^`]+`").ReplaceAllString(preview, "")

	// Remove links but keep text
	preview = mdLinkRegex.ReplaceAllString(preview, "$1")
	preview = wikiLinkRegex.ReplaceAllString(preview, "$1")

	// Remove images
	preview = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`).ReplaceAllString(preview, "")

	// Remove headers markers
	preview = headingRegex.ReplaceAllString(preview, "$2")

	// Remove bold/italic
	preview = regexp.MustCompile(`\*\*([^*]+)\*\*`).ReplaceAllString(preview, "$1")
	preview = regexp.MustCompile(`\*([^*]+)\*`).ReplaceAllString(preview, "$1")
	preview = regexp.MustCompile(`__([^_]+)__`).ReplaceAllString(preview, "$1")
	preview = regexp.MustCompile(`_([^_]+)_`).ReplaceAllString(preview, "$1")

	// Collapse whitespace
	preview = regexp.MustCompile(`\s+`).ReplaceAllString(preview, " ")
	preview = strings.TrimSpace(preview)

	if len(preview) > maxLen {
		// Find a good break point
		preview = preview[:maxLen]
		if lastSpace := strings.LastIndex(preview, " "); lastSpace > maxLen*3/4 {
			preview = preview[:lastSpace]
		}
		preview += "..."
	}

	return preview
}
