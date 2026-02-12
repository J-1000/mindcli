package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseQuery(t *testing.T) {
	tests := []struct {
		query        string
		wantIntent   QueryIntent
		wantSource   string
		wantTime     string
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
			enc.Encode(chunk)
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
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")

		// Send many chunks - the client should cancel before all are consumed.
		enc := json.NewEncoder(w)
		for i := 0; i < 1000; i++ {
			enc.Encode(ollamaGenerateResponse{Response: "tok ", Done: false})
			flusher.Flush()
		}
		enc.Encode(ollamaGenerateResponse{Response: "", Done: true})
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
