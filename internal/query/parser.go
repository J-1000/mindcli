package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// QueryIntent represents what the user wants to do.
type QueryIntent string

const (
	IntentSearch    QueryIntent = "search"
	IntentSummarize QueryIntent = "summarize"
	IntentAnswer    QueryIntent = "answer"
)

// ParsedQuery contains the analyzed query with extracted intent and entities.
type ParsedQuery struct {
	Original     string      // Original query text
	Intent       QueryIntent // What the user wants
	SearchTerms  string      // Terms for BM25/vector search
	TimeFilter   string      // Extracted time reference (e.g., "last week")
	SourceFilter string      // Extracted source filter (e.g., "emails")
}

// LLMClient calls a local Ollama instance for text generation.
type LLMClient struct {
	baseURL string
	model   string
	client  *http.Client
}

// NewLLMClient creates a client for Ollama text generation.
func NewLLMClient(baseURL, model string) *LLMClient {
	return &LLMClient{
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// ollamaGenerateRequest is the request body for /api/generate.
type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

// ollamaGenerateResponse is the response from /api/generate.
type ollamaGenerateResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// Generate calls Ollama to generate text from a prompt.
func (c *LLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	reqBody := ollamaGenerateRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var genResp ollamaGenerateResponse
	if err := json.Unmarshal(respBody, &genResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	return genResp.Response, nil
}

// ParseQuery analyzes a natural language query to extract intent and entities.
// This works without an LLM using simple heuristics, with optional LLM enhancement.
func ParseQuery(query string) ParsedQuery {
	query = strings.TrimSpace(query)
	parsed := ParsedQuery{
		Original:    query,
		Intent:      IntentSearch,
		SearchTerms: query,
	}

	lower := strings.ToLower(query)

	// Detect intent from keywords.
	if strings.HasPrefix(lower, "summarize ") || strings.HasPrefix(lower, "summary of ") {
		parsed.Intent = IntentSummarize
		parsed.SearchTerms = strings.TrimPrefix(strings.TrimPrefix(lower, "summarize "), "summary of ")
	} else if strings.HasPrefix(lower, "what ") || strings.HasPrefix(lower, "how ") ||
		strings.HasPrefix(lower, "why ") || strings.HasPrefix(lower, "when ") ||
		strings.HasPrefix(lower, "who ") || strings.HasPrefix(lower, "tell me ") {
		parsed.Intent = IntentAnswer
	}

	// Extract source filters.
	sourceKeywords := map[string]string{
		"in my notes":   "markdown",
		"in my emails":  "email",
		"in emails":     "email",
		"from browser":  "browser",
		"in browser":    "browser",
		"from clipboard": "clipboard",
		"in pdfs":       "pdf",
		"in pdf":        "pdf",
	}
	for keyword, source := range sourceKeywords {
		if strings.Contains(lower, keyword) {
			parsed.SourceFilter = source
			parsed.SearchTerms = strings.Replace(lower, keyword, "", 1)
			break
		}
	}

	// Extract time references.
	timeKeywords := []string{
		"last week", "last month", "yesterday", "today",
		"this week", "this month", "last year",
	}
	for _, kw := range timeKeywords {
		if strings.Contains(lower, kw) {
			parsed.TimeFilter = kw
			parsed.SearchTerms = strings.Replace(parsed.SearchTerms, kw, "", 1)
			break
		}
	}

	parsed.SearchTerms = strings.TrimSpace(parsed.SearchTerms)
	return parsed
}

// GenerateAnswer creates a RAG-style answer from search results using an LLM.
func (c *LLMClient) GenerateAnswer(ctx context.Context, query string, contexts []string) (string, error) {
	if len(contexts) == 0 {
		return "No relevant documents found.", nil
	}

	// Build context string from search results.
	var contextStr strings.Builder
	for i, ctx := range contexts {
		if i >= 5 {
			break // Limit context to top 5 results
		}
		contextStr.WriteString(fmt.Sprintf("--- Document %d ---\n%s\n\n", i+1, ctx))
	}

	prompt := fmt.Sprintf(`Based on the following documents from the user's personal knowledge base, answer the question concisely.

%s

Question: %s

Answer:`, contextStr.String(), query)

	return c.Generate(ctx, prompt)
}
