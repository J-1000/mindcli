// Package capture writes portable Markdown captures into a configured inbox.
package capture

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const MaxCaptureBytes = 5 << 20

var (
	frontmatterKeyPattern = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_-]+)\s*:`)
	validTagPattern       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
)

type Request struct {
	Content    string
	Title      string
	Tags       []string
	Collection string
	SourceURL  string
}

type Result struct {
	Path      string
	Title     string
	Duplicate bool
}

type Writer struct {
	Inbox string
	Now   func() time.Time
}

// Write creates one private Markdown file atomically. URL captures use a
// stable normalized-URL hash and return the existing file on duplicates.
func (w Writer) Write(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(w.Inbox) == "" {
		return Result{}, fmt.Errorf("capture inbox is not configured")
	}
	if strings.TrimSpace(request.Content) == "" {
		return Result{}, fmt.Errorf("capture content must not be empty")
	}
	if len(request.Content) > MaxCaptureBytes {
		return Result{}, fmt.Errorf("capture content exceeds %d bytes", MaxCaptureBytes)
	}
	request.SourceURL = strings.TrimSpace(request.SourceURL)
	request.Collection = strings.TrimSpace(request.Collection)
	tags, err := normalizeTags(request.Tags)
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = deriveTitle(request.Content)
	}
	if title == "" {
		title = "Capture"
	}
	content := composeMarkdown(request.Content, title, tags, request.Collection, request.SourceURL, now)
	if len(content) > MaxCaptureBytes {
		return Result{}, fmt.Errorf("capture with metadata exceeds %d bytes", MaxCaptureBytes)
	}

	if err := os.MkdirAll(w.Inbox, 0o700); err != nil {
		return Result{}, fmt.Errorf("creating capture inbox: %w", err)
	}
	if err := os.Chmod(w.Inbox, 0o700); err != nil {
		return Result{}, fmt.Errorf("protecting capture inbox: %w", err)
	}

	base := captureFilename(title, request.SourceURL, now)
	for suffix := 1; ; suffix++ {
		filename := base
		if request.SourceURL == "" && suffix > 1 {
			filename = strings.TrimSuffix(base, ".md") + "-" + strconv.Itoa(suffix) + ".md"
		}
		target := filepath.Join(w.Inbox, filename)
		duplicate, err := writeFileAtomicExclusive(ctx, target, []byte(content))
		if err != nil {
			return Result{}, err
		}
		if duplicate {
			if request.SourceURL != "" {
				return Result{Path: target, Title: title, Duplicate: true}, nil
			}
			continue
		}
		return Result{Path: target, Title: title}, nil
	}
}

func writeFileAtomicExclusive(ctx context.Context, target string, content []byte) (bool, error) {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".mindcli-capture-*.tmp")
	if err != nil {
		return false, fmt.Errorf("creating temporary capture: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()

	writeErr := func() error {
		if err := temporary.Chmod(0o600); err != nil {
			return err
		}
		if _, err := temporary.Write(content); err != nil {
			return err
		}
		if err := temporary.Sync(); err != nil {
			return err
		}
		return temporary.Close()
	}()
	if writeErr != nil {
		_ = temporary.Close()
		return false, fmt.Errorf("writing capture: %w", writeErr)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := os.Link(temporaryPath, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return true, nil
		}
		return false, fmt.Errorf("publishing capture: %w", err)
	}
	return false, nil
}

func captureFilename(title, sourceURL string, now time.Time) string {
	slug := safeSlug(title)
	if slug == "" {
		slug = "capture"
	}
	if sourceURL != "" {
		hash := sha256.Sum256([]byte(sourceURL))
		return fmt.Sprintf("saved-%x.md", hash[:10])
	}
	return now.Format("20060102-150405") + "-" + slug + ".md"
}

func safeSlug(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
		} else if !lastDash && builder.Len() > 0 && (unicode.IsSpace(r) || strings.ContainsRune("-_.:/", r)) {
			builder.WriteByte('-')
			lastDash = true
		}
		if builder.Len() >= 64 {
			break
		}
	}
	return strings.Trim(builder.String(), "-")
}

func composeMarkdown(content, title string, tags []string, collection, sourceURL string, now time.Time) string {
	header, body, lineEnding, hasFrontmatter := splitFrontmatter(content)
	if lineEnding == "" {
		lineEnding = "\n"
	}
	metadata := make([]string, 0, 5)
	keys := frontmatterKeys(header)
	appendMissing := func(key, value string) {
		if value != "" && !keys[strings.ToLower(key)] {
			metadata = append(metadata, key+": "+value)
		}
	}
	appendMissing("title", yamlString(title))
	if len(tags) > 0 {
		quoted := make([]string, len(tags))
		for index, tag := range tags {
			quoted[index] = yamlString(tag)
		}
		appendMissing("tags", "["+strings.Join(quoted, ", ")+"]")
	}
	appendMissing("collection", yamlString(collection))
	appendMissing("source_url", yamlString(sourceURL))
	appendMissing("captured_at", yamlString(now.Format(time.RFC3339)))

	if hasFrontmatter {
		if len(metadata) > 0 {
			if header != "" && !strings.HasSuffix(header, lineEnding) {
				header += lineEnding
			}
			header += strings.Join(metadata, lineEnding) + lineEnding
		}
		if len(tags) > 0 && keys["tags"] {
			body = appendInlineTags(body, tags, lineEnding)
		}
		if header != "" && !strings.HasSuffix(header, lineEnding) {
			header += lineEnding
		}
		return "---" + lineEnding + header + "---" + lineEnding + ensureTrailingLineEnding(body, lineEnding)
	}

	header = strings.Join(metadata, lineEnding)
	return "---" + lineEnding + header + lineEnding + "---" + lineEnding + ensureTrailingLineEnding(content, lineEnding)
}

func splitFrontmatter(content string) (header, body, lineEnding string, ok bool) {
	switch {
	case strings.HasPrefix(content, "---\r\n"):
		lineEnding = "\r\n"
	case strings.HasPrefix(content, "---\n"):
		lineEnding = "\n"
	default:
		return "", content, "\n", false
	}
	start := len("---" + lineEnding)
	closing := lineEnding + "---" + lineEnding
	index := strings.Index(content[start:], closing)
	if index < 0 {
		closing = lineEnding + "---"
		if !strings.HasSuffix(content, closing) {
			return "", content, lineEnding, false
		}
		index = len(content) - len(closing)
		return content[start:index], "", lineEnding, true
	}
	index += start
	return content[start:index], content[index+len(closing):], lineEnding, true
}

func frontmatterKeys(header string) map[string]bool {
	keys := make(map[string]bool)
	for _, match := range frontmatterKeyPattern.FindAllStringSubmatch(header, -1) {
		keys[strings.ToLower(match[1])] = true
	}
	return keys
}

func appendInlineTags(body string, tags []string, lineEnding string) string {
	trimmed := strings.TrimRight(body, "\r\n")
	values := make([]string, len(tags))
	for index, tag := range tags {
		values[index] = "#" + tag
	}
	if trimmed != "" {
		trimmed += lineEnding + lineEnding
	}
	return trimmed + strings.Join(values, " ") + lineEnding
}

func ensureTrailingLineEnding(value, lineEnding string) string {
	value = strings.TrimRight(value, "\r\n")
	return value + lineEnding
}

func yamlString(value string) string {
	if value == "" {
		return ""
	}
	return strconv.Quote(value)
}

func normalizeTags(tags []string) ([]string, error) {
	seen := make(map[string]bool)
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		for _, value := range strings.Split(tag, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if !validTagPattern.MatchString(value) {
				return nil, fmt.Errorf("invalid tag %q: use letters, numbers, underscores, or hyphens", value)
			}
			key := strings.ToLower(value)
			if !seen[key] {
				seen[key] = true
				result = append(result, key)
			}
		}
	}
	return result, nil
}

func deriveTitle(content string) string {
	_, body, _, hasFrontmatter := splitFrontmatter(content)
	if !hasFrontmatter {
		body = content
	}
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 80 {
			line = string(runes[:80])
		}
		return line
	}
	return ""
}
