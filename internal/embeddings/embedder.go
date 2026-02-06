// Package embeddings provides text embedding generation for semantic search.
package embeddings

import "context"

// Embedder generates vector embeddings from text.
type Embedder interface {
	// Embed generates an embedding for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch generates embeddings for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the embedding vector dimension.
	Dimensions() int
}
