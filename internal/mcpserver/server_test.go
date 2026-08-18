package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/query"
	"github.com/J-1000/mindcli/internal/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeSearcher struct {
	results  storage.SearchResults
	related  []query.RelatedResult
	parsed   query.ParsedQuery
	started  chan struct{}
	canceled chan struct{}
}

func (f *fakeSearcher) SearchParsed(ctx context.Context, parsed query.ParsedQuery, _ int) (storage.SearchResults, error) {
	f.parsed = parsed
	if f.started != nil {
		close(f.started)
		<-ctx.Done()
		close(f.canceled)
		return nil, ctx.Err()
	}
	return f.results, nil
}

func (f *fakeSearcher) Related(context.Context, string, int) ([]query.RelatedResult, error) {
	return f.related, nil
}

type fakeAnswerer struct {
	answer   string
	contexts []string
}

func (f *fakeAnswerer) GenerateAnswer(_ context.Context, _ string, contexts []string) (string, error) {
	f.contexts = append([]string(nil), contexts...)
	return f.answer, nil
}

func newMCPTestService(t *testing.T) (*Service, *fakeSearcher) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "mindcli.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	})

	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	source := &storage.Document{
		ID: "stable-source", Source: storage.SourceMarkdown, Path: "/SECRET/source.md",
		Title: "SECRET Launch", Content: "SECRET alpha details", Preview: "SECRET preview",
		Metadata:    map[string]string{"author": "SECRET owner", "tags": "project"},
		ContentHash: "source", IndexedAt: now.Add(-24 * time.Hour), ModifiedAt: now.Add(-48 * time.Hour),
	}
	related := &storage.Document{
		ID: "stable-related", Source: storage.SourceBrowser, Path: "https://example.com/SECRET",
		Title: "Related SECRET", Content: "related material", Preview: "related SECRET preview",
		Metadata:    map[string]string{"normalized_url": "https://example.com/SECRET"},
		ContentHash: "related", IndexedAt: now.Add(-2 * time.Hour), ModifiedAt: now.Add(-2 * time.Hour),
	}
	for _, doc := range []*storage.Document{source, related} {
		if err := db.InsertDocument(context.Background(), doc); err != nil {
			t.Fatal(err)
		}
	}
	collection := &storage.Collection{Name: "research", Description: "SECRET collection", Query: "alpha"}
	if err := db.CreateCollection(context.Background(), collection); err != nil {
		t.Fatal(err)
	}
	if err := db.AddToCollection(context.Background(), collection.ID, source.ID); err != nil {
		t.Fatal(err)
	}

	searcher := &fakeSearcher{
		results: storage.SearchResults{{
			Document: source, Score: 0.9, Highlights: []string{"SECRET highlight"},
		}},
		related: []query.RelatedResult{{
			Document: related, Score: 0.8,
			Reasons: []query.RelationReason{{Kind: query.RelationTags, Score: 1, Values: []string{"SECRET-tag"}}},
		}},
	}
	redactor, errs := privacy.NewRedactor([]string{"SECRET"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	answerer := &fakeAnswerer{answer: "SECRET answer [1]"}
	service := NewService(db, searcher, answerer, redactor)
	service.now = func() time.Time { return now }
	return service, searcher
}

func connectMCPTestClient(t *testing.T, service *Service) *mcp.ClientSession {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := New(service, "test", logger)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mindcli-test", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clientSession.Close(); err != nil {
			t.Errorf("closing MCP client: %v", err)
		}
		if err := serverSession.Wait(); err != nil && !expectedMCPTestShutdown(err) {
			t.Errorf("waiting for MCP server: %v", err)
		}
	})
	return clientSession
}

func expectedMCPTestShutdown(err error) bool {
	return errors.Is(err, io.EOF) ||
		strings.Contains(err.Error(), "server is closing") ||
		strings.Contains(err.Error(), "closed pipe")
}

func callTool[Output any](t *testing.T, session *mcp.ClientSession, name string, arguments any) (Output, *mcp.CallToolResult) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool %s returned error: %+v", name, result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output Output
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("decoding %s output: %v\n%s", name, err, data)
	}
	if strings.Contains(string(data), "SECRET") {
		t.Fatalf("tool %s leaked unredacted data: %s", name, data)
	}
	return output, result
}

