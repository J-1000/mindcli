package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "mindcli-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func TestOpen(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	if db == nil {
		t.Fatal("Open() returned nil")
	}
}

func TestOpenInvalidPath(t *testing.T) {
	_, err := Open("/nonexistent/path/to/db.sqlite")
	if err == nil {
		t.Error("Expected error when opening database in nonexistent directory")
	}
}

func TestInsertAndGetDocument(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	doc := &Document{
		ID:          "test-doc-1",
		Source:      SourceMarkdown,
		Path:        "/home/user/notes/test.md",
		Title:       "Test Document",
		Content:     "This is the full content of the document.",
		Preview:     "This is the preview...",
		Metadata:    map[string]string{"author": "test", "tags": "go,testing"},
		ContentHash: "abc123",
		IndexedAt:   now,
		ModifiedAt:  now,
	}

	// Insert
	err := db.InsertDocument(ctx, doc)
	if err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}

	// Get
	retrieved, err := db.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetDocument() error = %v", err)
	}

	// Verify fields
	if retrieved.ID != doc.ID {
		t.Errorf("ID = %q, want %q", retrieved.ID, doc.ID)
	}
	if retrieved.Source != doc.Source {
		t.Errorf("Source = %q, want %q", retrieved.Source, doc.Source)
	}
	if retrieved.Path != doc.Path {
		t.Errorf("Path = %q, want %q", retrieved.Path, doc.Path)
	}
	if retrieved.Title != doc.Title {
		t.Errorf("Title = %q, want %q", retrieved.Title, doc.Title)
	}
	if retrieved.Content != doc.Content {
		t.Errorf("Content = %q, want %q", retrieved.Content, doc.Content)
	}
	if retrieved.Metadata["author"] != "test" {
		t.Errorf("Metadata[author] = %q, want %q", retrieved.Metadata["author"], "test")
	}
}

func TestGetDocumentNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.GetDocument(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("GetDocument() error = %v, want ErrNotFound", err)
	}
}

func TestGetDocumentByPath(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc := &Document{
		ID:          "doc-by-path",
		Source:      SourceMarkdown,
		Path:        "/unique/path/document.md",
		Title:       "Path Test",
		ContentHash: "hash123",
		IndexedAt:   now,
		ModifiedAt:  now,
	}

	err := db.InsertDocument(ctx, doc)
	if err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}

	retrieved, err := db.GetDocumentByPath(ctx, doc.Path)
	if err != nil {
		t.Fatalf("GetDocumentByPath() error = %v", err)
	}

	if retrieved.ID != doc.ID {
		t.Errorf("ID = %q, want %q", retrieved.ID, doc.ID)
	}
}

func TestUpdateDocument(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc := &Document{
		ID:          "update-test",
		Source:      SourceMarkdown,
		Path:        "/path/to/doc.md",
		Title:       "Original Title",
		Content:     "Original content",
		ContentHash: "original-hash",
		IndexedAt:   now,
		ModifiedAt:  now,
	}

	err := db.InsertDocument(ctx, doc)
	if err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}

	// Update
	doc.Title = "Updated Title"
	doc.Content = "Updated content"
	doc.ContentHash = "updated-hash"
	doc.ModifiedAt = now.Add(time.Hour)

	err = db.UpdateDocument(ctx, doc)
	if err != nil {
		t.Fatalf("UpdateDocument() error = %v", err)
	}

	// Verify
	retrieved, err := db.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetDocument() error = %v", err)
	}

	if retrieved.Title != "Updated Title" {
		t.Errorf("Title = %q, want %q", retrieved.Title, "Updated Title")
	}
	if retrieved.Content != "Updated content" {
		t.Errorf("Content = %q, want %q", retrieved.Content, "Updated content")
	}
}

func TestUpdateDocumentNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	doc := &Document{
		ID:          "nonexistent",
		Source:      SourceMarkdown,
		Path:        "/path",
		ContentHash: "hash",
		IndexedAt:   time.Now(),
		ModifiedAt:  time.Now(),
	}

	err := db.UpdateDocument(ctx, doc)
	if err != ErrNotFound {
		t.Errorf("UpdateDocument() error = %v, want ErrNotFound", err)
	}
}

