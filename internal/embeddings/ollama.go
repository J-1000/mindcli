package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaEmbedder generates embeddings using a local Ollama server.
type OllamaEmbedder struct {
	baseURL    string
	model      string
	dimensions int
	client     *http.Client
}

// NewOllamaEmbedder creates an embedder that connects to Ollama.
func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	return &OllamaEmbedder{
		baseURL: baseURL,
		model:   model,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// ollamaEmbedRequest is the request body for /api/embed.
type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

// ollamaEmbedResponse is the response from /api/embed.
type ollamaEmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
}

// ollamaErrorResponse is the error response from Ollama.
type ollamaErrorResponse struct {
	Error string `json:"error"`
}

// Embed generates an embedding for a single text.
func (o *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama returned no embeddings")
	}
	return results[0], nil
}

// EmbedBatch generates embeddings for multiple texts.
func (o *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := ollamaEmbedRequest{
		Model: o.model,
		Input: texts,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed (is Ollama running at %s?): %w", o.baseURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp ollamaErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("ollama error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var embedResp ollamaEmbedResponse
	if err := json.Unmarshal(respBody, &embedResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if len(embedResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embedResp.Embeddings))
	}

	// Cache dimensions from first successful response.
	if o.dimensions == 0 && len(embedResp.Embeddings) > 0 {
		o.dimensions = len(embedResp.Embeddings[0])
	}

	return embedResp.Embeddings, nil
}

// Dimensions returns the embedding vector dimension.
// Returns 0 if no embeddings have been generated yet.
func (o *OllamaEmbedder) Dimensions() int {
	return o.dimensions
}