func TestMCPDiscoveryAndReadOnlyToolCalls(t *testing.T) {
	service, searcher := newMCPTestService(t)
	session := connectMCPTestClient(t, service)

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantTools := map[string]bool{
		"search": true, "ask": true, "get_document": true,
		"list_collections": true, "show_collection": true,
		"recent_documents": true, "related_documents": true,
	}
	if len(listed.Tools) != len(wantTools) {
		t.Fatalf("discovered %d tools, want %d: %+v", len(listed.Tools), len(wantTools), listed.Tools)
	}
	for _, tool := range listed.Tools {
		if !wantTools[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		delete(wantTools, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint {
			t.Errorf("tool %q is not marked read-only/idempotent: %+v", tool.Name, tool.Annotations)
		}
		if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("tool %q is not marked closed-world", tool.Name)
		}
	}
	if len(wantTools) != 0 {
		t.Fatalf("missing tools: %v", wantTools)
	}

	searchOutput, _ := callTool[SearchOutput](t, session, "search", map[string]any{
		"query": "alpha", "limit": 2,
		"filters": map[string]any{"sources": []string{"markdown"}, "tags": []string{"project"}},
	})
	if len(searchOutput.Results) != 1 || searchOutput.Results[0].Document.ID != "stable-source" {
		t.Fatalf("search output = %+v", searchOutput)
	}
	if len(searcher.parsed.Filters.Sources) != 1 || len(searcher.parsed.Filters.Tags) != 1 {
		t.Fatalf("typed filters were not passed to retrieval: %+v", searcher.parsed.Filters)
	}

	document, _ := callTool[DocumentOutput](t, session, "get_document", map[string]any{
		"id": "stable-source", "max_content_bytes": 12,
	})
	if document.ID != "stable-source" || !document.Truncated || len(document.Content) > 12 {
		t.Fatalf("bounded document output = %+v", document)
	}

	collections, _ := callTool[ListCollectionsOutput](t, session, "list_collections", map[string]any{})
	if len(collections.Collections) != 1 || collections.Collections[0].Name != "research" {
		t.Fatalf("collections output = %+v", collections)
	}

	shown, _ := callTool[ShowCollectionOutput](t, session, "show_collection", map[string]any{"name": "research"})
	if len(shown.Documents) != 1 || shown.Documents[0].ID != "stable-source" {
		t.Fatalf("show collection output = %+v", shown)
	}

	recent, _ := callTool[RecentDocumentsOutput](t, session, "recent_documents", map[string]any{"since": "7d"})
	if len(recent.Documents) != 2 || recent.Documents[0].ID != "stable-related" {
		t.Fatalf("recent documents output = %+v", recent)
	}

	related, _ := callTool[RelatedDocumentsOutput](t, session, "related_documents", map[string]any{"id": "stable-source"})
	if len(related.Results) != 1 || related.Results[0].Document.ID != "stable-related" {
		t.Fatalf("related documents output = %+v", related)
	}
	if got := related.Results[0].Reasons[0].Values[0]; got != "[REDACTED]-tag" {
		t.Fatalf("redacted relation reason = %q", got)
	}

	answer, _ := callTool[AskOutput](t, session, "ask", map[string]any{"question": "what is alpha?"})
	if answer.Answer != "[REDACTED] answer [1]" || len(answer.Citations) != 1 || answer.Citations[0].ID != "stable-source" {
		t.Fatalf("ask output = %+v", answer)
	}
}

func TestMCPMalformedInputsReturnToolErrors(t *testing.T) {
	service, _ := newMCPTestService(t)
	session := connectMCPTestClient(t, service)

	for _, test := range []struct {
		name string
		args any
	}{
		{name: "wrong type", args: map[string]any{"query": "alpha", "limit": "many"}},
		{name: "missing query", args: map[string]any{}},
		{name: "bad date", args: map[string]any{"query": "alpha", "filters": map[string]any{"after": "tomorrow"}}},
		{name: "excessive limit", args: map[string]any{"query": "alpha", "limit": 51}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "search", Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || len(result.Content) == 0 {
				t.Fatalf("malformed input result = %+v, want visible tool error", result)
			}
		})
	}
}

func TestMCPCancellationReachesToolHandler(t *testing.T) {
	service, _ := newMCPTestService(t)
	blocking := &fakeSearcher{started: make(chan struct{}), canceled: make(chan struct{})}
	service.searcher = blocking
	session := connectMCPTestClient(t, service)

	ctx, cancel := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "alpha"}})
		callDone <- err
	}()
	<-blocking.started
	cancel()
	if err := <-callDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("CallTool() error = %v, want context canceled", err)
	}
	select {
	case <-blocking.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("server handler did not observe cancellation")
	}
}
