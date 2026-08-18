// Package sources defines interfaces and implementations for document sources.
package sources

import (
	"context"

	"github.com/J-1000/mindcli/internal/storage"
)

// Source represents a document source that can be indexed.
type Source interface {
	// Name returns the source name (e.g., "markdown", "pdf").
	Name() storage.Source

	// Scan walks the configured paths and returns files to index.
	Scan(ctx context.Context) (<-chan FileInfo, <-chan error)

	// MatchesPath reports whether this source is configured to handle the given path.
	MatchesPath(path string) bool

	// Parse reads a file and returns the parsed document.
	Parse(ctx context.Context, file FileInfo) (*storage.Document, error)
}

// MultiDocumentSource is implemented by sources where one scanned artifact can
// contain multiple independently searchable documents. Parse remains part of
// Source so ordinary file-backed sources keep their simple one-file/one-document
// contract; the indexer prefers ParseDocuments when this interface is present.
type MultiDocumentSource interface {
	Source

	// ParseDocuments reads a scanned artifact and returns its documents. Every
	// document must have a stable ID so it can be updated independently.
	ParseDocuments(ctx context.Context, file FileInfo) ([]*storage.Document, error)
}

// ParseDocuments adapts a Source to the multi-document ingestion contract.
func ParseDocuments(ctx context.Context, source Source, file FileInfo) ([]*storage.Document, error) {
	if multi, ok := source.(MultiDocumentSource); ok {
		return multi.ParseDocuments(ctx, file)
	}

	doc, err := source.Parse(ctx, file)
	if err != nil {
		return nil, err
	}
	return []*storage.Document{doc}, nil
}

// FileInfo contains information about a file to be indexed.
type FileInfo struct {
	Path       string
	ModifiedAt int64 // Unix timestamp
	Size       int64
}
