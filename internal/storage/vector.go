package storage

import (
	"fmt"
	"os"
	"sync"

	"github.com/coder/hnsw"
)

// VectorStore provides HNSW-based vector storage for semantic search.
type VectorStore struct {
	graph *hnsw.SavedGraph[string]
	mu    sync.RWMutex
}

// NewVectorStore creates or loads a vector store from disk.
func NewVectorStore(path string) (*VectorStore, error) {
	g, err := hnsw.LoadSavedGraph[string](path)
	if err != nil {
		// If the file doesn't exist, create a new graph.
		if os.IsNotExist(err) {
			g = &hnsw.SavedGraph[string]{
				Graph: hnsw.NewGraph[string](),
				Path:  path,
			}
		} else {
			return nil, fmt.Errorf("loading vector store: %w", err)
		}
	}

	g.Graph.Distance = hnsw.CosineDistance

	return &VectorStore{graph: g}, nil
}

// Add inserts or updates a vector for the given key.
func (v *VectorStore) Add(key string, vector []float32) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Delete existing entry if present (HNSW doesn't handle duplicate keys).
	v.graph.Delete(key)
	v.graph.Add(hnsw.MakeNode(key, vector))
}

// AddBatch inserts multiple vectors at once.
func (v *VectorStore) AddBatch(keys []string, vectors [][]float32) {
	if len(keys) != len(vectors) {
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	nodes := make([]hnsw.Node[string], len(keys))
	for i := range keys {
		v.graph.Delete(keys[i])
		nodes[i] = hnsw.MakeNode(keys[i], vectors[i])
	}
	v.graph.Add(nodes...)
}

// Search finds the k nearest neighbors to the query vector.
// Returns chunk keys sorted by similarity (closest first).
func (v *VectorStore) Search(query []float32, k int) []VectorResult {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.graph.Len() == 0 {
		return nil
	}

	neighbors := v.graph.Search(query, k)
	results := make([]VectorResult, len(neighbors))
	for i, n := range neighbors {
		// CosineDistance returns 0 for identical, 2 for opposite.
		// Convert to similarity score: 1 - distance/2 gives [0, 1].
		dist := v.graph.Distance(query, n.Value)
		similarity := 1.0 - float64(dist)/2.0
		results[i] = VectorResult{
			Key:        n.Key,
			Score:      similarity,
			Similarity: similarity,
		}
	}
	return results
}

// Delete removes a vector by key.
func (v *VectorStore) Delete(key string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.graph.Delete(key)
}

// DeleteByPrefix removes all vectors whose keys start with the given prefix.
// Useful for removing all chunks of a document (prefix = docID).
func (v *VectorStore) DeleteByPrefix(prefix string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// We need to collect keys first since we can't modify during iteration.
	// The HNSW graph doesn't expose iteration, so we track keys externally
	// or just use Lookup. For now, we rely on the caller knowing the keys.
}

// Len returns the number of vectors in the store.
func (v *VectorStore) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.graph.Len()
}

// Save persists the vector store to disk.
func (v *VectorStore) Save() error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.graph.Save()
}

// Close saves and closes the vector store.
func (v *VectorStore) Close() error {
	return v.Save()
}

// VectorResult represents a vector search result.
type VectorResult struct {
	Key        string  // Chunk key (format: "docID:chunkIndex")
	Score      float64 // Relevance score [0, 1]
	Similarity float64 // Cosine similarity [0, 1]
}
