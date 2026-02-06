package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/jankowtf/mindcli/internal/storage"
)

// ClipboardSource indexes clipboard history.
// It polls the system clipboard and stores unique text entries.
type ClipboardSource struct {
	retentionDays int
	skipPasswords bool
	db            *storage.DB
}

// NewClipboardSource creates a new clipboard source.
func NewClipboardSource(db *storage.DB, retentionDays int, skipPasswords bool) *ClipboardSource {
	if retentionDays <= 0 {
		retentionDays = 30
	}
	return &ClipboardSource{
		retentionDays: retentionDays,
		skipPasswords: skipPasswords,
		db:            db,
	}
}

// Name returns the source name.
func (c *ClipboardSource) Name() storage.Source {
	return storage.SourceClipboard
}

// Scan returns the current clipboard content as a single file-like entry.
// For clipboard, each unique clip is treated as a "file".
func (c *ClipboardSource) Scan(ctx context.Context) (<-chan FileInfo, <-chan error) {
	files := make(chan FileInfo, 1)
	errs := make(chan error, 1)

	go func() {
		defer close(files)
		defer close(errs)

		text, err := clipboard.ReadAll()
		if err != nil {
			select {
			case errs <- fmt.Errorf("reading clipboard: %w", err):
			case <-ctx.Done():
			}
			return
		}

		text = strings.TrimSpace(text)
		if text == "" {
			return
		}

		// Skip likely passwords.
		if c.skipPasswords && looksLikePassword(text) {
			return
		}

		// Use content hash as the "path" for deduplication.
		hash := sha256.Sum256([]byte(text))
		id := hex.EncodeToString(hash[:8])

		select {
		case files <- FileInfo{
			Path:       "clipboard:" + id,
			ModifiedAt: time.Now().Unix(),
			Size:       int64(len(text)),
		}:
		case <-ctx.Done():
		}
	}()

	return files, errs
}

// Parse creates a document from the current clipboard content.
func (c *ClipboardSource) Parse(ctx context.Context, file FileInfo) (*storage.Document, error) {
	text, err := clipboard.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading clipboard: %w", err)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("clipboard is empty")
	}

	hash := sha256.Sum256([]byte(text))
	id := hex.EncodeToString(hash[:8])

	// Title is first line or truncated content.
	title := firstLine(text)
	if len(title) > 100 {
		title = title[:97] + "..."
	}

	return &storage.Document{
		ID:          id,
		Source:      storage.SourceClipboard,
		Path:        "clipboard:" + id,
		Title:       title,
		Content:     text,
		Preview:     generatePreview(text, 500),
		ContentHash: hex.EncodeToString(hash[:]),
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Now(),
	}, nil
}

// looksLikePassword uses simple heuristics to detect likely passwords.
func looksLikePassword(text string) bool {
	// Single line, no spaces, and has mixed character classes.
	if strings.Contains(text, "\n") || strings.Contains(text, " ") {
		return false
	}

	if len(text) < 8 || len(text) > 128 {
		return false
	}

	hasUpper, hasLower, hasDigit, hasSpecial := false, false, false, false
	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}

	// If it has 3+ character classes, it's likely a password.
	classes := 0
	if hasUpper {
		classes++
	}
	if hasLower {
		classes++
	}
	if hasDigit {
		classes++
	}
	if hasSpecial {
		classes++
	}
	return classes >= 3
}

// firstLine returns the first line of text.
func firstLine(text string) string {
	if idx := strings.Index(text, "\n"); idx != -1 {
		return strings.TrimSpace(text[:idx])
	}
	return text
}
