package query

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
)

// keywordEmbedder is a deterministic 2-D embedder: text mentioning "go" maps to
// [1,0], everything else to [0,1]. This lets vector search be exercised without
// a real embedding backend.
type keywordEmbedder struct{}

func (keywordEmbedder) vec(text string) []float32 {
	if containsFold(text, "go") {
		return []float32{1, 0}
	}
	return []float32{0, 1}
}

func (e keywordEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return e.vec(text), nil
}

func (e keywordEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.vec(t)
	}
	return out, nil
}

func (keywordEmbedder) Dimensions() int { return 2 }

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			c := s[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func newHybridTestStores(t *testing.T) (*storage.DB, *search.BleveIndex, *storage.VectorStore) {
	t.Helper()
	dir := t.TempDir()

	db, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	bleve, err := search.NewBleveIndex(filepath.Join(dir, "test.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bleve.Close() })

	vectors, err := storage.NewVectorStore(filepath.Join(dir, "vectors.graph"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { vectors.Close() })

	ctx := context.Background()
	now := time.Now()
	docs := []*storage.Document{
		{ID: "doc1", Source: storage.SourceMarkdown, Path: "/a.md", Title: "Go notes",
			Content: "go programming concurrency", ContentHash: "h1", IndexedAt: now, ModifiedAt: now},
		{ID: "doc2", Source: storage.SourceMarkdown, Path: "/b.md", Title: "Rust notes",
			Content: "rust language ownership", ContentHash: "h2", IndexedAt: now, ModifiedAt: now},
	}
	for _, d := range docs {
		if err := db.UpsertDocument(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := bleve.Index(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := vectors.AddBatch([]string{"doc1:0", "doc2:0"},
		[][]float32{{1, 0}, {0, 1}}); err != nil {
		t.Fatal(err)
	}
	return db, bleve, vectors
}

func TestHybridSearch_RanksRelevantDocFirst(t *testing.T) {
	db, bleve, vectors := newHybridTestStores(t)
	h := NewHybridSearcher(bleve, vectors, keywordEmbedder{}, db, 0.5)

	ctx := context.Background()
	var results storage.SearchResults
	// Bleve indexing settles asynchronously; poll briefly.
	for i := 0; i < 30; i++ {
		results, _ = h.Search(ctx, "go", 10)
		if len(results) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(results) == 0 {
		t.Fatal("no results from hybrid search")
	}
	if results[0].Document.ID != "doc1" {
		t.Errorf("top result = %s, want doc1 (the Go document)", results[0].Document.ID)
	}
}

func TestHybridSearch_FallsBackToBM25WhenNoVectors(t *testing.T) {
	db, bleve, _ := newHybridTestStores(t)
	// nil vectors/embedder => BM25-only path.
	h := NewHybridSearcher(bleve, nil, nil, db, 0.5)

	ctx := context.Background()
	var results storage.SearchResults
	for i := 0; i < 30; i++ {
		results, _ = h.Search(ctx, "rust", 10)
		if len(results) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(results) == 0 {
		t.Fatal("expected BM25 results for 'rust'")
	}
	if results[0].Document.ID != "doc2" {
		t.Errorf("top result = %s, want doc2", results[0].Document.ID)
	}
}
