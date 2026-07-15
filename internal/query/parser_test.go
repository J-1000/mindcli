package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jankowtf/mindcli/internal/storage"
)

func TestFilterByTime(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) // a Monday
	mk := func(daysAgo int) *storage.SearchResult {
		return &storage.SearchResult{Document: &storage.Document{
			ModifiedAt: now.AddDate(0, 0, -daysAgo),
		}}
	}
	results := storage.SearchResults{mk(0), mk(3), mk(10), mk(40)}

	// No filter: everything passes through unchanged.
	if got := FilterByTime(results, ParseQuery("notes"), now); len(got) != 4 {
		t.Errorf("no filter: got %d, want 4", len(got))
	}

	// "this month" (Jun 1 .. Jun 15): the docs 0/3/10 days ago fall in June;
	// the 40-day-old one (early May) does not.
	parsed := ParseQuery("notes this month")
	if parsed.TimeFilter != "this month" {
		t.Fatalf("TimeFilter = %q, want 'this month'", parsed.TimeFilter)
	}
	got := FilterByTime(results, parsed, now)
	if len(got) != 3 {
		t.Errorf("this month: got %d results, want 3", len(got))
	}
}

func TestTimeRange(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if _, _, ok := TimeRange("", now); ok {
		t.Error("empty filter should not be ok")
	}
	start, end, ok := TimeRange("last month", now)
	if !ok {
		t.Fatal("last month should be ok")
	}
	if start.Month() != time.May || end.Month() != time.June || end.Day() != 1 {
		t.Errorf("last month range = [%s, %s], want May 1 .. Jun 1", start, end)
	}
}

func TestParseQuery(t *testing.T) {
	tests := []struct {
		query      string
		wantIntent QueryIntent
		wantSource string
		wantTime   string
	}{
		{
			query:      "golang concurrency",
			wantIntent: IntentSearch,
		},
		{
			query:      "summarize my notes on testing",
			wantIntent: IntentSummarize,
		},
		{
			query:      "what did I write about Go last week",
			wantIntent: IntentAnswer,
			wantTime:   "last week",
		},
		{
			query:      "meetings in my emails",
			wantIntent: IntentSearch,
			wantSource: "email",
		},
		{
			query:      "articles from browser last month",
			wantIntent: IntentSearch,
			wantSource: "browser",
			wantTime:   "last month",
		},
		{
			query:      "how does authentication work in pdfs",
			wantIntent: IntentAnswer,
			wantSource: "pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			parsed := ParseQuery(tt.query)

			if parsed.Intent != tt.wantIntent {
				t.Errorf("Intent = %q, want %q", parsed.Intent, tt.wantIntent)
			}
			if parsed.SourceFilter != tt.wantSource {
				t.Errorf("SourceFilter = %q, want %q", parsed.SourceFilter, tt.wantSource)
			}
			if parsed.TimeFilter != tt.wantTime {
				t.Errorf("TimeFilter = %q, want %q", parsed.TimeFilter, tt.wantTime)
			}
			if parsed.SearchTerms == "" {
				t.Error("SearchTerms should not be empty")
			}
		})
	}
}

func TestParseQueryOriginalPreserved(t *testing.T) {
	query := "  some query with spaces  "
	parsed := ParseQuery(query)

	if parsed.Original != "some query with spaces" {
		t.Errorf("Original = %q, want trimmed input", parsed.Original)
	}
}

func TestBuildRAGPrompt(t *testing.T) {
	prompt := buildRAGPrompt("What is Go?", []string{"Go is a language", "Go has goroutines"})

	if !strings.Contains(prompt, "What is Go?") {
		t.Error("prompt should contain the question")
	}
	if !strings.Contains(prompt, "Document 1") {
		t.Error("prompt should contain Document 1")
	}
	if !strings.Contains(prompt, "Document 2") {
		t.Error("prompt should contain Document 2")
	}
	if !strings.Contains(prompt, "Go is a language") {
		t.Error("prompt should contain first context")
	}
}

func TestBuildRAGPromptWithHistory(t *testing.T) {
	history := []ConversationTurn{
		{Question: "What is Go?", Answer: "A programming language."},
	}
	prompt := buildRAGPromptWithHistory("Tell me more", []string{"Go is fast"}, history)
	if !strings.Contains(prompt, "Conversation so far") {
		t.Error("prompt should include conversation history header")
	}
	if !strings.Contains(prompt, "What is Go?") || !strings.Contains(prompt, "A programming language.") {
		t.Error("prompt should include the prior question and answer")
	}
	if !strings.Contains(prompt, "Tell me more") {
		t.Error("prompt should include the follow-up question")
	}
	// Without history there should be no conversation header.
	if strings.Contains(buildRAGPrompt("q", []string{"ctx"}), "Conversation so far") {
		t.Error("plain prompt should not include conversation header")
	}
}

