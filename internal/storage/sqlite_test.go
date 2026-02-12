package storage

import (
	"context"
	"fmt"
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

func TestAddAndGetTags(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc := &Document{
		ID: "tag-doc", Source: SourceMarkdown, Path: "/tag.md",
		ContentHash: "h", IndexedAt: now, ModifiedAt: now,
	}
	if err := db.InsertDocument(ctx, doc); err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}

	// Add manual tags
	if err := db.AddTag(ctx, doc.ID, "golang"); err != nil {
		t.Fatalf("AddTag() error = %v", err)
	}
	if err := db.AddTag(ctx, doc.ID, "tutorial"); err != nil {
		t.Fatalf("AddTag() error = %v", err)
	}

	// Add auto tag
	if err := db.AddAutoTag(ctx, doc.ID, "concurrency"); err != nil {
		t.Fatalf("AddAutoTag() error = %v", err)
	}

	// Get all tags
	tags, err := db.GetTags(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetTags() error = %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("GetTags() returned %d tags, want 3", len(tags))
	}

	// Tags should be sorted alphabetically
	if tags[0] != "concurrency" || tags[1] != "golang" || tags[2] != "tutorial" {
		t.Errorf("GetTags() = %v, want [concurrency golang tutorial]", tags)
	}

	// Duplicate add should be ignored
	if err := db.AddTag(ctx, doc.ID, "golang"); err != nil {
		t.Fatalf("AddTag() duplicate error = %v", err)
	}
	tags, _ = db.GetTags(ctx, doc.ID)
	if len(tags) != 3 {
		t.Errorf("after duplicate add, got %d tags, want 3", len(tags))
	}
}

func TestRemoveTag(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc := &Document{
		ID: "rm-tag-doc", Source: SourceMarkdown, Path: "/rm.md",
		ContentHash: "h", IndexedAt: now, ModifiedAt: now,
	}
	db.InsertDocument(ctx, doc)
	db.AddTag(ctx, doc.ID, "manual-tag")
	db.AddAutoTag(ctx, doc.ID, "auto-tag")

	// Remove manual tag should work
	if err := db.RemoveTag(ctx, doc.ID, "manual-tag"); err != nil {
		t.Fatalf("RemoveTag() error = %v", err)
	}

	// Remove auto tag should fail (only removes manual)
	err := db.RemoveTag(ctx, doc.ID, "auto-tag")
	if err != ErrNotFound {
		t.Errorf("RemoveTag(auto-tag) error = %v, want ErrNotFound", err)
	}

	// Auto tag should still be there
	tags, _ := db.GetTags(ctx, doc.ID)
	if len(tags) != 1 || tags[0] != "auto-tag" {
		t.Errorf("GetTags() = %v, want [auto-tag]", tags)
	}

	// Remove nonexistent tag
	err = db.RemoveTag(ctx, doc.ID, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("RemoveTag(nonexistent) error = %v, want ErrNotFound", err)
	}
}

