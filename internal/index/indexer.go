// Package index provides document indexing capabilities.
package index

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/J-1000/mindcli/internal/config"
	"github.com/J-1000/mindcli/internal/embeddings"
	"github.com/J-1000/mindcli/internal/index/sources"
	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/search"
	"github.com/J-1000/mindcli/internal/storage"
	"github.com/J-1000/mindcli/pkg/chunker"
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
	force    bool // when true, re-index even unchanged files (and re-embed)

	redactor      privacy.Redactor
	redactContent bool
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

	// Add PDF source if enabled
	if cfg.Sources.PDF.Enabled {
		srcs = append(srcs, sources.NewPDFSource(
			cfg.Sources.PDF.Paths,
			[]string{".git", "node_modules"},
		))
	}

	// Add email source if enabled
	if cfg.Sources.Email.Enabled {
		emailSrc := sources.NewEmailSource(
			cfg.Sources.Email.Paths,
			cfg.Sources.Email.Formats,
		)
		emailSrc.SetIgnore(cfg.Sources.Email.Ignore)
		emailSrc.SetMaskSensitivePreview(cfg.Sources.Email.MaskSensitivePreview)
		srcs = append(srcs, emailSrc)
	}

	// Add browser history source if enabled
	if cfg.Sources.Browser.Enabled {
		srcs = append(srcs, sources.NewBrowserSource(
			cfg.Sources.Browser.Browsers,
		))
	}

	// Add clipboard source if enabled
	if cfg.Sources.Clipboard.Enabled {
		srcs = append(srcs, sources.NewClipboardSource(
			db,
			cfg.Sources.Clipboard.RetentionDays,
			cfg.Sources.Clipboard.SkipPasswords,
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

// SetForce controls whether unchanged files are re-indexed (and re-embedded).
// Use this for a full rebuild, e.g. after changing the embedding model.
func (idx *Indexer) SetForce(force bool) {
	idx.force = force
}

// SetRedactor configures index-time redaction. When redactContent is true and
// the redactor has patterns, document content and previews are redacted before
// they are stored or indexed.
func (idx *Indexer) SetRedactor(r privacy.Redactor, redactContent bool) {
	idx.redactor = r
	idx.redactContent = redactContent
}

// applyRedaction redacts a document's content and preview in place when
// index-time redaction is enabled.
func (idx *Indexer) applyRedaction(doc *storage.Document) {
	if !idx.redactContent || !idx.redactor.Enabled() {
		return
	}
	doc.Content = idx.redactor.Redact(doc.Content)
	doc.Preview = idx.redactor.Redact(doc.Preview)
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

				// Fast path: skip files whose mtime hasn't advanced.
				existing, _ := idx.db.GetDocumentByPath(ctx, file.Path)
				if !idx.force && existing != nil && existing.ModifiedAt.Unix() >= file.ModifiedAt {
					atomic.AddInt64(&indexed, 1)
					continue
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

				idx.applyRedaction(doc)

				// Content-hash check: if the bytes are identical despite a
				// newer mtime, refresh metadata but skip the expensive
				// re-embedding (existing vectors are still valid).
				unchanged := !idx.force && existing != nil && existing.ContentHash == doc.ContentHash

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

				// Generate embeddings if available (skipped when content is
				// unchanged, since existing vectors remain valid).
				if idx.vectors != nil && idx.embedder != nil && !unchanged {
					if err := idx.embedDocument(ctx, doc); err != nil {
						if idx.progress != nil {
							idx.progress.OnError(string(src.Name()), file.Path, err)
						}
						atomic.AddInt64(&errors, 1)
					}
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
	// Find the appropriate source based on source configuration.
	for _, src := range idx.sources {
		if !src.MatchesPath(path) {
			continue
		}

		fileInfo, err := statFileInfo(path)
		if err != nil {
			// Fall back to a source-local scan for non-filesystem paths.
			fileInfo, err = findFileInfoByPath(ctx, src, path)
			if err != nil {
				return fmt.Errorf("resolving file info: %w", err)
			}
		}

		doc, err := src.Parse(ctx, fileInfo)
		if err != nil {
			return fmt.Errorf("parsing: %w", err)
		}
		idx.applyRedaction(doc)

		if err := idx.db.UpsertDocument(ctx, doc); err != nil {
			return fmt.Errorf("storing: %w", err)
		}

		if err := idx.search.Index(ctx, doc); err != nil {
			return fmt.Errorf("indexing: %w", err)
		}

		if idx.vectors != nil && idx.embedder != nil {
			if err := idx.embedDocument(ctx, doc); err != nil {
				return fmt.Errorf("embedding: %w", err)
			}
		}

		return nil
	}

	return fmt.Errorf("no source found for file: %s", path)
}

func statFileInfo(path string) (sources.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return sources.FileInfo{}, err
	}
	if info.IsDir() {
		return sources.FileInfo{}, fmt.Errorf("path is a directory: %s", path)
	}

	return sources.FileInfo{
		Path:       path,
		ModifiedAt: info.ModTime().Unix(),
		Size:       info.Size(),
	}, nil
}

func findFileInfoByPath(ctx context.Context, src sources.Source, path string) (sources.FileInfo, error) {
	files, errs := src.Scan(ctx)
	for file := range files {
		if file.Path == path {
			for range errs {
			}
			return file, nil
		}
	}

	var scanErr error
	for err := range errs {
		if scanErr == nil {
			scanErr = err
		}
	}
	if scanErr != nil {
		return sources.FileInfo{}, scanErr
	}
	return sources.FileInfo{}, fmt.Errorf("file not found in source scan: %s", path)
}

// RemoveFile removes a file from the index.
func (idx *Indexer) RemoveFile(ctx context.Context, path string) error {
	// Get document by path
	doc, err := idx.db.GetDocumentByPath(ctx, path)
	if err != nil {
		return err
	}

	// Remove semantic vectors for this document's chunks.
	if err := idx.deleteDocumentVectors(ctx, doc.ID); err != nil && idx.progress != nil {
		idx.progress.OnError(string(doc.Source), doc.Path, fmt.Errorf("removing vectors: %w", err))
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
// Errors are returned so callers can surface and count them rather than
// silently leaving a document without vectors.
func (idx *Indexer) embedDocument(ctx context.Context, doc *storage.Document) error {
	// Delete old chunks and vectors for this document.
	if err := idx.deleteDocumentVectors(ctx, doc.ID); err != nil {
		return fmt.Errorf("removing old vectors: %w", err)
	}
	if err := idx.db.DeleteChunksByDocument(ctx, doc.ID); err != nil {
		return fmt.Errorf("removing old chunks: %w", err)
	}

	// Chunk the document content.
	chunks := chunker.Split(doc.Content, chunker.DefaultOptions())
	if len(chunks) == 0 {
		return nil
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
		return fmt.Errorf("generating embeddings: %w", err)
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
		if err := idx.db.InsertChunk(ctx, chunk); err != nil {
			return fmt.Errorf("inserting chunk: %w", err)
		}
	}

	if err := idx.vectors.AddBatch(keys, embeds); err != nil {
		return fmt.Errorf("adding vectors: %w", err)
	}
	return nil
}

// Prune removes indexed documents whose backing file no longer exists. Only
// filesystem-backed sources (markdown, pdf, email) are considered; browser and
// clipboard entries are not file-backed and are left untouched. Callers should
// SaveVectors afterwards to persist vector removals.
func (idx *Indexer) Prune(ctx context.Context) (int, error) {
	docs, err := idx.db.ListDocuments(ctx, "")
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, doc := range docs {
		if !isFileBackedSource(doc.Source) {
			continue
		}
		if _, err := os.Stat(doc.Path); !os.IsNotExist(err) {
			continue
		}
		if err := idx.RemoveFile(ctx, doc.Path); err != nil {
			if idx.progress != nil {
				idx.progress.OnError(string(doc.Source), doc.Path, err)
			}
			continue
		}
		removed++
	}
	return removed, nil
}

func isFileBackedSource(s storage.Source) bool {
	switch s {
	case storage.SourceMarkdown, storage.SourcePDF, storage.SourceEmail:
		return true
	default:
		return false
	}
}

func (idx *Indexer) deleteDocumentVectors(ctx context.Context, docID string) error {
	if idx.vectors == nil {
		return nil
	}

	chunks, err := idx.db.GetChunksByDocument(ctx, docID)
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		idx.vectors.Delete(chunk.ID)
	}
	return nil
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

func (n *NoopProgressReporter) OnStart(source string, total int)                          {}
func (n *NoopProgressReporter) OnProgress(source string, current, total int, path string) {}
func (n *NoopProgressReporter) OnComplete(source string, indexed, errors int)             {}
func (n *NoopProgressReporter) OnError(source string, path string, err error)             {}
