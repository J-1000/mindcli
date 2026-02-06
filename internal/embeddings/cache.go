package embeddings

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"

	_ "github.com/mattn/go-sqlite3"
)

// CachedEmbedder wraps an Embedder with a content-hash based SQLite cache.
type CachedEmbedder struct {
	inner Embedder
	db    *sql.DB
}

// NewCachedEmbedder creates a cached wrapper around an embedder.
// The cachePath should point to a SQLite database file.
func NewCachedEmbedder(inner Embedder, cachePath string) (*CachedEmbedder, error) {
	db, err := sql.Open("sqlite3", cachePath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening cache db: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS embedding_cache (
			content_hash TEXT PRIMARY KEY,
			embedding BLOB NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating cache table: %w", err)
	}

	return &CachedEmbedder{inner: inner, db: db}, nil
}

// Embed generates or retrieves a cached embedding for text.
func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	hash := contentHash(text)

	// Check cache first.
	if emb, err := c.get(hash); err == nil {
		return emb, nil
	}

	// Generate embedding.
	emb, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	// Store in cache.
	c.put(hash, emb)
	return emb, nil
}

// EmbedBatch generates embeddings, using cache where possible.
func (c *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	var uncachedTexts []string
	var uncachedIndices []int

	// Check cache for each text.
	for i, text := range texts {
		hash := contentHash(text)
		if emb, err := c.get(hash); err == nil {
			results[i] = emb
		} else {
			uncachedTexts = append(uncachedTexts, text)
			uncachedIndices = append(uncachedIndices, i)
		}
	}

	// Generate embeddings for uncached texts.
	if len(uncachedTexts) > 0 {
		embeddings, err := c.inner.EmbedBatch(ctx, uncachedTexts)
		if err != nil {
			return nil, err
		}

		for j, emb := range embeddings {
			idx := uncachedIndices[j]
			results[idx] = emb
			c.put(contentHash(uncachedTexts[j]), emb)
		}
	}

	return results, nil
}

// Dimensions returns the embedding vector dimension.
func (c *CachedEmbedder) Dimensions() int {
	return c.inner.Dimensions()
}

// Close closes the cache database.
func (c *CachedEmbedder) Close() error {
	return c.db.Close()
}

func contentHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h[:16])
}

func (c *CachedEmbedder) get(hash string) ([]float32, error) {
	var blob []byte
	err := c.db.QueryRow("SELECT embedding FROM embedding_cache WHERE content_hash = ?", hash).Scan(&blob)
	if err != nil {
		return nil, err
	}
	return decodeEmbedding(blob), nil
}

func (c *CachedEmbedder) put(hash string, embedding []float32) {
	blob := encodeEmbedding(embedding)
	c.db.Exec("INSERT OR REPLACE INTO embedding_cache (content_hash, embedding) VALUES (?, ?)", hash, blob)
}

// encodeEmbedding converts float32 slice to a compact binary representation.
func encodeEmbedding(emb []float32) []byte {
	buf := make([]byte, len(emb)*4)
	for i, v := range emb {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// decodeEmbedding converts binary representation back to float32 slice.
func decodeEmbedding(buf []byte) []float32 {
	emb := make([]float32, len(buf)/4)
	for i := range emb {
		emb[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return emb
}
