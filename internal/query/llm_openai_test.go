package query

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIGenerateStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q, want Bearer sk-test", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if _, err := fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"); err != nil {
			t.Errorf("writing stream response: %v", err)
		}
		if _, err := fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n"); err != nil {
			t.Errorf("writing stream response: %v", err)
		}
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Errorf("writing stream response: %v", err)
		}
	}))
	defer srv.Close()

	t.Setenv("OPENAI_BASE_URL", srv.URL)
	client := NewOpenAILLMClient("sk-test", "gpt-4o-mini")

	var sb strings.Builder
	var done bool
	err := client.GenerateStream(context.Background(), "hi", func(token string, d bool) {
		sb.WriteString(token)
		if d {
			done = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if sb.String() != "Hello world" {
		t.Errorf("got %q, want %q", sb.String(), "Hello world")
	}
	if !done {
		t.Error("stream never signaled done")
	}
}
