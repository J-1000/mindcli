package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

// DefaultOpenAIBaseURL is the OpenAI API base. Override with OPENAI_BASE_URL to
// target an OpenAI-compatible server.
const DefaultOpenAIBaseURL = "https://api.openai.com/v1"

// OpenAIEmbedder generates embeddings using the OpenAI embeddings API (or any
// OpenAI-compatible endpoint).
type OpenAIEmbedder struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	client     *http.Client
}

// NewOpenAIEmbedder creates an embedder backed by the OpenAI embeddings API.
func NewOpenAIEmbedder(apiKey, model string) *OpenAIEmbedder {
	baseURL := DefaultOpenAIBaseURL
	if env := os.Getenv("OPENAI_BASE_URL"); env != "" {
		baseURL = env
	}
	return &OpenAIEmbedder{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

type openAIEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIEmbedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Embed generates an embedding for a single text.
func (o *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	results, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("openai returned no embeddings")
	}
	return results[0], nil
}

// EmbedBatch generates embeddings for multiple texts.
func (o *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if o.apiKey == "" {
		return nil, fmt.Errorf("openai api key not configured (set embeddings.openai_key)")
	}

	body, err := json.Marshal(openAIEmbedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var embedResp openAIEmbedResponse
	if err := json.Unmarshal(respBody, &embedResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		if embedResp.Error != nil {
			return nil, fmt.Errorf("openai error: %s", embedResp.Error.Message)
		}
		return nil, fmt.Errorf("openai returned status %d: %s", resp.StatusCode, string(respBody))
	}
	if len(embedResp.Data) != len(texts) {
		return nil, fmt.Errorf("expected %d embeddings, got %d", len(texts), len(embedResp.Data))
	}

	// Responses include an index; sort to guarantee input order.
	sort.Slice(embedResp.Data, func(i, j int) bool {
		return embedResp.Data[i].Index < embedResp.Data[j].Index
	})

	results := make([][]float32, len(embedResp.Data))
	for i, d := range embedResp.Data {
		results[i] = d.Embedding
	}

	if o.dimensions == 0 && len(results) > 0 {
		o.dimensions = len(results[0])
	}

	return results, nil
}

// Dimensions returns the embedding vector dimension (0 until first call).
func (o *OpenAIEmbedder) Dimensions() int {
	return o.dimensions
}
