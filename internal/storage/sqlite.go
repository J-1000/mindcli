package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// ErrNotFound is returned when a document is not found.
var ErrNotFound = errors.New("document not found")

// ErrCollectionExists is returned when a collection name already exists.
var ErrCollectionExists = errors.New("collection already exists")

// DB wraps a SQLite database connection.
type DB struct {
	db *sql.DB
}

// Open opens a SQLite database at the given path.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Set connection pool settings
	db.SetMaxOpenConns(1) // SQLite doesn't support multiple writers
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &DB{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return store, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}

// migrate runs database migrations.
func (d *DB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			path TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL DEFAULT '',
			preview TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '{}',
			content_hash TEXT NOT NULL,
			indexed_at DATETIME NOT NULL,
			modified_at DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_source ON documents(source)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_path ON documents(path)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_content_hash ON documents(content_hash)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id TEXT PRIMARY KEY,
			document_id TEXT NOT NULL,
			content TEXT NOT NULL,
			start_pos INTEGER NOT NULL,
			end_pos INTEGER NOT NULL,
			FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_document_id ON chunks(document_id)`,
		`CREATE TABLE IF NOT EXISTS document_tags (
			document_id TEXT NOT NULL,
			tag TEXT NOT NULL,
			manual BOOLEAN NOT NULL DEFAULT 1,
			PRIMARY KEY (document_id, tag),
			FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_document_tags_tag ON document_tags(tag)`,
		`CREATE TABLE IF NOT EXISTS collections (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			query TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS collection_documents (
			collection_id TEXT NOT NULL,
			document_id TEXT NOT NULL,
			added_at DATETIME NOT NULL,
			PRIMARY KEY (collection_id, document_id),
			FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE,
			FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_collection_documents_doc ON collection_documents(document_id)`,
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		)`,
		`INSERT OR IGNORE INTO schema_version (version) VALUES (1)`,
	}

	for _, m := range migrations {
		if _, err := d.db.Exec(m); err != nil {
			return fmt.Errorf("executing migration: %w", err)
		}
	}

	return nil
}

// InsertDocument inserts a new document into the database.
func (d *DB) InsertDocument(ctx context.Context, doc *Document) error {
	query := `
		INSERT INTO documents (id, source, path, title, content, preview, metadata, content_hash, indexed_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := d.db.ExecContext(ctx, query,
		doc.ID,
		doc.Source,
		doc.Path,
		doc.Title,
		doc.Content,
		doc.Preview,
		doc.MetadataJSON(),
		doc.ContentHash,
		doc.IndexedAt.UTC(),
		doc.ModifiedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("inserting document: %w", err)
	}
	return nil
}

// UpdateDocument updates an existing document.
func (d *DB) UpdateDocument(ctx context.Context, doc *Document) error {
	query := `
		UPDATE documents
		SET source = ?, path = ?, title = ?, content = ?, preview = ?,
			metadata = ?, content_hash = ?, indexed_at = ?, modified_at = ?
		WHERE id = ?
	`
	result, err := d.db.ExecContext(ctx, query,
		doc.Source,
		doc.Path,
		doc.Title,
		doc.Content,
		doc.Preview,
		doc.MetadataJSON(),
		doc.ContentHash,
		doc.IndexedAt.UTC(),
		doc.ModifiedAt.UTC(),
		doc.ID,
	)
	if err != nil {
		return fmt.Errorf("updating document: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertDocument inserts or updates a document.
func (d *DB) UpsertDocument(ctx context.Context, doc *Document) error {
	query := `
		INSERT INTO documents (id, source, path, title, content, preview, metadata, content_hash, indexed_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			path = excluded.path,
			title = excluded.title,
			content = excluded.content,
			preview = excluded.preview,
			metadata = excluded.metadata,
			content_hash = excluded.content_hash,
			indexed_at = excluded.indexed_at,
			modified_at = excluded.modified_at
	`
	_, err := d.db.ExecContext(ctx, query,
		doc.ID,
		doc.Source,
		doc.Path,
		doc.Title,
		doc.Content,
		doc.Preview,
		doc.MetadataJSON(),
		doc.ContentHash,
		doc.IndexedAt.UTC(),
		doc.ModifiedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("upserting document: %w", err)
	}
	return nil
}

// GetDocument retrieves a document by ID.
func (d *DB) GetDocument(ctx context.Context, id string) (*Document, error) {
	query := `
		SELECT id, source, path, title, content, preview, metadata, content_hash, indexed_at, modified_at
		FROM documents WHERE id = ?
	`
	row := d.db.QueryRowContext(ctx, query, id)
	return d.scanDocument(row)
}

// GetDocumentByPath retrieves a document by its path.
func (d *DB) GetDocumentByPath(ctx context.Context, path string) (*Document, error) {
	query := `
		SELECT id, source, path, title, content, preview, metadata, content_hash, indexed_at, modified_at
		FROM documents WHERE path = ?
	`
	row := d.db.QueryRowContext(ctx, query, path)
	return d.scanDocument(row)
}

// DeleteDocument deletes a document by ID.
func (d *DB) DeleteDocument(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM documents WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting document: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDocumentByPath deletes a document by its path.
func (d *DB) DeleteDocumentByPath(ctx context.Context, path string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM documents WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("deleting document: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// ListDocuments returns all documents, optionally filtered by source.
func (d *DB) ListDocuments(ctx context.Context, source Source) ([]*Document, error) {
	var query string
	var args []interface{}

	if source == "" {
		query = `
			SELECT id, source, path, title, content, preview, metadata, content_hash, indexed_at, modified_at
			FROM documents ORDER BY modified_at DESC
		`
	} else {
		query = `
			SELECT id, source, path, title, content, preview, metadata, content_hash, indexed_at, modified_at
			FROM documents WHERE source = ? ORDER BY modified_at DESC
		`
		args = append(args, source)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying documents: %w", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		doc, err := d.scanDocumentRows(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating documents: %w", err)
	}

	return docs, nil
}

// CountDocuments returns the total number of documents.
func (d *DB) CountDocuments(ctx context.Context) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting documents: %w", err)
	}
	return count, nil
}

// CountDocumentsBySource returns the number of documents by source.
func (d *DB) CountDocumentsBySource(ctx context.Context, source Source) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM documents WHERE source = ?", source).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting documents: %w", err)
	}
	return count, nil
}

// SearchDocuments performs a simple text search on title and content.
func (d *DB) SearchDocuments(ctx context.Context, query string, limit int) ([]*Document, error) {
	sqlQuery := `
		SELECT id, source, path, title, content, preview, metadata, content_hash, indexed_at, modified_at
		FROM documents
		WHERE title LIKE ? OR content LIKE ?
		ORDER BY modified_at DESC
		LIMIT ?
	`
	pattern := "%" + query + "%"
	rows, err := d.db.QueryContext(ctx, sqlQuery, pattern, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("searching documents: %w", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		doc, err := d.scanDocumentRows(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating search results: %w", err)
	}

	return docs, nil
}

// InsertChunk inserts a chunk into the database.
func (d *DB) InsertChunk(ctx context.Context, chunk *Chunk) error {
	query := `INSERT INTO chunks (id, document_id, content, start_pos, end_pos) VALUES (?, ?, ?, ?, ?)`
	_, err := d.db.ExecContext(ctx, query, chunk.ID, chunk.DocumentID, chunk.Content, chunk.StartPos, chunk.EndPos)
	if err != nil {
		return fmt.Errorf("inserting chunk: %w", err)
	}
	return nil
}

// GetChunksByDocument retrieves all chunks for a document.
func (d *DB) GetChunksByDocument(ctx context.Context, documentID string) ([]*Chunk, error) {
	query := `SELECT id, document_id, content, start_pos, end_pos FROM chunks WHERE document_id = ? ORDER BY start_pos`
	rows, err := d.db.QueryContext(ctx, query, documentID)
	if err != nil {
		return nil, fmt.Errorf("querying chunks: %w", err)
	}
	defer rows.Close()

	var chunks []*Chunk
	for rows.Next() {
		var chunk Chunk
		if err := rows.Scan(&chunk.ID, &chunk.DocumentID, &chunk.Content, &chunk.StartPos, &chunk.EndPos); err != nil {
			return nil, fmt.Errorf("scanning chunk: %w", err)
		}
		chunks = append(chunks, &chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating chunks: %w", err)
	}

	return chunks, nil
}

// DeleteChunksByDocument deletes all chunks for a document.
func (d *DB) DeleteChunksByDocument(ctx context.Context, documentID string) error {
	_, err := d.db.ExecContext(ctx, "DELETE FROM chunks WHERE document_id = ?", documentID)
	if err != nil {
		return fmt.Errorf("deleting chunks: %w", err)
	}
	return nil
}

// scanDocument scans a single row into a Document.
func (d *DB) scanDocument(row *sql.Row) (*Document, error) {
	var doc Document
	var metadataJSON string
	var indexedAt, modifiedAt time.Time

	err := row.Scan(
		&doc.ID,
		&doc.Source,
		&doc.Path,
		&doc.Title,
		&doc.Content,
		&doc.Preview,
		&metadataJSON,
		&doc.ContentHash,
		&indexedAt,
		&modifiedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning document: %w", err)
	}

	doc.IndexedAt = indexedAt
	doc.ModifiedAt = modifiedAt
	if err := doc.SetMetadataFromJSON(metadataJSON); err != nil {
		return nil, fmt.Errorf("parsing metadata: %w", err)
	}

	return &doc, nil
}

// scanDocumentRows scans a row from Rows into a Document.
func (d *DB) scanDocumentRows(rows *sql.Rows) (*Document, error) {
	var doc Document
	var metadataJSON string
	var indexedAt, modifiedAt time.Time

	err := rows.Scan(
		&doc.ID,
		&doc.Source,
		&doc.Path,
		&doc.Title,
		&doc.Content,
		&doc.Preview,
		&metadataJSON,
		&doc.ContentHash,
		&indexedAt,
		&modifiedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning document: %w", err)
	}

	doc.IndexedAt = indexedAt
	doc.ModifiedAt = modifiedAt
	if err := doc.SetMetadataFromJSON(metadataJSON); err != nil {
		return nil, fmt.Errorf("parsing metadata: %w", err)
	}

	return &doc, nil
}

// AddTag adds a manual tag to a document.
func (d *DB) AddTag(ctx context.Context, docID, tag string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO document_tags (document_id, tag, manual) VALUES (?, ?, 1)`,
		docID, tag,
	)
	if err != nil {
		return fmt.Errorf("adding tag: %w", err)
	}
	return nil
}

// AddAutoTag adds an auto-extracted tag to a document.
func (d *DB) AddAutoTag(ctx context.Context, docID, tag string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO document_tags (document_id, tag, manual) VALUES (?, ?, 0)`,
		docID, tag,
	)
	if err != nil {
		return fmt.Errorf("adding auto tag: %w", err)
	}
	return nil
}

// RemoveTag removes a manual tag from a document.
func (d *DB) RemoveTag(ctx context.Context, docID, tag string) error {
	result, err := d.db.ExecContext(ctx,
		`DELETE FROM document_tags WHERE document_id = ? AND tag = ? AND manual = 1`,
		docID, tag,
	)
	if err != nil {
		return fmt.Errorf("removing tag: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// GetTags returns all tags for a document (both manual and auto-extracted).
func (d *DB) GetTags(ctx context.Context, docID string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT tag FROM document_tags WHERE document_id = ? ORDER BY tag`,
		docID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying tags: %w", err)
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// ListAllTags returns all unique tags across all documents.
func (d *DB) ListAllTags(ctx context.Context) ([]string, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT DISTINCT tag FROM document_tags ORDER BY tag`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying all tags: %w", err)
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

// FindByTag returns all documents with a given tag.
func (d *DB) FindByTag(ctx context.Context, tag string) ([]*Document, error) {
	sqlQuery := `
		SELECT d.id, d.source, d.path, d.title, d.content, d.preview, d.metadata, d.content_hash, d.indexed_at, d.modified_at
		FROM documents d
		INNER JOIN document_tags dt ON d.id = dt.document_id
		WHERE dt.tag = ?
		ORDER BY d.modified_at DESC
	`
	rows, err := d.db.QueryContext(ctx, sqlQuery, tag)
	if err != nil {
		return nil, fmt.Errorf("finding by tag: %w", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		doc, err := d.scanDocumentRows(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// generateID generates a random 16-byte hex ID.
func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// scanCollection scans a single row into a Collection.
func (d *DB) scanCollection(row *sql.Row) (*Collection, error) {
	var c Collection
	var createdAt time.Time
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.Query, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanning collection: %w", err)
	}
	c.CreatedAt = createdAt
	return &c, nil
}

// CreateCollection creates a new collection.
func (d *DB) CreateCollection(ctx context.Context, c *Collection) error {
	if c.ID == "" {
		c.ID = generateID()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO collections (id, name, description, query, created_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Description, c.Query, c.CreatedAt.UTC(),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrCollectionExists
		}
		return fmt.Errorf("creating collection: %w", err)
	}
	return nil
}

// GetCollection retrieves a collection by ID.
func (d *DB) GetCollection(ctx context.Context, id string) (*Collection, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, name, description, query, created_at FROM collections WHERE id = ?`, id,
	)
	return d.scanCollection(row)
}

// GetCollectionByName retrieves a collection by name.
func (d *DB) GetCollectionByName(ctx context.Context, name string) (*Collection, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, name, description, query, created_at FROM collections WHERE name = ?`, name,
	)
	return d.scanCollection(row)
}

// ListCollections returns all collections ordered by name.
func (d *DB) ListCollections(ctx context.Context) ([]*Collection, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, name, description, query, created_at FROM collections ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing collections: %w", err)
	}
	defer rows.Close()

	var collections []*Collection
	for rows.Next() {
		var c Collection
		var createdAt time.Time
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Query, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning collection: %w", err)
		}
		c.CreatedAt = createdAt
		collections = append(collections, &c)
	}
	return collections, rows.Err()
}

// RenameCollection renames a collection.
func (d *DB) RenameCollection(ctx context.Context, id, newName string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE collections SET name = ? WHERE id = ?`, newName, id,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrCollectionExists
		}
		return fmt.Errorf("renaming collection: %w", err)
	}
	return nil
}