func TestListAllTags(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc1 := &Document{ID: "t1", Source: SourceMarkdown, Path: "/1.md", ContentHash: "h", IndexedAt: now, ModifiedAt: now}
	doc2 := &Document{ID: "t2", Source: SourceMarkdown, Path: "/2.md", ContentHash: "h", IndexedAt: now, ModifiedAt: now}
	db.InsertDocument(ctx, doc1)
	db.InsertDocument(ctx, doc2)

	db.AddTag(ctx, doc1.ID, "golang")
	db.AddTag(ctx, doc1.ID, "concurrency")
	db.AddTag(ctx, doc2.ID, "golang")
	db.AddTag(ctx, doc2.ID, "testing")

	tags, err := db.ListAllTags(ctx)
	if err != nil {
		t.Fatalf("ListAllTags() error = %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("ListAllTags() returned %d tags, want 3 (unique)", len(tags))
	}
	if tags[0] != "concurrency" || tags[1] != "golang" || tags[2] != "testing" {
		t.Errorf("ListAllTags() = %v, want [concurrency golang testing]", tags)
	}
}

func TestFindByTag(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	doc1 := &Document{ID: "f1", Source: SourceMarkdown, Path: "/1.md", Title: "Go Doc", ContentHash: "h", IndexedAt: now, ModifiedAt: now}
	doc2 := &Document{ID: "f2", Source: SourceMarkdown, Path: "/2.md", Title: "Rust Doc", ContentHash: "h", IndexedAt: now, ModifiedAt: now}
	doc3 := &Document{ID: "f3", Source: SourceMarkdown, Path: "/3.md", Title: "Python Doc", ContentHash: "h", IndexedAt: now, ModifiedAt: now}
	db.InsertDocument(ctx, doc1)
	db.InsertDocument(ctx, doc2)
	db.InsertDocument(ctx, doc3)

	db.AddTag(ctx, doc1.ID, "programming")
	db.AddTag(ctx, doc2.ID, "programming")
	db.AddTag(ctx, doc3.ID, "scripting")

	docs, err := db.FindByTag(ctx, "programming")
	if err != nil {
		t.Fatalf("FindByTag() error = %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("FindByTag(programming) returned %d docs, want 2", len(docs))
	}

	docs, err = db.FindByTag(ctx, "scripting")
	if err != nil {
		t.Fatalf("FindByTag() error = %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("FindByTag(scripting) returned %d docs, want 1", len(docs))
	}
	if docs[0].Title != "Python Doc" {
		t.Errorf("FindByTag(scripting)[0].Title = %q, want %q", docs[0].Title, "Python Doc")
	}

	// Nonexistent tag
	docs, err = db.FindByTag(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("FindByTag(nonexistent) error = %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("FindByTag(nonexistent) returned %d docs, want 0", len(docs))
	}
}

func TestCreateCollection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	c := &Collection{Name: "reading-list", Description: "Books to read"}
	if err := db.CreateCollection(ctx, c); err != nil {
		t.Fatalf("CreateCollection() error = %v", err)
	}
	if c.ID == "" {
		t.Error("CreateCollection() did not generate ID")
	}
	if c.CreatedAt.IsZero() {
		t.Error("CreateCollection() did not set CreatedAt")
	}
}

func TestCreateCollectionDuplicate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	c1 := &Collection{Name: "dupe"}
	if err := db.CreateCollection(ctx, c1); err != nil {
		t.Fatalf("CreateCollection() error = %v", err)
	}
	c2 := &Collection{Name: "dupe"}
	err := db.CreateCollection(ctx, c2)
	if err != ErrCollectionExists {
		t.Errorf("CreateCollection() error = %v, want ErrCollectionExists", err)
	}
}

func TestGetCollection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	c := &Collection{Name: "test-col", Description: "desc", Query: "golang"}
	db.CreateCollection(ctx, c)

	got, err := db.GetCollection(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetCollection() error = %v", err)
	}
	if got.Name != "test-col" {
		t.Errorf("Name = %q, want %q", got.Name, "test-col")
	}
	if got.Description != "desc" {
		t.Errorf("Description = %q, want %q", got.Description, "desc")
	}
	if got.Query != "golang" {
		t.Errorf("Query = %q, want %q", got.Query, "golang")
	}
}

func TestGetCollectionNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.GetCollection(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Errorf("GetCollection() error = %v, want ErrNotFound", err)
	}
}

func TestGetCollectionByName(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	c := &Collection{Name: "by-name"}
	db.CreateCollection(ctx, c)

	got, err := db.GetCollectionByName(ctx, "by-name")
	if err != nil {
		t.Fatalf("GetCollectionByName() error = %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("ID = %q, want %q", got.ID, c.ID)
	}
}

func TestGetCollectionByNameNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.GetCollectionByName(context.Background(), "nope")
	if err != ErrNotFound {
		t.Errorf("GetCollectionByName() error = %v, want ErrNotFound", err)
	}
}

