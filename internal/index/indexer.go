// Package index provides document indexing capabilities.
package index

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jankowtf/mindcli/internal/config"
	"github.com/jankowtf/mindcli/internal/embeddings"
	"github.com/jankowtf/mindcli/internal/index/sources"
	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
	"github.com/jankowtf/mindcli/pkg/chunker"
)

// Indexer orchestrates document indexing from various sources.
type Indexer struct {
	db       *storage.DB
	search   *search.BleveIndex
	vectors  *storage.VectorStore
	embedder embeddings.Embedder
	sources  []sources.Source
	workers  int
	progress ProgressReporter
}

// ProgressReporter receives progress updates during indexing.
type ProgressReporter interface {
	OnStart(source string, total int)
	OnProgress(source string, current int, total int, path string)
	OnComplete(source string, indexed int, errors int)
	OnError(source string, path string, err error)
}

// Stats contains indexing statistics.
type Stats struct {
	TotalFiles   int64
	IndexedFiles int64
	Errors       int64
	BySource     map[string]int64
}

// NewIndexer creates a new indexer with the given configuration.
// The vectors and embedder parameters are optional; if nil, semantic indexing is skipped.
func NewIndexer(db *storage.DB, searchIndex *search.BleveIndex, vectors *storage.VectorStore, embedder embeddings.Embedder, cfg *config.Config) *Indexer {
	var srcs []sources.Source

	// Add markdown source if enabled
	if cfg.Sources.Markdown.Enabled {
		srcs = append(srcs, sources.NewMarkdownSource(
			cfg.Sources.Markdown.Paths,
			cfg.Sources.Markdown.Extensions,
			cfg.Sources.Markdown.Ignore,
		))
	}

	return &Indexer{
		db:       db,
		search:   searchIndex,
		vectors:  vectors,
		embedder: embedder,
		sources:  srcs,
		workers:  cfg.Indexing.Workers,
	}
}

// SetProgressReporter sets the progress reporter.
func (idx *Indexer) SetProgressReporter(pr ProgressReporter) {
	idx.progress = pr
}

// IndexAll indexes all documents from all configured sources.
func (idx *Indexer) IndexAll(ctx context.Context) (*Stats, error) {
	stats := &Stats{
		BySource: make(map[string]int64),
	}

	for _, src := range idx.sources {
		srcStats, err := idx.indexSource(ctx, src)
		if err != nil {
			return stats, fmt.Errorf("indexing %s: %w", src.Name(), err)
		}

		stats.TotalFiles += srcStats.TotalFiles
		stats.IndexedFiles += srcStats.IndexedFiles
		stats.Errors += srcStats.Errors
		stats.BySource[string(src.Name())] = srcStats.IndexedFiles
	}

	return stats, nil
}

// indexSource indexes all documents from a single source.
func (idx *Indexer) indexSource(ctx context.Context, src sources.Source) (*Stats, error) {
	stats := &Stats{
		BySource: make(map[string]int64),
	}

	// Create channels
	files, scanErrs := src.Scan(ctx)

	// Collect all files first to get total count
	var allFiles []sources.FileInfo
	for f := range files {
		allFiles = append(allFiles, f)
	}

	// Drain scan errors
	for err := range scanErrs {
		if idx.progress != nil {
			idx.progress.OnError(string(src.Name()), "", err)
		}
		atomic.AddInt64(&stats.Errors, 1)
	}

	stats.TotalFiles = int64(len(allFiles))

	if idx.progress != nil {
		idx.progress.OnStart(string(src.Name()), len(allFiles))
	}

	// Create worker pool
	jobs := make(chan sources.FileInfo, idx.workers*2)
	var wg sync.WaitGroup

	var processed int64
	var indexed int64
	var errors int64

	// Start workers
	for i := 0; i < idx.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				current := atomic.AddInt64(&processed, 1)
				if idx.progress != nil {
					idx.progress.OnProgress(string(src.Name()), int(current), len(allFiles), file.Path)
				}

				// Check if file needs indexing (compare hash)
				existing, err := idx.db.GetDocumentByPath(ctx, file.Path)
				if err == nil && existing != nil {
					// File exists, check if modified
					if existing.ModifiedAt.Unix() >= file.ModifiedAt {
						// Not modified, skip
						atomic.AddInt64(&indexed, 1)
						continue
					}
				}

				// Parse document
				doc, err := src.Parse(ctx, file)
				if err != nil {
					if idx.progress != nil {
						idx.progress.OnError(string(src.Name()), file.Path, err)
					}
					atomic.AddInt64(&errors, 1)
					continue
				}

				// Store in database
				if err := idx.db.UpsertDocument(ctx, doc); err != nil {
					if idx.progress != nil {
						idx.progress.OnError(string(src.Name()), file.Path, err)
					}
					atomic.AddInt64(&errors, 1)
					continue
				}

				// Index in search
				if err := idx.search.Index(ctx, doc); err != nil {
					if idx.progress != nil {
						idx.progress.OnError(string(src.Name()), file.Path, err)
					}
					atomic.AddInt64(&errors, 1)
					continue
				}

				// Generate embeddings if available
				if idx.vectors != nil && idx.embedder != nil {
					idx.embedDocument(ctx, doc)
				}

				atomic.AddInt64(&indexed, 1)
			}
		}()
	}

	// Send jobs
	for _, file := range allFiles {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return stats, ctx.Err()
		case jobs <- file:
		}
	}
	close(jobs)
	wg.Wait()

	stats.IndexedFiles = indexed
	stats.Errors = errors

	if idx.progress != nil {
		idx.progress.OnComplete(string(src.Name()), int(indexed), int(errors))
	}

	return stats, nil
}