func TestUpsertDocument(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc := &Document{
		ID:          "upsert-test",
		Source:      SourceMarkdown,
		Path:        "/path/to/doc.md",
		Title:       "Original Title",
		ContentHash: "hash",
		IndexedAt:   now,
		ModifiedAt:  now,
	}

	// First upsert (insert)
	err := db.UpsertDocument(ctx, doc)
	if err != nil {
		t.Fatalf("UpsertDocument() insert error = %v", err)
	}

	// Second upsert (update)
	doc.Title = "Updated Title"
	err = db.UpsertDocument(ctx, doc)
	if err != nil {
		t.Fatalf("UpsertDocument() update error = %v", err)
	}

	// Verify
	retrieved, err := db.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetDocument() error = %v", err)
	}

	if retrieved.Title != "Updated Title" {
		t.Errorf("Title = %q, want %q", retrieved.Title, "Updated Title")
	}

	// Should still be one document
	count, err := db.CountDocuments(ctx)
	if err != nil {
		t.Fatalf("CountDocuments() error = %v", err)
	}
	if count != 1 {
		t.Errorf("CountDocuments() = %d, want 1", count)
	}
}

func TestDeleteDocument(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc := &Document{
		ID:          "delete-test",
		Source:      SourceMarkdown,
		Path:        "/path/to/delete.md",
		ContentHash: "hash",
		IndexedAt:   now,
		ModifiedAt:  now,
	}

	err := db.InsertDocument(ctx, doc)
	if err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}

	err = db.DeleteDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("DeleteDocument() error = %v", err)
	}

	_, err = db.GetDocument(ctx, doc.ID)
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteDocumentNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	err := db.DeleteDocument(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("DeleteDocument() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteDocumentByPath(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc := &Document{
		ID:          "delete-path-test",
		Source:      SourceMarkdown,
		Path:        "/unique/delete/path.md",
		ContentHash: "hash",
		IndexedAt:   now,
		ModifiedAt:  now,
	}

	err := db.InsertDocument(ctx, doc)
	if err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}

	err = db.DeleteDocumentByPath(ctx, doc.Path)
	if err != nil {
		t.Fatalf("DeleteDocumentByPath() error = %v", err)
	}

	_, err = db.GetDocument(ctx, doc.ID)
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound after delete, got %v", err)
	}
}

func TestListDocuments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Insert documents of different sources
	docs := []*Document{
		{ID: "md1", Source: SourceMarkdown, Path: "/md1.md", ContentHash: "h1", IndexedAt: now, ModifiedAt: now},
		{ID: "md2", Source: SourceMarkdown, Path: "/md2.md", ContentHash: "h2", IndexedAt: now, ModifiedAt: now.Add(time.Hour)},
		{ID: "pdf1", Source: SourcePDF, Path: "/doc.pdf", ContentHash: "h3", IndexedAt: now, ModifiedAt: now},
	}

	for _, doc := range docs {
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("InsertDocument() error = %v", err)
		}
	}

	// List all
	all, err := db.ListDocuments(ctx, "")
	if err != nil {
		t.Fatalf("ListDocuments() error = %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListDocuments() returned %d documents, want 3", len(all))
	}

	// List by source
	mdDocs, err := db.ListDocuments(ctx, SourceMarkdown)
	if err != nil {
		t.Fatalf("ListDocuments(markdown) error = %v", err)
	}
	if len(mdDocs) != 2 {
		t.Errorf("ListDocuments(markdown) returned %d documents, want 2", len(mdDocs))
	}

	pdfDocs, err := db.ListDocuments(ctx, SourcePDF)
	if err != nil {
		t.Fatalf("ListDocuments(pdf) error = %v", err)
	}
	if len(pdfDocs) != 1 {
		t.Errorf("ListDocuments(pdf) returned %d documents, want 1", len(pdfDocs))
	}
}

func TestCountDocuments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Initially empty
	count, err := db.CountDocuments(ctx)
	if err != nil {
		t.Fatalf("CountDocuments() error = %v", err)
	}
	if count != 0 {
		t.Errorf("CountDocuments() = %d, want 0", count)
	}

	// Add documents
	for i := 0; i < 5; i++ {
		doc := &Document{
			ID:          "count-" + string(rune('a'+i)),
			Source:      SourceMarkdown,
			Path:        "/path/" + string(rune('a'+i)) + ".md",
			ContentHash: "hash",
			IndexedAt:   now,
			ModifiedAt:  now,
		}
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("InsertDocument() error = %v", err)
		}
	}

	count, err = db.CountDocuments(ctx)
	if err != nil {
		t.Fatalf("CountDocuments() error = %v", err)
	}
	if count != 5 {
		t.Errorf("CountDocuments() = %d, want 5", count)
	}
}

