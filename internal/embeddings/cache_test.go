package embeddings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// mockEmbedder is a test double that counts calls.
type mockEmbedder struct {
	calls      int
	batchCalls int
	dim        int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	m.calls++
	emb := make([]float32, m.dim)
	for i := range emb {
		emb[i] = float32(i) * 0.01
	}
	return emb, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	m.batchCalls++
	results := make([][]float32, len(texts))
	for i := range texts {
		emb := make([]float32, m.dim)
		for j := range emb {
			emb[j] = float32(i*100+j) * 0.01
		}
		results[i] = emb
	}
	return results, nil
}

func (m *mockEmbedder) Dimensions() int { return m.dim }

func TestCachedEmbedder(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-cache-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	mock := &mockEmbedder{dim: 128}
	cache, err := NewCachedEmbedder(mock, filepath.Join(tmpDir, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	ctx := context.Background()

	// First call should hit the inner embedder.
	emb1, err := cache.Embed(ctx, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if mock.calls != 1 {
		t.Errorf("expected 1 inner call, got %d", mock.calls)
	}
	if len(emb1) != 128 {
		t.Errorf("expected 128 dimensions, got %d", len(emb1))
	}

	// Second call with same text should use cache.
	emb2, err := cache.Embed(ctx, "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if mock.calls != 1 {
		t.Errorf("expected still 1 inner call, got %d", mock.calls)
	}

	// Values should match.
	for i := range emb1 {
		if emb1[i] != emb2[i] {
			t.Errorf("cached embedding differs at index %d: %f != %f", i, emb1[i], emb2[i])
			break
		}
	}

	// Different text should hit the inner embedder.
	_, err = cache.Embed(ctx, "different text")
	if err != nil {
		t.Fatal(err)
	}
	if mock.calls != 2 {
		t.Errorf("expected 2 inner calls, got %d", mock.calls)
	}
}

func TestCachedEmbedderBatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "mindcli-cache-batch-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	mock := &mockEmbedder{dim: 64}
	cache, err := NewCachedEmbedder(mock, filepath.Join(tmpDir, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()

	ctx := context.Background()

	// First batch: all uncached.
	texts := []string{"alpha", "beta", "gamma"}
	results, err := cache.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if mock.batchCalls != 1 {
		t.Errorf("expected 1 batch call, got %d", mock.batchCalls)
	}

	// Second batch: mix of cached and uncached.
	texts2 := []string{"alpha", "delta", "gamma"}
	results2, err := cache.EmbedBatch(ctx, texts2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results2) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results2))
	}
	if mock.batchCalls != 2 {
		t.Errorf("expected 2 batch calls (only for uncached), got %d", mock.batchCalls)
	}
}

func TestEncodeDecodeEmbedding(t *testing.T) {
	original := []float32{1.0, -0.5, 0.123, 3.14159, 0.0}
	encoded := encodeEmbedding(original)
	decoded := decodeEmbedding(encoded)

	if len(decoded) != len(original) {
		t.Fatalf("expected %d floats, got %d", len(original), len(decoded))
	}
	for i := range original {
		if original[i] != decoded[i] {
			t.Errorf("mismatch at %d: %f != %f", i, original[i], decoded[i])
		}
	}
}
