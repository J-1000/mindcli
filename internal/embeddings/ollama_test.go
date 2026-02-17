package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeOllamaServer creates an httptest server that mimics the Ollama /api/embed endpoint.
// The handler function receives the decoded request and returns the response to send.
func fakeOllamaServer(t *testing.T, handler func(req ollamaEmbedRequest) (int, any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req ollamaEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		status, resp := handler(req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(resp)
	}))
}

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

func TestEmbedSuccess(t *testing.T) {
	srv := fakeOllamaServer(t, func(req ollamaEmbedRequest) (int, any) {
		if req.Model != "test-model" {
			t.Errorf("expected model test-model, got %s", req.Model)
		}
		return http.StatusOK, ollamaEmbedResponse{
			Model:      req.Model,
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		}
	})
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	emb, err := e.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(emb) != 3 {
		t.Fatalf("expected 3 dimensions, got %d", len(emb))
	}
	if emb[0] != 0.1 || emb[1] != 0.2 || emb[2] != 0.3 {
		t.Errorf("unexpected embedding values: %v", emb)
	}
	if d := e.Dimensions(); d != 3 {
		t.Errorf("expected Dimensions() == 3 after embed, got %d", d)
	}
}

func TestEmbedBatchEmpty(t *testing.T) {
	e := NewOllamaEmbedder("http://localhost:11434", "test-model")
	results, err := e.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty input, got %v", results)
	}

	results, err = e.EmbedBatch(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty slice, got %v", results)
	}
}

func TestEmbedBatchSuccess(t *testing.T) {
	srv := fakeOllamaServer(t, func(req ollamaEmbedRequest) (int, any) {
		return http.StatusOK, ollamaEmbedResponse{
			Model: req.Model,
			Embeddings: [][]float32{
				{1.0, 2.0},
				{3.0, 4.0},
				{5.0, 6.0},
			},
		}
	})
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	results, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0][0] != 1.0 || results[1][0] != 3.0 || results[2][0] != 5.0 {
		t.Errorf("unexpected embedding values: %v", results)
	}
	if d := e.Dimensions(); d != 2 {
		t.Errorf("expected Dimensions() == 2, got %d", d)
	}
}

func TestEmbedBatchOllamaError(t *testing.T) {
	srv := fakeOllamaServer(t, func(req ollamaEmbedRequest) (int, any) {
		return http.StatusBadRequest, ollamaErrorResponse{
			Error: "model 'nonexistent' not found",
		}
	})
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "nonexistent")
	_, err := e.EmbedBatch(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	expected := "ollama error: model 'nonexistent' not found"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}
