package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIEmbedderBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		var req openAIEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)
		// Return embeddings out of order to exercise index sorting.
		resp := openAIEmbedResponse{}
		for i := len(req.Input) - 1; i >= 0; i-- {
			resp.Data = append(resp.Data, struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}{Index: i, Embedding: []float32{float32(i), 0.5}})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("OPENAI_BASE_URL", srv.URL)
	emb := NewOpenAIEmbedder("sk-test", "text-embedding-3-small")

	got, err := emb.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d embeddings, want 3", len(got))
	}
	// After sorting by index, element 0 must correspond to input "a" (index 0).
	if got[0][0] != 0 || got[2][0] != 2 {
		t.Errorf("embeddings not ordered by index: %v", got)
	}
	if emb.Dimensions() != 2 {
		t.Errorf("Dimensions() = %d, want 2", emb.Dimensions())
	}
}

func TestOpenAIEmbedderMissingKey(t *testing.T) {
	emb := NewOpenAIEmbedder("", "text-embedding-3-small")
	if _, err := emb.Embed(context.Background(), "hi"); err == nil {
		t.Error("expected error when api key is missing")
	}
}