func TestCountDocumentsBySource(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	docs := []*Document{
		{ID: "s1", Source: SourceMarkdown, Path: "/1.md", ContentHash: "h", IndexedAt: now, ModifiedAt: now},
		{ID: "s2", Source: SourceMarkdown, Path: "/2.md", ContentHash: "h", IndexedAt: now, ModifiedAt: now},
		{ID: "s3", Source: SourcePDF, Path: "/1.pdf", ContentHash: "h", IndexedAt: now, ModifiedAt: now},
	}

	for _, doc := range docs {
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("InsertDocument() error = %v", err)
		}
	}

	mdCount, err := db.CountDocumentsBySource(ctx, SourceMarkdown)
	if err != nil {
		t.Fatalf("CountDocumentsBySource() error = %v", err)
	}
	if mdCount != 2 {
		t.Errorf("CountDocumentsBySource(markdown) = %d, want 2", mdCount)
	}

	pdfCount, err := db.CountDocumentsBySource(ctx, SourcePDF)
	if err != nil {
		t.Fatalf("CountDocumentsBySource() error = %v", err)
	}
	if pdfCount != 1 {
		t.Errorf("CountDocumentsBySource(pdf) = %d, want 1", pdfCount)
	}
}

func TestSearchDocuments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	docs := []*Document{
		{ID: "search1", Source: SourceMarkdown, Path: "/1.md", Title: "Go Programming", Content: "Learn about goroutines", ContentHash: "h", IndexedAt: now, ModifiedAt: now},
		{ID: "search2", Source: SourceMarkdown, Path: "/2.md", Title: "Python Basics", Content: "Introduction to Python", ContentHash: "h", IndexedAt: now, ModifiedAt: now},
		{ID: "search3", Source: SourceMarkdown, Path: "/3.md", Title: "Advanced Go", Content: "Channels and concurrency", ContentHash: "h", IndexedAt: now, ModifiedAt: now},
	}

	for _, doc := range docs {
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("InsertDocument() error = %v", err)
		}
	}

	// Search by title
	results, err := db.SearchDocuments(ctx, "Go", 10)
	if err != nil {
		t.Fatalf("SearchDocuments() error = %v", err)
	}
	if len(results) != 2 {
		t.Errorf("SearchDocuments('Go') returned %d results, want 2", len(results))
	}

	// Search by content
	results, err = db.SearchDocuments(ctx, "goroutines", 10)
	if err != nil {
		t.Fatalf("SearchDocuments() error = %v", err)
	}
	if len(results) != 1 {
		t.Errorf("SearchDocuments('goroutines') returned %d results, want 1", len(results))
	}

	// Search with limit
	results, err = db.SearchDocuments(ctx, "Go", 1)
	if err != nil {
		t.Fatalf("SearchDocuments() error = %v", err)
	}
	if len(results) != 1 {
		t.Errorf("SearchDocuments() with limit 1 returned %d results, want 1", len(results))
	}
}

func TestChunks(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// First insert a document
	doc := &Document{
		ID:          "chunk-doc",
		Source:      SourceMarkdown,
		Path:        "/chunk-test.md",
		ContentHash: "hash",
		IndexedAt:   now,
		ModifiedAt:  now,
	}
	if err := db.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}

	// Insert chunks
	chunks := []*Chunk{
		{ID: "c1", DocumentID: doc.ID, Content: "First chunk", StartPos: 0, EndPos: 100},
		{ID: "c2", DocumentID: doc.ID, Content: "Second chunk", StartPos: 100, EndPos: 200},
		{ID: "c3", DocumentID: doc.ID, Content: "Third chunk", StartPos: 200, EndPos: 300},
	}

	for _, chunk := range chunks {
		if err := db.InsertChunk(ctx, chunk); err != nil {
			t.Fatalf("InsertChunk() error = %v", err)
		}
	}

	// Get chunks
	retrieved, err := db.GetChunksByDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetChunksByDocument() error = %v", err)
	}
	if len(retrieved) != 3 {
		t.Errorf("GetChunksByDocument() returned %d chunks, want 3", len(retrieved))
	}

	// Verify order
	if retrieved[0].StartPos != 0 {
		t.Errorf("First chunk StartPos = %d, want 0", retrieved[0].StartPos)
	}
	if retrieved[1].StartPos != 100 {
		t.Errorf("Second chunk StartPos = %d, want 100", retrieved[1].StartPos)
	}

	// Delete chunks
	err = db.DeleteChunksByDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("DeleteChunksByDocument() error = %v", err)
	}

	retrieved, err = db.GetChunksByDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetChunksByDocument() after delete error = %v", err)
	}
	if len(retrieved) != 0 {
		t.Errorf("GetChunksByDocument() after delete returned %d chunks, want 0", len(retrieved))
	}
}
