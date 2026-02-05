// Package sources defines interfaces and implementations for document sources.
package sources

import (
	"context"

	"github.com/jankowtf/mindcli/internal/storage"
)

// Source represents a document source that can be indexed.
type Source interface {
	// Name returns the source name (e.g., "markdown", "pdf").
	Name() storage.Source

	// Scan walks the configured paths and returns files to index.
	Scan(ctx context.Context) (<-chan FileInfo, <-chan error)

	// Parse reads a file and returns the parsed document.
	Parse(ctx context.Context, file FileInfo) (*storage.Document, error)
}

// FileInfo contains information about a file to be indexed.
type FileInfo struct {
	Path       string
	ModifiedAt int64 // Unix timestamp
	Size       int64
}
