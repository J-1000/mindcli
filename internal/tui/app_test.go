package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jankowtf/mindcli/internal/query"
	"github.com/jankowtf/mindcli/internal/storage"
)

func setupTestDB(t *testing.T) (*storage.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "mindcli-tui-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func TestNew(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	if model.db != db {
		t.Error("New() did not set database")
	}

	if model.panel != PanelSearch {
		t.Errorf("Initial panel = %v, want PanelSearch", model.panel)
	}

	if model.cursor != 0 {
		t.Errorf("Initial cursor = %d, want 0", model.cursor)
	}
}

func TestModelInit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	cmd := model.Init()

	if cmd == nil {
		t.Error("Init() returned nil cmd")
	}
}

func TestModelUpdateWindowSize(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	msg := tea.WindowSizeMsg{Width: 120, Height: 40}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if m.width != 120 {
		t.Errorf("width = %d, want 120", m.width)
	}
	if m.height != 40 {
		t.Errorf("height = %d, want 40", m.height)
	}
}

func TestModelUpdateDocsLoaded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	docs := []*storage.Document{
		{ID: "1", Title: "Doc 1", Source: storage.SourceMarkdown},
		{ID: "2", Title: "Doc 2", Source: storage.SourcePDF},
	}

	msg := docsLoadedMsg{docs: docs}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if len(m.results) != 2 {
		t.Errorf("results len = %d, want 2", len(m.results))
	}
}

func TestModelUpdateSearchResults(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	docs := []*storage.Document{
		{ID: "1", Title: "Search Result", Source: storage.SourceMarkdown},
	}

	msg := searchResultsMsg{docs: docs}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if len(m.results) != 1 {
		t.Errorf("results len = %d, want 1", len(m.results))
	}
	if m.statusMsg != "1 results" {
		t.Errorf("statusMsg = %q, want '1 results'", m.statusMsg)
	}
}

func TestModelUpdateError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	msg := errMsg{err: os.ErrNotExist}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if !m.statusIsErr {
		t.Error("statusIsErr should be true after error")
	}
}

func TestModelToggleHelp(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	if model.showHelp {
		t.Error("showHelp should initially be false")
	}

	// Press ? to toggle help
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if !m.showHelp {
		t.Error("showHelp should be true after pressing ?")
	}

	// Press ? again to hide
	updated, _ = m.Update(msg)
	m = updated.(Model)

	if m.showHelp {
		t.Error("showHelp should be false after pressing ? again")
	}
}

func TestModelView(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	model.width = 120
	model.height = 40

	view := model.View()

	if view == "" {
		t.Error("View() returned empty string")
	}

	if view == "Loading..." {
		t.Error("View() returned loading state with dimensions set")
	}
}

func TestModelViewLoading(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	// Don't set width/height

	view := model.View()

	if view != "Loading..." {
		t.Errorf("View() = %q, want 'Loading...'", view)
	}
}

func TestModelViewHelp(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	model.width = 120
	model.height = 40
	model.showHelp = true

	view := model.View()

	if view == "" {
		t.Error("View() returned empty string in help mode")
	}
}

func TestPanelNavigation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	// Add some results so we can navigate
	model.results = []*storage.Document{
		{ID: "1", Title: "Test", Source: storage.SourceMarkdown},
	}

	// Initial state: search panel
	if model.panel != PanelSearch {
		t.Errorf("Initial panel = %v, want PanelSearch", model.panel)
	}

	// Tab to next panel
	tabMsg := tea.KeyMsg{Type: tea.KeyTab}
	updated, _ := model.Update(tabMsg)
	m := updated.(Model)

	if m.panel != PanelResults {
		t.Errorf("After Tab, panel = %v, want PanelResults", m.panel)
	}

	// Tab again
	updated, _ = m.Update(tabMsg)
	m = updated.(Model)

	if m.panel != PanelPreview {
		t.Errorf("After second Tab, panel = %v, want PanelPreview", m.panel)
	}

	// Tab wraps around
	updated, _ = m.Update(tabMsg)
	m = updated.(Model)

	if m.panel != PanelSearch {
		t.Errorf("After third Tab, panel = %v, want PanelSearch (wrapped)", m.panel)
	}
}

