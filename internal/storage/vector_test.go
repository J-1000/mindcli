package storage

import (
	"path/filepath"
	"testing"
)

func closeTestVectorStore(t *testing.T, store *VectorStore) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Errorf("closing vector store: %v", err)
	}
}

func TestVectorStoreDimMismatch(t *testing.T) {
	store, err := NewVectorStore(filepath.Join(t.TempDir(), "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestVectorStore(t, store)

	if err := store.Add("a", []float32{1, 0, 0}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if store.Dim() != 3 {
		t.Errorf("Dim() = %d, want 3", store.Dim())
	}
	if err := store.Add("b", []float32{1, 0}); err == nil {
		t.Error("expected dimension-mismatch error, got nil")
	}
	if err := store.AddBatch([]string{"c"}, [][]float32{{1, 0, 0, 0}}); err == nil {
		t.Error("expected AddBatch dimension-mismatch error, got nil")
	}
}

func TestVectorStoreMetaPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.graph")

	store, err := NewVectorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.SetModel("nomic-embed-text")
	if err := store.Add("a", []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewVectorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestVectorStore(t, reopened)
	if reopened.Model() != "nomic-embed-text" {
		t.Errorf("Model() = %q, want nomic-embed-text", reopened.Model())
	}
	if reopened.Dim() != 3 {
		t.Errorf("Dim() = %d, want 3", reopened.Dim())
	}
}

func TestVectorStoreAddAndSearch(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestVectorStore(t, store)

	// Add some vectors.
	mustSucceed(t, store.Add("doc1:0", []float32{1.0, 0.0, 0.0}))
	mustSucceed(t, store.Add("doc1:1", []float32{0.9, 0.1, 0.0}))
	mustSucceed(t, store.Add("doc2:0", []float32{0.0, 1.0, 0.0}))
	mustSucceed(t, store.Add("doc3:0", []float32{0.0, 0.0, 1.0}))

	if store.Len() != 4 {
		t.Errorf("expected 4 vectors, got %d", store.Len())
	}

	// Search for something similar to doc1.
	results := store.Search([]float32{0.95, 0.05, 0.0}, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First result should be most similar to the query.
	if results[0].Key != "doc1:1" && results[0].Key != "doc1:0" {
		t.Errorf("expected doc1 chunk as top result, got %s", results[0].Key)
	}

	// Scores should be positive.
	for _, r := range results {
		if r.Score <= 0 {
			t.Errorf("expected positive score, got %f for %s", r.Score, r.Key)
		}
	}
}

func TestVectorStorePersistence(t *testing.T) {
	tmpDir := t.TempDir()

	path := filepath.Join(tmpDir, "persist.graph")

	// Create and populate store.
	store, err := NewVectorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	mustSucceed(t, store.Add("key1", []float32{1.0, 0.0, 0.0}))
	mustSucceed(t, store.Add("key2", []float32{0.0, 1.0, 0.0}))
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// Reload store from disk.
	store2, err := NewVectorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestVectorStore(t, store2)

	if store2.Len() != 2 {
		t.Errorf("expected 2 vectors after reload, got %d", store2.Len())
	}

	// Should still find results after reload.
	results := store2.Search([]float32{0.9, 0.1, 0.0}, 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result after reload, got %d", len(results))
	}
	if results[0].Key != "key1" {
		t.Errorf("expected key1 as top result, got %s", results[0].Key)
	}
}

func TestVectorStoreDelete(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestVectorStore(t, store)

	mustSucceed(t, store.Add("key1", []float32{1.0, 0.0}))
	mustSucceed(t, store.Add("key2", []float32{0.0, 1.0}))

	if store.Len() != 2 {
		t.Fatalf("expected 2, got %d", store.Len())
	}

	store.Delete("key1")

	if store.Len() != 1 {
		t.Errorf("expected 1 after delete, got %d", store.Len())
	}
}

func TestVectorStoreAddBatch(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestVectorStore(t, store)

	keys := []string{"a", "b", "c"}
	vecs := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}
	mustSucceed(t, store.AddBatch(keys, vecs))

	if store.Len() != 3 {
		t.Errorf("expected 3 after batch add, got %d", store.Len())
	}
}

func TestVectorStoreEmptySearch(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestVectorStore(t, store)

	results := store.Search([]float32{1.0, 0.0}, 5)
	if results != nil {
		t.Errorf("expected nil results for empty store, got %d", len(results))
	}
}