// IndexFile indexes a single file.
func (idx *Indexer) IndexFile(ctx context.Context, path string) error {
	// Find the appropriate source
	for _, src := range idx.sources {
		files, _ := src.Scan(ctx)
		for file := range files {
			if file.Path == path {
				doc, err := src.Parse(ctx, file)
				if err != nil {
					return fmt.Errorf("parsing: %w", err)
				}

				if err := idx.db.UpsertDocument(ctx, doc); err != nil {
					return fmt.Errorf("storing: %w", err)
				}

				if err := idx.search.Index(ctx, doc); err != nil {
					return fmt.Errorf("indexing: %w", err)
				}

				if idx.vectors != nil && idx.embedder != nil {
					idx.embedDocument(ctx, doc)
				}

				return nil
			}
		}
	}

	return fmt.Errorf("no source found for file: %s", path)
}

// RemoveFile removes a file from the index.
func (idx *Indexer) RemoveFile(ctx context.Context, path string) error {
	// Get document by path
	doc, err := idx.db.GetDocumentByPath(ctx, path)
	if err != nil {
		return err
	}

	// Remove from search index
	if err := idx.search.Delete(ctx, doc.ID); err != nil {
		return fmt.Errorf("removing from search: %w", err)
	}

	// Remove from database
	if err := idx.db.DeleteDocument(ctx, doc.ID); err != nil {
		return fmt.Errorf("removing from database: %w", err)
	}

	return nil
}

// embedDocument chunks a document, generates embeddings, and stores them.
func (idx *Indexer) embedDocument(ctx context.Context, doc *storage.Document) {
	// Delete old chunks and vectors for this document.
	idx.db.DeleteChunksByDocument(ctx, doc.ID)

	// Chunk the document content.
	chunks := chunker.Split(doc.Content, chunker.DefaultOptions())
	if len(chunks) == 0 {
		return
	}

	// Collect chunk texts and keys.
	texts := make([]string, len(chunks))
	keys := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
		keys[i] = fmt.Sprintf("%s:%d", doc.ID, i)
	}

	// Generate embeddings in batch.
	embeds, err := idx.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		if idx.progress != nil {
			idx.progress.OnError(string(doc.Source), doc.Path,
				fmt.Errorf("generating embeddings: %w", err))
		}
		return
	}

	// Store chunks in SQLite and vectors in HNSW.
	for i, c := range chunks {
		chunk := &storage.Chunk{
			ID:         keys[i],
			DocumentID: doc.ID,
			Content:    c.Content,
			StartPos:   c.StartPos,
			EndPos:     c.EndPos,
		}
		idx.db.InsertChunk(ctx, chunk)
	}

	idx.vectors.AddBatch(keys, embeds)
}

// SaveVectors persists the vector store to disk. Call after indexing completes.
func (idx *Indexer) SaveVectors() error {
	if idx.vectors != nil {
		return idx.vectors.Save()
	}
	return nil
}

// NoopProgressReporter is a no-op progress reporter.
type NoopProgressReporter struct{}

func (n *NoopProgressReporter) OnStart(source string, total int)                       {}
func (n *NoopProgressReporter) OnProgress(source string, current, total int, path string) {}
func (n *NoopProgressReporter) OnComplete(source string, indexed, errors int)          {}
func (n *NoopProgressReporter) OnError(source string, path string, err error)          {}
