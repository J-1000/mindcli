package embeddings

import (
	"testing"
)

func TestNewOllamaEmbedder(t *testing.T) {
	e := NewOllamaEmbedder("http://localhost:11434", "nomic-embed-text")

	if e.baseURL != "http://localhost:11434" {
		t.Errorf("expected baseURL http://localhost:11434, got %s", e.baseURL)
	}
	if e.model != "nomic-embed-text" {
		t.Errorf("expected model nomic-embed-text, got %s", e.model)
	}
	if e.client == nil {
		t.Fatal("expected non-nil http client")
	}
	if e.dimensions != 0 {
		t.Errorf("expected initial dimensions 0, got %d", e.dimensions)
	}
}

func TestDimensionsInitiallyZero(t *testing.T) {
	e := NewOllamaEmbedder("http://localhost:11434", "test-model")
	if d := e.Dimensions(); d != 0 {
		t.Errorf("expected Dimensions() == 0 before any embedding, got %d", d)
	}
}
