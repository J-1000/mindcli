package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jankowtf/mindcli/internal/config"
	"github.com/jankowtf/mindcli/internal/index/sources"
	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
)

func TestIndexer_IndexAll(t *testing.T) {
	// Create temp directories
	tmpDir, err := os.MkdirTemp("", "indexer-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	notesDir := filepath.Join(tmpDir, "notes")
	dataDir := filepath.Join(tmpDir, "data")
	os.MkdirAll(notesDir, 0755)
	os.MkdirAll(dataDir, 0755)

	// Create test markdown files
	files := map[string]string{
		"note1.md": `---
title: First Note
tags: [test, golang]
---

# First Note

This is the content of the first note about Go programming.
`,
		"note2.md": `# Second Note

Another note about Rust programming language.

#rust #programming
`,
		"subdir/note3.md": `---
title: Nested Note
---

# Nested Note

A note in a subdirectory.
`,
	}

	for name, content := range files {
		path := filepath.Join(notesDir, name)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("writing file: %v", err)
		}
	}

	// Set up database
	dbPath := filepath.Join(dataDir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	// Set up search index
	indexPath := filepath.Join(dataDir, "test.bleve")
	searchIdx, err := search.NewBleveIndex(indexPath)
	if err != nil {
		t.Fatalf("creating search index: %v", err)
	}
	defer searchIdx.Close()

	// Create config
	cfg := &config.Config{
		Sources: config.SourcesConfig{
			Markdown: config.MarkdownSourceConfig{
				Enabled:    true,
				Paths:      []string{notesDir},
				Extensions: []string{".md"},
				Ignore:     []string{".git"},
			},
		},
		Indexing: config.IndexingConfig{
			Workers: 2,
		},
	}

	// Create indexer with progress tracking
	indexer := NewIndexer(db, searchIdx, nil, nil, cfg)

	var progress testProgressReporter
	indexer.SetProgressReporter(&progress)

	// Run indexing
	ctx := context.Background()
	stats, err := indexer.IndexAll(ctx)
	if err != nil {
		t.Fatalf("indexing: %v", err)
	}

	// Verify stats
	if stats.TotalFiles != 3 {
		t.Errorf("TotalFiles = %d, want 3", stats.TotalFiles)
	}
	if stats.IndexedFiles != 3 {
		t.Errorf("IndexedFiles = %d, want 3", stats.IndexedFiles)
	}
	if stats.Errors != 0 {
		t.Errorf("Errors = %d, want 0", stats.Errors)
	}

	// Verify progress callbacks
	if !progress.started {
		t.Error("OnStart not called")
	}
	if progress.total != 3 {
		t.Errorf("total = %d, want 3", progress.total)
	}
	if !progress.completed {
		t.Error("OnComplete not called")
	}

	// Verify documents in database
	docs, err := db.ListDocuments(ctx, storage.SourceMarkdown)
	if err != nil {
		t.Fatalf("listing documents: %v", err)
	}
	if len(docs) != 3 {
		t.Errorf("got %d documents, want 3", len(docs))
	}

	// Verify search works
	time.Sleep(100 * time.Millisecond) // Let Bleve finish indexing

	results, err := searchIdx.Search(ctx, "Go programming", 10)
	if err != nil {
		t.Fatalf("searching: %v", err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 search result")
	}
}

func TestIndexer_IncrementalIndexing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "indexer-incremental-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	notesDir := filepath.Join(tmpDir, "notes")
	dataDir := filepath.Join(tmpDir, "data")
	os.MkdirAll(notesDir, 0755)
	os.MkdirAll(dataDir, 0755)

	// Create initial file
	filePath := filepath.Join(notesDir, "note.md")
	if err := os.WriteFile(filePath, []byte("# Original Content"), 0644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	// Set up database and search
	db, err := storage.Open(filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	searchIdx, err := search.NewBleveIndex(filepath.Join(dataDir, "test.bleve"))
	if err != nil {
		t.Fatalf("creating search index: %v", err)
	}
	defer searchIdx.Close()

	cfg := &config.Config{
		Sources: config.SourcesConfig{
			Markdown: config.MarkdownSourceConfig{
				Enabled:    true,
				Paths:      []string{notesDir},
				Extensions: []string{".md"},
			},
		},
		Indexing: config.IndexingConfig{Workers: 1},
	}

	indexer := NewIndexer(db, searchIdx, nil, nil, cfg)
	ctx := context.Background()

	// First index
	stats1, err := indexer.IndexAll(ctx)
	if err != nil {
		t.Fatalf("first indexing: %v", err)
	}
	if stats1.IndexedFiles != 1 {
		t.Errorf("first run: IndexedFiles = %d, want 1", stats1.IndexedFiles)
	}

	// Index again without changes - should skip
	stats2, err := indexer.IndexAll(ctx)
	if err != nil {
		t.Fatalf("second indexing: %v", err)
	}
	// The file should be counted but skipped due to unchanged modtime
	if stats2.TotalFiles != 1 {
		t.Errorf("second run: TotalFiles = %d, want 1", stats2.TotalFiles)
	}

	// Modify file
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filePath, []byte("# Updated Content"), 0644); err != nil {
		t.Fatalf("updating file: %v", err)
	}

	// Index again - should reindex
	stats3, err := indexer.IndexAll(ctx)
	if err != nil {
		t.Fatalf("third indexing: %v", err)
	}
	if stats3.IndexedFiles != 1 {
		t.Errorf("third run: IndexedFiles = %d, want 1", stats3.IndexedFiles)
	}
}

func TestIndexer_Cancellation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "indexer-cancel-test")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	notesDir := filepath.Join(tmpDir, "notes")
	dataDir := filepath.Join(tmpDir, "data")
	os.MkdirAll(notesDir, 0755)
	os.MkdirAll(dataDir, 0755)

	// Create many files
	for i := 0; i < 50; i++ {
		path := filepath.Join(notesDir, "note"+string(rune('a'+i%26))+".md")
		os.WriteFile(path, []byte("# Note "+string(rune('a'+i%26))), 0644)
	}

	db, _ := storage.Open(filepath.Join(dataDir, "test.db"))
	defer db.Close()

	searchIdx, _ := search.NewBleveIndex(filepath.Join(dataDir, "test.bleve"))
	defer searchIdx.Close()

	cfg := &config.Config{
		Sources: config.SourcesConfig{
			Markdown: config.MarkdownSourceConfig{
				Enabled:    true,
				Paths:      []string{notesDir},
				Extensions: []string{".md"},
			},
		},
		Indexing: config.IndexingConfig{Workers: 1},
	}

	indexer := NewIndexer(db, searchIdx, nil, nil, cfg)

	// Cancel after short delay
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	stats, err := indexer.IndexAll(ctx)
	if err != context.Canceled {
		t.Logf("indexing returned: err=%v, stats=%+v", err, stats)
	}
	// Note: Cancellation may or may not return an error depending on timing
}