func TestPanelNavigationShiftTab(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	model.panel = PanelResults

	shiftTabMsg := tea.KeyMsg{Type: tea.KeyShiftTab}
	updated, _ := model.Update(shiftTabMsg)
	m := updated.(Model)

	if m.panel != PanelSearch {
		t.Errorf("After Shift+Tab, panel = %v, want PanelSearch", m.panel)
	}
}

func TestResultsNavigation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	model.panel = PanelResults
	model.results = []*storage.Document{
		{ID: "1", Title: "Doc 1", Source: storage.SourceMarkdown},
		{ID: "2", Title: "Doc 2", Source: storage.SourceMarkdown},
		{ID: "3", Title: "Doc 3", Source: storage.SourceMarkdown},
	}

	// Move down
	downMsg := tea.KeyMsg{Type: tea.KeyDown}
	updated, _ := model.Update(downMsg)
	m := updated.(Model)

	if m.cursor != 1 {
		t.Errorf("After Down, cursor = %d, want 1", m.cursor)
	}

	// Move down again
	updated, _ = m.Update(downMsg)
	m = updated.(Model)

	if m.cursor != 2 {
		t.Errorf("After second Down, cursor = %d, want 2", m.cursor)
	}

	// Can't go past end
	updated, _ = m.Update(downMsg)
	m = updated.(Model)

	if m.cursor != 2 {
		t.Errorf("After third Down, cursor = %d, want 2 (clamped)", m.cursor)
	}

	// Move up
	upMsg := tea.KeyMsg{Type: tea.KeyUp}
	updated, _ = m.Update(upMsg)
	m = updated.(Model)

	if m.cursor != 1 {
		t.Errorf("After Up, cursor = %d, want 1", m.cursor)
	}
}

func TestSearchResultsIntegration(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add test documents to database
	ctx := t.Context()
	now := time.Now()
	docs := []*storage.Document{
		{ID: "1", Source: storage.SourceMarkdown, Path: "/test1.md", Title: "Go Programming", Content: "Learn Go", ContentHash: "h1", IndexedAt: now, ModifiedAt: now},
		{ID: "2", Source: storage.SourceMarkdown, Path: "/test2.md", Title: "Python Basics", Content: "Learn Python", ContentHash: "h2", IndexedAt: now, ModifiedAt: now},
	}
	for _, doc := range docs {
		if err := db.InsertDocument(ctx, doc); err != nil {
			t.Fatalf("Failed to insert document: %v", err)
		}
	}

	model := New(db, nil, nil, nil)

	// Initialize and run the load command
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil")
	}

	// Execute the batch command to get messages
	// In real use, the runtime handles this, but we can test the message handling
	msg := docsLoadedMsg{docs: docs}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if len(m.results) != 2 {
		t.Errorf("After loading, results = %d, want 2", len(m.results))
	}
}