func TestListCollections(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	db.CreateCollection(ctx, &Collection{Name: "beta"})
	db.CreateCollection(ctx, &Collection{Name: "alpha"})
	db.CreateCollection(ctx, &Collection{Name: "gamma"})

	cols, err := db.ListCollections(ctx)
	if err != nil {
		t.Fatalf("ListCollections() error = %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("ListCollections() returned %d, want 3", len(cols))
	}
	// Should be ordered by name
	if cols[0].Name != "alpha" || cols[1].Name != "beta" || cols[2].Name != "gamma" {
		t.Errorf("ListCollections() order = [%s %s %s], want [alpha beta gamma]",
			cols[0].Name, cols[1].Name, cols[2].Name)
	}
}

func TestListCollectionsEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	cols, err := db.ListCollections(context.Background())
	if err != nil {
		t.Fatalf("ListCollections() error = %v", err)
	}
	if len(cols) != 0 {
		t.Errorf("ListCollections() returned %d, want 0", len(cols))
	}
}

func TestRenameCollection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	c := &Collection{Name: "old-name"}
	db.CreateCollection(ctx, c)

	if err := db.RenameCollection(ctx, c.ID, "new-name"); err != nil {
		t.Fatalf("RenameCollection() error = %v", err)
	}

	got, _ := db.GetCollection(ctx, c.ID)
	if got.Name != "new-name" {
		t.Errorf("Name = %q, want %q", got.Name, "new-name")
	}
}

func TestRenameCollectionDuplicate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	db.CreateCollection(ctx, &Collection{Name: "taken"})
	c := &Collection{Name: "rename-me"}
	db.CreateCollection(ctx, c)

	err := db.RenameCollection(ctx, c.ID, "taken")
	if err != ErrCollectionExists {
		t.Errorf("RenameCollection() error = %v, want ErrCollectionExists", err)
	}
}

func TestDeleteCollection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	c := &Collection{Name: "delete-me"}
	db.CreateCollection(ctx, c)

	if err := db.DeleteCollection(ctx, c.ID); err != nil {
		t.Fatalf("DeleteCollection() error = %v", err)
	}

	_, err := db.GetCollection(ctx, c.ID)
	if err != ErrNotFound {
		t.Errorf("after delete, GetCollection() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteCollectionNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := db.DeleteCollection(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Errorf("DeleteCollection() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteCollectionByName(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	db.CreateCollection(ctx, &Collection{Name: "del-by-name"})

	if err := db.DeleteCollectionByName(ctx, "del-by-name"); err != nil {
		t.Fatalf("DeleteCollectionByName() error = %v", err)
	}

	_, err := db.GetCollectionByName(ctx, "del-by-name")
	if err != ErrNotFound {
		t.Errorf("after delete, error = %v, want ErrNotFound", err)
	}
}

// --- Collection membership tests ---

func createTestDoc(t *testing.T, db *DB, id, path string) *Document {
	t.Helper()
	now := time.Now().UTC()
	doc := &Document{
		ID: id, Source: SourceMarkdown, Path: path,
		Title: id, ContentHash: "h", IndexedAt: now, ModifiedAt: now,
	}
	if err := db.InsertDocument(context.Background(), doc); err != nil {
		t.Fatalf("InsertDocument() error = %v", err)
	}
	return doc
}

func TestAddToCollection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "col1"}
	db.CreateCollection(ctx, col)
	doc := createTestDoc(t, db, "d1", "/d1.md")

	if err := db.AddToCollection(ctx, col.ID, doc.ID); err != nil {
		t.Fatalf("AddToCollection() error = %v", err)
	}

	count, _ := db.CountCollectionDocuments(ctx, col.ID)
	if count != 1 {
		t.Errorf("CountCollectionDocuments() = %d, want 1", count)
	}
}

func TestAddToCollectionIdempotent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "col1"}
	db.CreateCollection(ctx, col)
	doc := createTestDoc(t, db, "d1", "/d1.md")

	db.AddToCollection(ctx, col.ID, doc.ID)
	// Second add should not error
	if err := db.AddToCollection(ctx, col.ID, doc.ID); err != nil {
		t.Fatalf("AddToCollection() idempotent error = %v", err)
	}

	count, _ := db.CountCollectionDocuments(ctx, col.ID)
	if count != 1 {
		t.Errorf("after duplicate add, count = %d, want 1", count)
	}
}