// UpdateCollectionDescription updates a collection's description.
func (d *DB) UpdateCollectionDescription(ctx context.Context, id, desc string) error {
	result, err := d.db.ExecContext(ctx,
		`UPDATE collections SET description = ? WHERE id = ?`, desc, id,
	)
	if err != nil {
		return fmt.Errorf("updating collection description: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteCollection deletes a collection by ID.
func (d *DB) DeleteCollection(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM collections WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting collection: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// AddToCollection adds a document to a collection (idempotent).
func (d *DB) AddToCollection(ctx context.Context, collectionID, documentID string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO collection_documents (collection_id, document_id, added_at) VALUES (?, ?, ?)`,
		collectionID, documentID, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("adding to collection: %w", err)
	}
	return nil
}

// RemoveFromCollection removes a document from a collection.
func (d *DB) RemoveFromCollection(ctx context.Context, collectionID, documentID string) error {
	result, err := d.db.ExecContext(ctx,
		`DELETE FROM collection_documents WHERE collection_id = ? AND document_id = ?`,
		collectionID, documentID,
	)
	if err != nil {
		return fmt.Errorf("removing from collection: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// GetCollectionDocuments returns all documents in a collection.
func (d *DB) GetCollectionDocuments(ctx context.Context, collectionID string) ([]*Document, error) {
	sqlQuery := `
		SELECT d.id, d.source, d.path, d.title, d.content, d.preview, d.metadata, d.content_hash, d.indexed_at, d.modified_at
		FROM documents d
		INNER JOIN collection_documents cd ON d.id = cd.document_id
		WHERE cd.collection_id = ?
		ORDER BY cd.added_at DESC
	`
	rows, err := d.db.QueryContext(ctx, sqlQuery, collectionID)
	if err != nil {
		return nil, fmt.Errorf("getting collection documents: %w", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		doc, err := d.scanDocumentRows(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// CountCollectionDocuments returns the number of documents in a collection.
func (d *DB) CountCollectionDocuments(ctx context.Context, collectionID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM collection_documents WHERE collection_id = ?`, collectionID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting collection documents: %w", err)
	}
	return count, nil
}

// GetDocumentCollections returns all collections a document belongs to.
func (d *DB) GetDocumentCollections(ctx context.Context, documentID string) ([]*Collection, error) {
	sqlQuery := `
		SELECT c.id, c.name, c.description, c.query, c.created_at
		FROM collections c
		INNER JOIN collection_documents cd ON c.id = cd.collection_id
		WHERE cd.document_id = ?
		ORDER BY c.name
	`
	rows, err := d.db.QueryContext(ctx, sqlQuery, documentID)
	if err != nil {
		return nil, fmt.Errorf("getting document collections: %w", err)
	}
	defer rows.Close()

	var collections []*Collection
	for rows.Next() {
		var c Collection
		var createdAt time.Time
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Query, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning collection: %w", err)
		}
		c.CreatedAt = createdAt
		collections = append(collections, &c)
	}
	return collections, rows.Err()
}

// DeleteCollectionByName deletes a collection by name.
func (d *DB) DeleteCollectionByName(ctx context.Context, name string) error {
	result, err := d.db.ExecContext(ctx, "DELETE FROM collections WHERE name = ?", name)
	if err != nil {
		return fmt.Errorf("deleting collection: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}