func TestIndexer_RemoveFileDeletesVectors(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	searchIdx, err := search.NewBleveIndex(filepath.Join(tmpDir, "test.bleve"))
	if err != nil {
		t.Fatalf("creating search index: %v", err)
	}
	defer searchIdx.Close()

	vectors, err := storage.NewVectorStore(filepath.Join(tmpDir, "vectors.graph"))
	if err != nil {
		t.Fatalf("creating vector store: %v", err)
	}
	defer vectors.Close()

	cfg := &config.Config{Indexing: config.IndexingConfig{Workers: 1}}
	indexer := NewIndexer(db, searchIdx, vectors, nil, cfg)

	ctx := context.Background()
	now := time.Now().UTC()
	doc := &storage.Document{
		ID:          "doc-remove",
		Source:      storage.SourceMarkdown,
		Path:        filepath.Join(tmpDir, "note.md"),
		Title:       "Note",
		Content:     "content",
		ContentHash: "hash-remove",
		IndexedAt:   now,
		ModifiedAt:  now,
	}

	if err := db.UpsertDocument(ctx, doc); err != nil {
		t.Fatalf("upserting document: %v", err)
	}
	if err := searchIdx.Index(ctx, doc); err != nil {
		t.Fatalf("indexing document: %v", err)
	}
	chunk := &storage.Chunk{
		ID:         "doc-remove:0",
		DocumentID: doc.ID,
		Content:    "content",
		StartPos:   0,
		EndPos:     7,
	}
	if err := db.InsertChunk(ctx, chunk); err != nil {
		t.Fatalf("inserting chunk: %v", err)
	}
	vectors.Add(chunk.ID, []float32{1, 0})
	if got := vectors.Len(); got != 1 {
		t.Fatalf("vector count before remove = %d, want 1", got)
	}

	if err := indexer.RemoveFile(ctx, doc.Path); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}

	if got := vectors.Len(); got != 0 {
		t.Fatalf("vector count after remove = %d, want 0", got)
	}
}