func TestBuildRAGPromptLimitsContexts(t *testing.T) {
	contexts := make([]string, 10)
	for i := range contexts {
		contexts[i] = "doc content"
	}
	prompt := buildRAGPrompt("question", contexts)
	// Should only include 5 documents
	if strings.Contains(prompt, "Document 6") {
		t.Error("prompt should only include up to 5 documents")
	}
}

func TestGenerateStream(t *testing.T) {
	// Create a mock Ollama server that streams newline-delimited JSON.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")

		chunks := []ollamaGenerateResponse{
			{Response: "Hello", Done: false},
			{Response: " world", Done: false},
			{Response: "!", Done: true},
		}

		enc := json.NewEncoder(w)
		for _, chunk := range chunks {
			if err := enc.Encode(chunk); err != nil {
				t.Errorf("encoding stream chunk: %v", err)
				return
			}
			flusher.Flush()
		}
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test-model")
	ctx := context.Background()

	var collected strings.Builder
	var chunkCount int
	var gotDone bool

	err := client.GenerateStream(ctx, "test prompt", func(token string, done bool) {
		collected.WriteString(token)
		chunkCount++
		if done {
			gotDone = true
		}
	})

	if err != nil {
		t.Fatalf("GenerateStream() error = %v", err)
	}

	if collected.String() != "Hello world!" {
		t.Errorf("collected = %q, want %q", collected.String(), "Hello world!")
	}
	if chunkCount != 3 {
		t.Errorf("chunkCount = %d, want 3", chunkCount)
	}
	if !gotDone {
		t.Error("never received done=true")
	}
}

func TestGenerateStreamCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")

		// Send many chunks - the client should cancel before all are consumed.
		enc := json.NewEncoder(w)
		for i := 0; i < 1000; i++ {
			if err := enc.Encode(ollamaGenerateResponse{Response: "tok ", Done: false}); err != nil {
				return
			}
			flusher.Flush()
		}
		_ = enc.Encode(ollamaGenerateResponse{Response: "", Done: true})
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test-model")
	ctx, cancel := context.WithCancel(context.Background())

	count := 0
	_ = client.GenerateStream(ctx, "test", func(token string, done bool) {
		count++
		if count >= 5 {
			cancel()
		}
	})

	// We should have stopped relatively early (the stream decode will error after cancel)
	if count > 100 {
		t.Errorf("expected early cancellation, got %d chunks", count)
	}
}

func TestGenerateAnswerStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		enc := json.NewEncoder(w)
		enc.Encode(ollamaGenerateResponse{Response: "Answer here", Done: true})
		flusher.Flush()
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test-model")
	ctx := context.Background()

	var result string
	err := client.GenerateAnswerStream(ctx, "question", []string{"context1"}, func(token string, done bool) {
		result += token
	})

	if err != nil {
		t.Fatalf("GenerateAnswerStream() error = %v", err)
	}
	if result != "Answer here" {
		t.Errorf("result = %q, want %q", result, "Answer here")
	}
}

func TestGenerateAnswerStreamNoContexts(t *testing.T) {
	client := NewLLMClient("http://localhost:1", "test")
	ctx := context.Background()

	var result string
	var gotDone bool
	err := client.GenerateAnswerStream(ctx, "question", nil, func(token string, done bool) {
		result += token
		gotDone = done
	})

	if err != nil {
		t.Fatalf("GenerateAnswerStream() error = %v", err)
	}
	if result != "No relevant documents found." {
		t.Errorf("result = %q, want fallback message", result)
	}
	if !gotDone {
		t.Error("expected done=true for no-context case")
	}
}

func TestEstimateAnswerConfidence(t *testing.T) {
	tests := []struct {
		name     string
		question string
		contexts []string
		want     string
	}{
		{
			name:     "no contexts",
			question: "what did I write about go concurrency",
			contexts: nil,
			want:     "low",
		},
		{
			name:     "high overlap and multiple contexts",
			question: "what did I write about go concurrency",
			contexts: []string{"Go concurrency uses goroutines and channels.", "My notes: go concurrency patterns and worker pools."},
			want:     "high",
		},
		{
			name:     "limited overlap",
			question: "what did I write about go concurrency",
			contexts: []string{"Shopping list: milk and eggs"},
			want:     "low",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateAnswerConfidence(tt.question, tt.contexts)
			if got.Level != tt.want {
				t.Fatalf("Level = %q, want %q (score=%0.2f)", got.Level, tt.want, got.Score)
			}
			if got.Score < 0 || got.Score > 1 {
				t.Fatalf("Score out of range: %0.4f", got.Score)
			}
		})
	}
}
