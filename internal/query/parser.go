package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
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

// AnswerConfidence represents a simple confidence estimate for generated answers.
type AnswerConfidence struct {
	Score float64 // [0,1]
	Level string  // low, medium, high
}

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// LLMClient generates text via a local Ollama instance or the OpenAI API.
type LLMClient struct {
	provider string // "ollama" | "openai"
	baseURL  string
	model    string
	apiKey   string
	client   *http.Client
}

// NewLLMClient creates a client for Ollama text generation.
func NewLLMClient(baseURL, model string) *LLMClient {
	return &LLMClient{
		provider: "ollama",
		baseURL:  baseURL,
		model:    model,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

// NewOpenAILLMClient creates a client for OpenAI chat-completion generation.
func NewOpenAILLMClient(apiKey, model string) *LLMClient {
	baseURL := defaultOpenAIBaseURL
	if env := os.Getenv("OPENAI_BASE_URL"); env != "" {
		baseURL = env
	}
	return &LLMClient{
		provider: "openai",
		baseURL:  baseURL,
		model:    model,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 60 * time.Second},
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

// Generate produces text from a prompt using the configured provider.
func (c *LLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	if c.provider == "openai" {
		return c.openAIGenerate(ctx, prompt)
	}
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
		"in my notes":    "markdown",
		"in my emails":   "email",
		"in emails":      "email",
		"from browser":   "browser",
		"in browser":     "browser",
		"from clipboard": "clipboard",
		"in pdfs":        "pdf",
		"in pdf":         "pdf",
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

// TimeRange converts a parsed time-filter keyword into an inclusive [start,end]
// range relative to now. ok is false when there is no recognized filter.
func TimeRange(filter string, now time.Time) (start, end time.Time, ok bool) {
	startOfDay := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
	startOfWeek := func(t time.Time) time.Time {
		d := startOfDay(t)
		// ISO-ish: treat Monday as the first day of the week.
		offset := (int(d.Weekday()) + 6) % 7
		return d.AddDate(0, 0, -offset)
	}
	firstOfMonth := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	}

	end = now
	switch filter {
	case "today":
		start = startOfDay(now)
	case "yesterday":
		start = startOfDay(now.AddDate(0, 0, -1))
		end = startOfDay(now)
	case "this week":
		start = startOfWeek(now)
	case "last week":
		end = startOfWeek(now)
		start = end.AddDate(0, 0, -7)
	case "this month":
		start = firstOfMonth(now)
	case "last month":
		end = firstOfMonth(now)
		start = end.AddDate(0, -1, 0)
	case "last year":
		start = now.AddDate(-1, 0, 0)
	default:
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

// inTimeRange reports whether t falls within the parsed query's time filter.
// When there is no time filter it always returns true.
func inTimeRange(t time.Time, parsed ParsedQuery, now time.Time) bool {
	start, end, ok := TimeRange(parsed.TimeFilter, now)
	if !ok {
		return true
	}
	return !t.Before(start) && !t.After(end)
}

// FilterByTime drops search results whose document modification time falls
// outside the parsed query's time filter. Results are returned unchanged when
// there is no time filter.
func FilterByTime(results storage.SearchResults, parsed ParsedQuery, now time.Time) storage.SearchResults {
	if _, _, ok := TimeRange(parsed.TimeFilter, now); !ok {
		return results
	}
	filtered := make(storage.SearchResults, 0, len(results))
	for _, r := range results {
		if r.Document != nil && inTimeRange(r.Document.ModifiedAt, parsed, now) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// FilterDocumentsByTime is the document-slice equivalent of FilterByTime.
func FilterDocumentsByTime(docs []*storage.Document, parsed ParsedQuery, now time.Time) []*storage.Document {
	if _, _, ok := TimeRange(parsed.TimeFilter, now); !ok {
		return docs
	}
	filtered := make([]*storage.Document, 0, len(docs))
	for _, d := range docs {
		if d != nil && inTimeRange(d.ModifiedAt, parsed, now) {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// buildRAGPrompt constructs the prompt for RAG-style answer generation.
func buildRAGPrompt(question string, contexts []string) string {
	var contextStr strings.Builder
	for i, ctx := range contexts {
		if i >= 5 {
			break
		}
		contextStr.WriteString(fmt.Sprintf("--- Document %d ---\n%s\n\n", i+1, ctx))
	}

	return fmt.Sprintf(`Based on the following documents from the user's personal knowledge base, answer the question concisely.

%s

Question: %s

Answer:`, contextStr.String(), question)
}

// GenerateAnswer creates a RAG-style answer from search results using an LLM.
func (c *LLMClient) GenerateAnswer(ctx context.Context, query string, contexts []string) (string, error) {
	if len(contexts) == 0 {
		return "No relevant documents found.", nil
	}
	return c.Generate(ctx, buildRAGPrompt(query, contexts))
}

// GenerateStream sends a streaming request and calls onChunk for each token.
func (c *LLMClient) GenerateStream(ctx context.Context, prompt string, onChunk func(token string, done bool)) error {
	if c.provider == "openai" {
		return c.openAIGenerateStream(ctx, prompt, onChunk)
	}
	reqBody := ollamaGenerateRequest{
		Model:  c.model,
		Prompt: prompt,
		Stream: true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Use a client without timeout for streaming; rely on ctx for cancellation.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(respBody))
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk ollamaGenerateResponse
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("decoding stream: %w", err)
		}
		onChunk(chunk.Response, chunk.Done)
		if chunk.Done {
			return nil
		}
	}
}

// GenerateAnswerStream builds the RAG prompt and streams the response.
func (c *LLMClient) GenerateAnswerStream(ctx context.Context, question string, contexts []string, onChunk func(string, bool)) error {
	if len(contexts) == 0 {
		onChunk("No relevant documents found.", true)
		return nil
	}
	return c.GenerateStream(ctx, buildRAGPrompt(question, contexts), onChunk)
}

// EstimateAnswerConfidence estimates answer confidence from question/context coverage.
func EstimateAnswerConfidence(question string, contexts []string) AnswerConfidence {
	if len(contexts) == 0 {
		return AnswerConfidence{Score: 0, Level: "low"}
	}

	questionTokens := tokenize(question)
	contextCountScore := minFloat(float64(len(contexts)), 5) / 5.0

	var totalLen int
	bestOverlap := 0.0
	mergedContextTokens := make(map[string]struct{})
	for _, ctx := range contexts {
		totalLen += len(ctx)
		ctxTokens := tokenize(ctx)
		for tok := range ctxTokens {
			mergedContextTokens[tok] = struct{}{}
		}
		overlap := tokenOverlap(questionTokens, ctxTokens)
		if overlap > bestOverlap {
			bestOverlap = overlap
		}
	}
	unionOverlap := tokenOverlap(questionTokens, mergedContextTokens)

	avgLen := float64(totalLen) / float64(len(contexts))
	lengthScore := minFloat(avgLen, 800) / 800.0

	// Weighted heuristic:
	// - more supporting contexts => higher confidence
	// - reasonable context depth => higher confidence
	// - lexical overlap with the question => higher confidence
	score := 0.25*contextCountScore + 0.15*lengthScore + 0.30*bestOverlap + 0.30*unionOverlap
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	level := "low"
	switch {
	case score >= 0.60:
		level = "high"
	case score >= 0.35:
		level = "medium"
	}

	return AnswerConfidence{Score: score, Level: level}
}

var tokenSplitRe = regexp.MustCompile(`[^a-z0-9]+`)
var stopwords = map[string]struct{}{
	"what": {}, "when": {}, "where": {}, "which": {}, "who": {}, "why": {}, "how": {},
	"the": {}, "and": {}, "for": {}, "with": {}, "did": {}, "about": {}, "write": {},
	"from": {}, "into": {}, "this": {}, "that": {}, "your": {}, "were": {}, "have": {},
}

func tokenize(s string) map[string]struct{} {
	s = strings.ToLower(s)
	parts := tokenSplitRe.Split(s, -1)
	out := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		if _, skip := stopwords[p]; skip {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func tokenOverlap(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var shared int
	for token := range a {
		if _, ok := b[token]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(a))
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