func TestRemoveFromCollection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "col1"}
	db.CreateCollection(ctx, col)
	doc := createTestDoc(t, db, "d1", "/d1.md")
	db.AddToCollection(ctx, col.ID, doc.ID)

	if err := db.RemoveFromCollection(ctx, col.ID, doc.ID); err != nil {
		t.Fatalf("RemoveFromCollection() error = %v", err)
	}

	count, _ := db.CountCollectionDocuments(ctx, col.ID)
	if count != 0 {
		t.Errorf("after remove, count = %d, want 0", count)
	}
}

func TestRemoveFromCollectionNotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "col1"}
	db.CreateCollection(ctx, col)

	err := db.RemoveFromCollection(ctx, col.ID, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("RemoveFromCollection() error = %v, want ErrNotFound", err)
	}
}

func TestGetCollectionDocuments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "col1"}
	db.CreateCollection(ctx, col)
	d1 := createTestDoc(t, db, "d1", "/d1.md")
	d2 := createTestDoc(t, db, "d2", "/d2.md")
	db.AddToCollection(ctx, col.ID, d1.ID)
	db.AddToCollection(ctx, col.ID, d2.ID)

	docs, err := db.GetCollectionDocuments(ctx, col.ID)
	if err != nil {
		t.Fatalf("GetCollectionDocuments() error = %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("GetCollectionDocuments() returned %d, want 2", len(docs))
	}
}

func TestGetCollectionDocumentsEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "empty-col"}
	db.CreateCollection(ctx, col)

	docs, err := db.GetCollectionDocuments(ctx, col.ID)
	if err != nil {
		t.Fatalf("GetCollectionDocuments() error = %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("GetCollectionDocuments() returned %d, want 0", len(docs))
	}
}

func TestCountCollectionDocuments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "col1"}
	db.CreateCollection(ctx, col)

	count, _ := db.CountCollectionDocuments(ctx, col.ID)
	if count != 0 {
		t.Errorf("initially count = %d, want 0", count)
	}

	for i := 0; i < 3; i++ {
		doc := createTestDoc(t, db, fmt.Sprintf("d%d", i), fmt.Sprintf("/d%d.md", i))
		db.AddToCollection(ctx, col.ID, doc.ID)
	}

	count, _ = db.CountCollectionDocuments(ctx, col.ID)
	if count != 3 {
		t.Errorf("after 3 adds, count = %d, want 3", count)
	}
}

func TestGetDocumentCollections(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	c1 := &Collection{Name: "alpha"}
	c2 := &Collection{Name: "beta"}
	db.CreateCollection(ctx, c1)
	db.CreateCollection(ctx, c2)
	doc := createTestDoc(t, db, "d1", "/d1.md")

	db.AddToCollection(ctx, c1.ID, doc.ID)
	db.AddToCollection(ctx, c2.ID, doc.ID)

	cols, err := db.GetDocumentCollections(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetDocumentCollections() error = %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("GetDocumentCollections() returned %d, want 2", len(cols))
	}
	if cols[0].Name != "alpha" || cols[1].Name != "beta" {
		t.Errorf("collections = [%s %s], want [alpha beta]", cols[0].Name, cols[1].Name)
	}
}

func TestDeleteCollectionCascade(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "cascade"}
	db.CreateCollection(ctx, col)
	doc := createTestDoc(t, db, "d1", "/d1.md")
	db.AddToCollection(ctx, col.ID, doc.ID)

	db.DeleteCollection(ctx, col.ID)

	// Memberships should be gone
	cols, _ := db.GetDocumentCollections(ctx, doc.ID)
	if len(cols) != 0 {
		t.Errorf("after cascade delete, document still in %d collections", len(cols))
	}
}

func TestDocumentDeleteCascade(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	col := &Collection{Name: "col1"}
	db.CreateCollection(ctx, col)
	doc := createTestDoc(t, db, "d1", "/d1.md")
	db.AddToCollection(ctx, col.ID, doc.ID)

	db.DeleteDocument(ctx, doc.ID)

	count, _ := db.CountCollectionDocuments(ctx, col.ID)
	if count != 0 {
		t.Errorf("after document delete, collection count = %d, want 0", count)
	}
}
