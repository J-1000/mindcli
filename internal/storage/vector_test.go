package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVectorStoreAddAndSearch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-vector-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Add some vectors.
	store.Add("doc1:0", []float32{1.0, 0.0, 0.0})
	store.Add("doc1:1", []float32{0.9, 0.1, 0.0})
	store.Add("doc2:0", []float32{0.0, 1.0, 0.0})
	store.Add("doc3:0", []float32{0.0, 0.0, 1.0})

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
	tmpDir, err := os.MkdirTemp("", "mindcli-vector-persist-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "persist.graph")

	// Create and populate store.
	store, err := NewVectorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.Add("key1", []float32{1.0, 0.0, 0.0})
	store.Add("key2", []float32{0.0, 1.0, 0.0})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	// Reload store from disk.
	store2, err := NewVectorStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

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
	tmpDir, err := os.MkdirTemp("", "mindcli-vector-delete-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.Add("key1", []float32{1.0, 0.0})
	store.Add("key2", []float32{0.0, 1.0})

	if store.Len() != 2 {
		t.Fatalf("expected 2, got %d", store.Len())
	}

	store.Delete("key1")

	if store.Len() != 1 {
		t.Errorf("expected 1 after delete, got %d", store.Len())
	}
}

func TestVectorStoreAddBatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-vector-batch-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	keys := []string{"a", "b", "c"}
	vecs := [][]float32{
		{1.0, 0.0, 0.0},
		{0.0, 1.0, 0.0},
		{0.0, 0.0, 1.0},
	}
	store.AddBatch(keys, vecs)

	if store.Len() != 3 {
		t.Errorf("expected 3 after batch add, got %d", store.Len())
	}
}

func TestVectorStoreEmptySearch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-vector-empty-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := NewVectorStore(filepath.Join(tmpDir, "test.graph"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	results := store.Search([]float32{1.0, 0.0}, 5)
	if results != nil {
		t.Errorf("expected nil results for empty store, got %d", len(results))
	}
}