func TestIndexer_EmbedDocumentRemovesStaleVectors(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	searchIdx, err := search.NewBleveIndex(filepath.Join(tmpDir, "test.bleve"))
	if err != nil {
		t.Fatalf("creating search index: %v", err)
	}
	defer searchIdx.Close()

	vectors, err := storage.NewVectorStore(filepath.Join(tmpDir, "vectors.graph"))
	if err != nil {
		t.Fatalf("creating vector store: %v", err)
	}
	defer vectors.Close()

	cfg := &config.Config{Indexing: config.IndexingConfig{Workers: 1}}
	embedder := &testEmbedder{}
	indexer := NewIndexer(db, searchIdx, vectors, embedder, cfg)

	ctx := context.Background()
	doc := &storage.Document{
		ID:      "doc-embed",
		Source:  storage.SourceMarkdown,
		Path:    filepath.Join(tmpDir, "embed.md"),
		Title:   "Embed",
		Content: "fresh content",
	}
	if err := db.UpsertDocument(ctx, &storage.Document{
		ID:          doc.ID,
		Source:      doc.Source,
		Path:        doc.Path,
		Title:       doc.Title,
		Content:     "old content",
		ContentHash: "hash-old",
		IndexedAt:   time.Now().UTC(),
		ModifiedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upserting document: %v", err)
	}

	staleChunk := &storage.Chunk{
		ID:         "doc-embed:stale",
		DocumentID: doc.ID,
		Content:    "stale content",
		StartPos:   0,
		EndPos:     12,
	}
	if err := db.InsertChunk(ctx, staleChunk); err != nil {
		t.Fatalf("inserting stale chunk: %v", err)
	}
	vectors.Add(staleChunk.ID, []float32{9, 9})
	if vectors.Len() != 1 {
		t.Fatalf("expected 1 stale vector before embed, got %d", vectors.Len())
	}

	indexer.embedDocument(ctx, doc)

	chunks, err := db.GetChunksByDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("loading chunks: %v", err)
	}
	if len(chunks) != vectors.Len() {
		t.Fatalf("chunks=%d vectors=%d, expected equality after re-embed", len(chunks), vectors.Len())
	}
}