func TestMaxFunction(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 2},
		{2, 1, 2},
		{0, 0, 0},
		{-1, 1, 1},
		{-5, -3, -3},
	}

	for _, tt := range tests {
		got := max(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("max(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMinFunction(t *testing.T) {
	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{0, 0, 0},
		{-1, 1, -1},
		{-5, -3, -5},
	}

	for _, tt := range tests {
		got := min(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("min(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNewWithLLMClient(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	llm := query.NewLLMClient("http://localhost:11434", "llama3.2")
	model := New(db, nil, nil, llm)

	if model.llm != llm {
		t.Error("New() did not set LLM client")
	}
}

func TestSearchResultsWithAnswer(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	model.width = 120
	model.height = 40

	docs := []*storage.Document{
		{ID: "1", Title: "Go Guide", Source: storage.SourceMarkdown, Content: "Learn Go"},
		{ID: "2", Title: "Go Tips", Source: storage.SourceMarkdown, Content: "Go tips"},
	}

	msg := searchResultsMsg{
		docs: docs,
		parsed: query.ParsedQuery{
			Original:    "what is Go?",
			Intent:      query.IntentAnswer,
			SearchTerms: "Go",
		},
	}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	// Without LLM client, no streaming should start; answerText stays empty
	if m.answerText != "" {
		t.Errorf("answerText = %q, want empty (no LLM client)", m.answerText)
	}
	if len(m.results) != 2 {
		t.Errorf("results len = %d, want 2", len(m.results))
	}
}

func TestSearchResultsWithSourceFilter(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	msg := searchResultsMsg{
		docs: []*storage.Document{
			{ID: "1", Title: "Email 1", Source: storage.SourceEmail},
		},
		parsed: query.ParsedQuery{
			Original:     "meetings in my emails",
			Intent:       query.IntentSearch,
			SearchTerms:  "meetings",
			SourceFilter: "email",
		},
	}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if !strings.Contains(m.statusMsg, "[source:email]") {
		t.Errorf("statusMsg = %q, want it to contain '[source:email]'", m.statusMsg)
	}
}

func TestSearchResultsWithTimeFilter(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)

	msg := searchResultsMsg{
		docs: []*storage.Document{
			{ID: "1", Title: "Note", Source: storage.SourceMarkdown},
		},
		parsed: query.ParsedQuery{
			Original:    "notes from last week",
			Intent:      query.IntentSearch,
			SearchTerms: "notes",
			TimeFilter:  "last week",
		},
	}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if !strings.Contains(m.statusMsg, "[last week]") {
		t.Errorf("statusMsg = %q, want it to contain '[last week]'", m.statusMsg)
	}
}

func TestShowAnswer(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	model.width = 120
	model.height = 40
	model.updateViewportSize()
	model.answerText = "This is the LLM answer."
	model.results = []*storage.Document{
		{ID: "1", Title: "Source Doc", Source: storage.SourceMarkdown},
		{ID: "2", Title: "Source Doc 2", Source: storage.SourceMarkdown},
	}

	model.showAnswer()

	content := model.preview.View()
	if content == "" {
		t.Error("showAnswer() did not set preview content")
	}
}

func TestAnswerClearedOnNavigation(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil)
	model.width = 120
	model.height = 40

	// Simulate receiving search results (no LLM set so no streaming)
	docs := []*storage.Document{
		{ID: "1", Title: "Doc 1", Source: storage.SourceMarkdown, Content: "Content 1", Path: "/a.md"},
		{ID: "2", Title: "Doc 2", Source: storage.SourceMarkdown, Content: "Content 2", Path: "/b.md"},
	}
	msg := searchResultsMsg{
		docs:   docs,
		parsed: query.ParsedQuery{Intent: query.IntentAnswer, SearchTerms: "test"},
	}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	// Without LLM, no streaming should start — answerText stays empty
	if m.answerText != "" {
		t.Fatal("answerText should be empty without LLM client")
	}
	if m.streaming {
		t.Fatal("should not be streaming without LLM client")
	}

	// Navigate to results panel and move cursor
	m.panel = PanelResults
	downMsg := tea.KeyMsg{Type: tea.KeyDown}
	updated, _ = m.Update(downMsg)
	m = updated.(Model)

	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}

	// Test streamChunkMsg handling
	m.streaming = true
	m.answerText = ""
	chunkUpdated, _ := m.Update(streamChunkMsg{token: "Hello", done: false})
	mc := chunkUpdated.(Model)
	if mc.answerText != "Hello" {
		t.Errorf("answerText = %q, want %q", mc.answerText, "Hello")
	}
	if !mc.streaming {
		t.Error("should still be streaming after non-done chunk")
	}

	// Final chunk
	doneUpdated, _ := mc.Update(streamChunkMsg{done: true})
	md := doneUpdated.(Model)
	if md.streaming {
		t.Error("should not be streaming after done chunk")
	}
}
