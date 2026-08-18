// Package index provides document indexing capabilities.
package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	// The capture inbox is always a Markdown source so add/save can index a new
	// portable file immediately even when ordinary note indexing is disabled.
	markdownPaths := markdownSourcePaths(cfg)
	if len(markdownPaths) > 0 {
		extensions := append([]string(nil), cfg.Sources.Markdown.Extensions...)
		if !containsExtension(extensions, ".md") {
			extensions = append(extensions, ".md")
		}
		srcs = append(srcs, sources.NewMarkdownSource(
			markdownPaths,
			extensions,
			cfg.Sources.Markdown.Ignore,
		))
	}

	// Add PDF source if enabled
	if cfg.Sources.PDF.Enabled {
		pdfSource := sources.NewPDFSource(
			cfg.Sources.PDF.Paths,
			[]string{".git", "node_modules"},
		)
		pdfSource.SetOCROptions(sources.PDFOCROptions{
			Enabled:      cfg.Sources.PDF.OCREnabled,
			Command:      cfg.Sources.PDF.OCRCommand,
			Renderer:     cfg.Sources.PDF.OCRRenderer,
			Languages:    cfg.Sources.PDF.OCRLanguages,
			MaxPages:     cfg.Sources.PDF.OCRMaxPages,
			Timeout:      time.Duration(cfg.Sources.PDF.OCRTimeoutSeconds) * time.Second,
			MinTextChars: cfg.Sources.PDF.OCRMinTextChars,
		})
		srcs = append(srcs, pdfSource)
	}

	if cfg.Sources.HTML.Enabled {
		srcs = append(srcs, sources.NewHTMLSource(
			cfg.Sources.HTML.Paths,
			cfg.Sources.HTML.Ignore,
			cfg.Sources.HTML.MaxFileBytes,
			cfg.Sources.HTML.MaxDecompressedBytes,
		))
	}

	if cfg.Sources.DOCX.Enabled {
		srcs = append(srcs, sources.NewDOCXSource(
			cfg.Sources.DOCX.Paths,
			cfg.Sources.DOCX.Ignore,
			cfg.Sources.DOCX.MaxFileBytes,
			cfg.Sources.DOCX.MaxDecompressedBytes,
		))
	}

	if cfg.Sources.EPUB.Enabled {
		srcs = append(srcs, sources.NewEPUBSource(
			cfg.Sources.EPUB.Paths,
			cfg.Sources.EPUB.Ignore,
			cfg.Sources.EPUB.MaxFileBytes,
			cfg.Sources.EPUB.MaxDecompressedBytes,
		))
	}

	if cfg.Sources.Org.Enabled {
		srcs = append(srcs, sources.NewOrgSource(
			cfg.Sources.Org.Paths,
			cfg.Sources.Org.Ignore,
			cfg.Sources.Org.MaxFileBytes,
		))
	}

	if cfg.Sources.Code.Enabled {
		srcs = append(srcs, sources.NewCodeSource(
			cfg.Sources.Code.Paths,
			cfg.Sources.Code.Ignore,
			cfg.Sources.Code.MaxFileBytes,
			cfg.Sources.Code.MaxFiles,
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
		emailSrc.SetAttachmentOptions(sources.EmailAttachmentOptions{
			Enabled:              cfg.Sources.Email.ExtractAttachments,
			MaxAttachmentBytes:   cfg.Sources.Email.MaxAttachmentBytes,
			MaxDecompressedBytes: cfg.Sources.Email.MaxDecompressedBytes,
			MaxArchiveDepth:      cfg.Sources.Email.MaxArchiveDepth,
		})
		srcs = append(srcs, emailSrc)
	}

	// Add browser history source if enabled
	if cfg.Sources.Browser.Enabled {
		browserSource := sources.NewBrowserSource(cfg.Sources.Browser.Browsers)
		browserSource.SetOptions(sources.BrowserOptions{
			IncludeContent:   cfg.Sources.Browser.IncludeContent,
			AllowedDomains:   cfg.Sources.Browser.AllowedDomains,
			DeniedDomains:    cfg.Sources.Browser.DeniedDomains,
			MaxResponseBytes: cfg.Sources.Browser.MaxResponseBytes,
			RequestTimeout:   time.Duration(cfg.Sources.Browser.RequestTimeoutSeconds) * time.Second,
			FetchConcurrency: cfg.Sources.Browser.FetchConcurrency,
			MaxPages:         cfg.Sources.Browser.MaxPages,
			RetentionDays:    cfg.Sources.Browser.RetentionDays,
		})
		srcs = append(srcs, browserSource)
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

func markdownSourcePaths(cfg *config.Config) []string {
	var paths []string
	if cfg.Sources.Markdown.Enabled {
		paths = append(paths, cfg.Sources.Markdown.Paths...)
	}
	inbox := strings.TrimSpace(cfg.Capture.Inbox)
	if inbox == "" {
		return paths
	}
	for _, path := range paths {
		if pathContains(path, inbox) {
			return paths
		}
	}
	return append(paths, inbox)
}

func pathContains(base, target string) bool {
	base, baseErr := filepath.Abs(filepath.Clean(base))
	target, targetErr := filepath.Abs(filepath.Clean(target))
	if baseErr != nil || targetErr != nil {
		return false
	}
	relative, err := filepath.Rel(base, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func containsExtension(extensions []string, target string) bool {
	for _, extension := range extensions {
		if !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		if strings.EqualFold(extension, target) {
			return true
		}
	}
	return false
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
	_, isMultiDocumentSource := src.(sources.MultiDocumentSource)
	reconciledSource, isReconciledSource := src.(sources.ReconciledMultiDocumentSource)

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

				// Fast path for ordinary file-backed sources: skip files whose
				// mtime hasn't advanced. Multi-document artifacts must be parsed
				// first so each returned document can be checked independently.
				var existing *storage.Document
				if !isMultiDocumentSource {
					existing, _ = idx.db.GetDocumentByPath(ctx, file.Path)
				}
				if !isMultiDocumentSource && !idx.force && existing != nil && existing.ModifiedAt.Unix() >= file.ModifiedAt {
					if err := idx.refreshStoredTags(ctx, existing); err != nil {
						if idx.progress != nil {
							idx.progress.OnError(string(src.Name()), file.Path, err)
						}
						atomic.AddInt64(&errors, 1)
						continue
					}
					atomic.AddInt64(&indexed, 1)
					continue
				}

				// Parse one or more documents from the scanned artifact.
				docs, err := sources.ParseDocuments(ctx, src, file)
				if err != nil {
					if idx.progress != nil {
						idx.progress.OnError(string(src.Name()), file.Path, err)
					}
					atomic.AddInt64(&errors, 1)
					continue
				}

				currentIDs := make(map[string]struct{}, len(docs))
				reconciliationScope := ""
				if isReconciledSource {
					reconciliationScope = reconciledSource.ReconciliationScope(file)
				}

				for _, doc := range docs {
					if doc == nil || doc.ID == "" {
						err := fmt.Errorf("source returned a document without a stable ID")
						if idx.progress != nil {
							idx.progress.OnError(string(src.Name()), file.Path, err)
						}
						atomic.AddInt64(&errors, 1)
						continue
					}
					currentIDs[doc.ID] = struct{}{}
					if reconciliationScope != "" {
						if doc.Metadata == nil {
							doc.Metadata = make(map[string]string)
						}
						doc.Metadata[sources.IngestionScopeMetadata] = reconciliationScope
					}

					docExisting := existing
					if isMultiDocumentSource {
						docExisting, _ = idx.db.GetDocument(ctx, doc.ID)
					}

					idx.applyRedaction(doc)
					if err := idx.db.AttachStoredTags(ctx, doc); err != nil {
						if idx.progress != nil {
							idx.progress.OnError(string(src.Name()), doc.Path, err)
						}
						atomic.AddInt64(&errors, 1)
						continue
					}

					// Content-hash check: if the bytes are identical despite newer
					// metadata, keep the existing vectors.
					unchanged := !idx.force && docExisting != nil && docExisting.ContentHash == doc.ContentHash

					if err := idx.db.UpsertDocument(ctx, doc); err != nil {
						if idx.progress != nil {
							idx.progress.OnError(string(src.Name()), doc.Path, err)
						}
						atomic.AddInt64(&errors, 1)
						continue
					}

					if err := idx.search.Index(ctx, doc); err != nil {
						if idx.progress != nil {
							idx.progress.OnError(string(src.Name()), doc.Path, err)
						}
						atomic.AddInt64(&errors, 1)
						continue
					}

					if idx.vectors != nil && idx.embedder != nil && !unchanged {
						if err := idx.embedDocument(ctx, doc); err != nil {
							if idx.progress != nil {
								idx.progress.OnError(string(src.Name()), doc.Path, err)
							}
							atomic.AddInt64(&errors, 1)
						}
					}

					atomic.AddInt64(&indexed, 1)
				}

				if reconciliationScope != "" {
					if err := idx.reconcileDocumentSet(ctx, reconciledSource, file, currentIDs); err != nil {
						if idx.progress != nil {
							idx.progress.OnError(string(src.Name()), file.Path, err)
						}
						atomic.AddInt64(&errors, 1)
					}
				}
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

		docs, err := sources.ParseDocuments(ctx, src, fileInfo)
		if err != nil {
			return fmt.Errorf("parsing: %w", err)
		}
		currentIDs := make(map[string]struct{}, len(docs))
		reconciledSource, isReconciledSource := src.(sources.ReconciledMultiDocumentSource)
		reconciliationScope := ""
		if isReconciledSource {
			reconciliationScope = reconciledSource.ReconciliationScope(fileInfo)
		}
		for _, doc := range docs {
			if doc == nil || doc.ID == "" {
				return fmt.Errorf("parsing: source returned a document without a stable ID")
			}
			currentIDs[doc.ID] = struct{}{}
			if reconciliationScope != "" {
				if doc.Metadata == nil {
					doc.Metadata = make(map[string]string)
				}
				doc.Metadata[sources.IngestionScopeMetadata] = reconciliationScope
			}
			idx.applyRedaction(doc)
			if err := idx.db.AttachStoredTags(ctx, doc); err != nil {
				return fmt.Errorf("attaching stored tags: %w", err)
			}

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
		}
		if reconciliationScope != "" {
			if err := idx.reconcileDocumentSet(ctx, reconciledSource, fileInfo, currentIDs); err != nil {
				return err
			}
		}

		return nil
	}

	return fmt.Errorf("no source found for file: %s", path)
}

// refreshStoredTags keeps an unchanged document's metadata and search entry in
// sync with tags stored in the normalized document_tags table. This also
// repairs tag projections created by older MindCLI versions during an ordinary
// incremental index pass.
func (idx *Indexer) refreshStoredTags(ctx context.Context, doc *storage.Document) error {
	previous := ""
	if doc.Metadata != nil {
		previous = doc.Metadata["stored_tags"]
	}
	if err := idx.db.AttachStoredTags(ctx, doc); err != nil {
		return fmt.Errorf("attaching stored tags: %w", err)
	}
	current := ""
	if doc.Metadata != nil {
		current = doc.Metadata["stored_tags"]
	}
	if previous == current {
		return nil
	}
	if err := idx.db.UpdateDocument(ctx, doc); err != nil {
		return fmt.Errorf("storing tag projection: %w", err)
	}
	if err := idx.search.Index(ctx, doc); err != nil {
		return fmt.Errorf("indexing tag projection: %w", err)
	}
	return nil
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
	docs, err := idx.db.ListDocumentsByPath(ctx, path)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return storage.ErrNotFound
	}
	for _, doc := range docs {
		if err := idx.removeDocument(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

func (idx *Indexer) removeDocument(ctx context.Context, doc *storage.Document) error {
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

func (idx *Indexer) reconcileDocumentSet(
	ctx context.Context,
	src sources.ReconciledMultiDocumentSource,
	file sources.FileInfo,
	currentIDs map[string]struct{},
) error {
	docs, err := idx.db.ListDocuments(ctx, src.Name())
	if err != nil {
		return fmt.Errorf("listing documents for reconciliation: %w", err)
	}

	for _, doc := range docs {
		if _, current := currentIDs[doc.ID]; current || !src.IsDocumentInScope(file, doc) {
			continue
		}
		if err := idx.removeDocument(ctx, doc); err != nil {
			return fmt.Errorf("removing stale document %s: %w", doc.ID, err)
		}
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
	var chunks []chunker.Chunk
	if doc.Source == storage.SourceCode {
		chunks = chunker.SplitCode(doc.Content, doc.Metadata["language"], chunker.DefaultOptions())
	} else {
		chunks = chunker.Split(doc.Content, chunker.DefaultOptions())
	}
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

// Prune removes indexed documents whose backing file no longer exists.
// Virtual browser/clipboard entries are left untouched. Callers should
// SaveVectors afterwards to persist vector removals.
func (idx *Indexer) Prune(ctx context.Context) (int, error) {
	docs, err := idx.db.ListDocuments(ctx, "")
	if err != nil {
		return 0, err
	}

	removed := 0
	for _, doc := range docs {
		if !storage.IsFileBackedSource(doc.Source) {
			continue
		}
		backingPath := doc.Path
		if original := strings.TrimSpace(doc.Metadata["original_path"]); original != "" {
			backingPath = original
		}
		if _, err := os.Stat(backingPath); !os.IsNotExist(err) {
			continue
		}
		if err := idx.removeDocument(ctx, doc); err != nil {
			if idx.progress != nil {
				idx.progress.OnError(string(doc.Source), doc.Path, err)
			}
			continue
		}
		removed++
	}
	return removed, nil
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