func TestIndexer_IndexFile_UsesStatPathWithoutScan(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "note.md")
	if err := os.WriteFile(filePath, []byte("# note"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	searchIdx, err := search.NewBleveIndex(filepath.Join(tmpDir, "test.bleve"))
	if err != nil {
		t.Fatalf("creating search index: %v", err)
	}
	defer searchIdx.Close()

	src := &mockSource{
		name:      storage.SourceMarkdown,
		matchPath: filePath,
	}

	idx := &Indexer{
		db:      db,
		search:  searchIdx,
		sources: []sources.Source{src},
	}

	if err := idx.IndexFile(context.Background(), filePath); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	if src.scanCalls != 0 {
		t.Fatalf("scanCalls = %d, want 0", src.scanCalls)
	}
	if src.parseCalls != 1 {
		t.Fatalf("parseCalls = %d, want 1", src.parseCalls)
	}
	if src.lastParsed.Path != filePath {
		t.Fatalf("parsed path = %q, want %q", src.lastParsed.Path, filePath)
	}
}

func TestIndexer_IndexFile_FallsBackToSourceScan(t *testing.T) {
	tmpDir := t.TempDir()
	virtualPath := "clipboard:test"

	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	searchIdx, err := search.NewBleveIndex(filepath.Join(tmpDir, "test.bleve"))
	if err != nil {
		t.Fatalf("creating search index: %v", err)
	}
	defer searchIdx.Close()

	src := &mockSource{
		name:      storage.SourceClipboard,
		matchPath: virtualPath,
		scanFiles: []sources.FileInfo{
			{Path: virtualPath, ModifiedAt: time.Now().Unix(), Size: 42},
		},
	}

	idx := &Indexer{
		db:      db,
		search:  searchIdx,
		sources: []sources.Source{src},
	}

	if err := idx.IndexFile(context.Background(), virtualPath); err != nil {
		t.Fatalf("IndexFile: %v", err)
	}

	if src.scanCalls != 1 {
		t.Fatalf("scanCalls = %d, want 1", src.scanCalls)
	}
	if src.parseCalls != 1 {
		t.Fatalf("parseCalls = %d, want 1", src.parseCalls)
	}
	if src.lastParsed.Path != virtualPath {
		t.Fatalf("parsed path = %q, want %q", src.lastParsed.Path, virtualPath)
	}
}

// testProgressReporter tracks progress calls for testing.
type testProgressReporter struct {
	mu        sync.Mutex
	started   bool
	completed bool
	source    string
	total     int
	current   int
	errors    []error
}

func (p *testProgressReporter) OnStart(source string, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = true
	p.source = source
	p.total = total
}

func (p *testProgressReporter) OnProgress(source string, current, total int, path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.current = current
}

func (p *testProgressReporter) OnComplete(source string, indexed, errors int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.completed = true
}

func (p *testProgressReporter) OnError(source string, path string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.errors = append(p.errors, err)
}

type testEmbedder struct{}

func (e *testEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return []float32{float32(len(text)), 1}, nil
}

func (e *testEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{float32(len(text)), float32(i + 1)}
	}
	return out, nil
}

func (e *testEmbedder) Dimensions() int { return 2 }

type mockSource struct {
	name      storage.Source
	matchPath string
	scanFiles []sources.FileInfo

	scanCalls  int
	parseCalls int
	lastParsed sources.FileInfo
}

func (m *mockSource) Name() storage.Source { return m.name }

func (m *mockSource) Scan(ctx context.Context) (<-chan sources.FileInfo, <-chan error) {
	m.scanCalls++
	files := make(chan sources.FileInfo, len(m.scanFiles))
	errs := make(chan error, 1)
	for _, f := range m.scanFiles {
		files <- f
	}
	close(files)
	close(errs)
	return files, errs
}

func (m *mockSource) MatchesPath(path string) bool {
	return path == m.matchPath
}

func (m *mockSource) Parse(ctx context.Context, file sources.FileInfo) (*storage.Document, error) {
	m.parseCalls++
	m.lastParsed = file
	if file.Path == "" {
		return nil, fmt.Errorf("empty path")
	}

	now := time.Now().UTC()
	return &storage.Document{
		ID:          "doc:" + file.Path,
		Source:      m.name,
		Path:        file.Path,
		Title:       file.Path,
		Content:     "content",
		Preview:     "content",
		ContentHash: "hash:" + file.Path,
		IndexedAt:   now,
		ModifiedAt:  now,
	}, nil
}
