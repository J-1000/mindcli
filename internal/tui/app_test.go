package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/filter"
	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/query"
	"github.com/J-1000/mindcli/internal/search"
	"github.com/J-1000/mindcli/internal/storage"
	tea "github.com/charmbracelet/bubbletea"
)

func setupTestDB(t *testing.T) (*storage.DB, func()) {
	t.Helper()

	tmpDir := t.TempDir()

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := storage.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	}

	return db, cleanup
}

func TestNew(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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

func TestReindexDoneUpdatesStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, func(_ context.Context) (int, int, error) {
		return 5, 1, nil
	})
	model.indexing = true

	updated, _ := model.Update(reindexDoneMsg{indexed: 5, errs: 1})
	m := updated.(Model)
	if m.indexing {
		t.Error("indexing flag should be cleared after reindexDoneMsg")
	}
	if !strings.Contains(m.statusMsg, "Indexed 5") {
		t.Errorf("status = %q, want it to mention indexed count", m.statusMsg)
	}
}

func TestNextSourceFilter(t *testing.T) {
	got := nextSourceFilter("")
	if got != storage.SourceMarkdown {
		t.Errorf("after all, got %q, want markdown", got)
	}
	// Cycling from the last source wraps back to all.
	if got := nextSourceFilter(storage.SourceClipboard); got != "" {
		t.Errorf("after clipboard, got %q, want \"\" (all)", got)
	}
}

func TestModelInit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	cmd := model.Init()

	if cmd == nil {
		t.Error("Init() returned nil cmd")
	}
}

func TestModelUpdateWindowSize(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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

func TestModelUpdateRelatedResultsShowsReasons(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	doc := &storage.Document{ID: "related", Title: "Related", Source: storage.SourceMarkdown, Path: "/related.md"}
	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	model.width = 120
	model.height = 40
	model.updateViewportSize()
	updated, _ := model.Update(relatedResultsMsg{
		sourceTitle: "Source",
		results: []query.RelatedResult{{
			Document: doc,
			Score:    0.5,
			Reasons:  []query.RelationReason{{Kind: query.RelationTags, Score: 1, Values: []string{"project"}}},
		}},
	})
	m := updated.(Model)
	if len(m.results) != 1 || m.results[0].ID != doc.ID {
		t.Fatalf("related results = %+v", m.results)
	}
	if !strings.Contains(m.statusMsg, "1 documents related to Source") {
		t.Errorf("statusMsg = %q", m.statusMsg)
	}
	if !strings.Contains(m.preview.View(), "Related: shared tags: project") {
		t.Errorf("preview missing relation reason: %q", m.preview.View())
	}
}

func TestRelatedKeyReportsUnavailableSearcher(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	model.panel = PanelResults
	model.results = []*storage.Document{{ID: "selected", Title: "Selected"}}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m := updated.(Model)
	if cmd != nil {
		t.Fatal("related key returned a command without a related searcher")
	}
	if !m.statusIsErr || m.statusMsg != "Related search is unavailable" {
		t.Fatalf("status = %q (error=%v)", m.statusMsg, m.statusIsErr)
	}
}

func TestQuickCaptureFlow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	model.SetCapture(func(ctx context.Context, content string) (string, error) {
		if content != "new thought" {
			t.Errorf("capture content = %q", content)
		}
		now := time.Now().UTC()
		doc := &storage.Document{
			ID: "captured", Source: storage.SourceMarkdown, Path: "/inbox/captured.md",
			Title: "New thought", Content: content, ContentHash: "hash",
			IndexedAt: now, ModifiedAt: now,
		}
		return doc.Path, db.InsertDocument(ctx, doc)
	})

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	m := updated.(Model)
	if !m.capturing {
		t.Fatal("Ctrl+n did not enter capture mode")
	}
	m.captureInput.SetValue("new thought")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.capturing || cmd == nil {
		t.Fatalf("submitting capture: capturing=%v cmd=%v", m.capturing, cmd)
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.statusIsErr || m.statusMsg != "Captured: /inbox/captured.md" {
		t.Fatalf("capture status = %q (error=%v)", m.statusMsg, m.statusIsErr)
	}
	if len(m.results) == 0 || m.results[0].ID != "captured" {
		t.Fatalf("capture results = %+v", m.results)
	}
}

func TestSessionContextIsOrderedExcludedAndHistoryBounded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	docs := map[string]*storage.Document{
		"pin":     {ID: "pin", Title: "Pinned", Content: strings.Repeat("界", maxAnswerDocumentRunes+20)},
		"include": {ID: "include", Title: "Included", Content: "included"},
		"exclude": {ID: "exclude", Title: "Excluded", Content: "excluded"},
		"search":  {ID: "search", Title: "Search", Content: "search"},
	}
	turns := make([]*storage.SessionTurn, 6)
	for index := range turns {
		turns[index] = &storage.SessionTurn{Question: "q" + string(rune('0'+index)), Answer: "answer"}
	}
	model.SetSession(&storage.ResearchSession{ID: "session", Name: "Research"}, turns, []*storage.SessionDocument{
		{Document: docs["include"], State: storage.SessionDocumentIncluded},
		{Document: docs["exclude"], State: storage.SessionDocumentExcluded},
		{Document: docs["pin"], State: storage.SessionDocumentPinned},
	})
	model.SetProfile("work")
	if len(model.conversation) != maxConversationTurns || model.conversation[0].Question != "q2" {
		t.Fatalf("bounded resumed conversation = %+v", model.conversation)
	}
	candidates := model.answerCandidates([]*storage.Document{docs["exclude"], docs["search"], docs["include"]})
	if len(candidates) != 3 || candidates[0].ID != "pin" || candidates[1].ID != "include" || candidates[2].ID != "search" {
		t.Fatalf("session answer candidates = %+v", candidates)
	}
	contexts := buildAnswerContexts(candidates)
	if len([]rune(contexts[0])) != maxAnswerDocumentRunes || !strings.HasSuffix(contexts[0], "界") {
		t.Fatalf("unicode context length = %d", len([]rune(contexts[0])))
	}
	model.width, model.height = 120, 40
	if view := model.View(); !strings.Contains(view, "session: Research") || !strings.Contains(view, "profile: work") {
		t.Fatalf("session/profile identity missing from TUI:\n%s", view)
	}
}

func TestSessionKeysPersistDocumentStateAndTurnCitations(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	doc := &storage.Document{ID: "source", Source: storage.SourceMarkdown, Path: "/source.md", Title: "Source", Content: "text", ContentHash: "hash", IndexedAt: now, ModifiedAt: now}
	if err := db.InsertDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	session := &storage.ResearchSession{Name: "Research"}
	if err := db.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	model.SetSession(session, nil, nil)
	model.width, model.height = 120, 40
	model.updateViewportSize()
	model.panel = PanelResults
	model.results = []*storage.Document{doc}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	model = updated.(Model)
	stored, err := db.ListSessionDocuments(ctx, session.ID)
	if err != nil || len(stored) != 1 || stored[0].State != storage.SessionDocumentPinned {
		t.Fatalf("pinned session documents = %+v, err=%v", stored, err)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	model = updated.(Model)
	if model.sessionStates[doc.ID] != storage.SessionDocumentExcluded || !strings.Contains(model.preview.View(), "Session context: excluded") {
		t.Fatalf("excluded session state = %q, preview=%q", model.sessionStates[doc.ID], model.preview.View())
	}

	model.currentQuestion = "Question"
	model.answerText = "Generated answer [1]"
	model.currentSources = []*storage.Document{doc}
	if err := model.recordConversationTurn(); err != nil {
		t.Fatal(err)
	}
	turns, err := db.ListSessionTurns(ctx, session.ID)
	if err != nil || len(turns) != 1 || len(turns[0].Citations) != 1 || turns[0].Citations[0].DocumentID != doc.ID {
		t.Fatalf("persisted session turns = %+v, err=%v", turns, err)
	}
	model.contextTotal = 8
	model.showAnswer()
	if preview := model.preview.View(); !strings.Contains(preview, "Context: 1/8") || !strings.Contains(preview, "History: 1/1") {
		t.Fatalf("visible context bounds missing: %q", preview)
	}
}

func TestEphemeralConversationIsNotPersisted(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	model.currentQuestion = "Question"
	model.answerText = "Answer"
	if err := model.recordConversationTurn(); err != nil {
		t.Fatal(err)
	}
	if len(model.conversation) != 1 {
		t.Fatalf("ephemeral conversation = %+v", model.conversation)
	}
	model.resetEphemeralConversation()
	if len(model.conversation) != 0 {
		t.Fatalf("ephemeral conversation was not cleared: %+v", model.conversation)
	}
	sessions, err := db.ListSessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("ephemeral conversation persisted sessions = %+v, err=%v", sessions, err)
	}
}

func TestCollectionBrowserShowsAndClearsNewActivity(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	doc := &storage.Document{ID: "new-doc", Source: storage.SourceMarkdown, Path: "/new.md", Title: "New", ContentHash: "hash", IndexedAt: now, ModifiedAt: now}
	if err := db.InsertDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	collection := &storage.Collection{Name: "reading"}
	if err := db.CreateCollection(ctx, collection); err != nil {
		t.Fatal(err)
	}
	if err := db.AddToCollection(ctx, collection.ID, doc.ID); err != nil {
		t.Fatal(err)
	}
	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	model.panel = PanelResults
	model.results = []*storage.Document{doc}
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("collection browser did not load activity")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.collectionNewCounts[collection.ID] != 1 || !strings.Contains(model.renderCollectionsList(80, 20), "1 new") {
		t.Fatalf("collection activity = %#v, rendered=%q", model.collectionNewCounts, model.renderCollectionsList(80, 20))
	}
	updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("collection selection did not record view")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	if model.collectionNewCounts[collection.ID] != 0 || !strings.Contains(model.statusMsg, "1 new since last view") {
		t.Fatalf("viewed collection status = %q, activity=%#v", model.statusMsg, model.collectionNewCounts)
	}
	loaded, err := db.GetCollection(ctx, collection.ID)
	if err != nil || loaded.LastViewedAt == nil {
		t.Fatalf("collection last view = %+v, err=%v", loaded, err)
	}
}

func TestModelUpdateError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

	msg := errMsg{err: os.ErrNotExist}
	updated, _ := model.Update(msg)
	m := updated.(Model)

	if !m.statusIsErr {
		t.Error("statusIsErr should be true after error")
	}
}

func TestTagInputPersistsAndIndexesTag(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing database: %v", err)
		}
	}()
	searchIndex, err := search.NewBleveIndex(filepath.Join(tmpDir, "search.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := searchIndex.Close(); err != nil {
			t.Errorf("closing search index: %v", err)
		}
	}()

	ctx := context.Background()
	now := time.Now().UTC()
	doc := &storage.Document{
		ID: "tagged", Source: storage.SourceMarkdown, Path: "/tagged.md",
		Title: "Tagged", Content: "document", ContentHash: "hash",
		IndexedAt: now, ModifiedAt: now,
	}
	if err := db.InsertDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	if err := searchIndex.Index(ctx, doc); err != nil {
		t.Fatal(err)
	}

	model := New(db, searchIndex, nil, nil, privacy.Redactor{}, nil)
	model.results = []*storage.Document{doc}
	model.tagging = true
	model.tagInput.SetValue("favorite")
	updated, _ := model.updateTagInput(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.statusIsErr {
		t.Fatalf("tag input failed: %s", updated.statusMsg)
	}

	reloaded, err := db.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.TagsString(); got != "favorite" {
		t.Fatalf("persisted tags = %q, want favorite", got)
	}
	results, err := searchIndex.Search(ctx, "tag:favorite", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != doc.ID {
		t.Fatalf("tag search = %+v, want document %s", results, doc.ID)
	}
}

func TestModelToggleHelp(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	// Don't set width/height

	view := model.View()

	if view != "Loading..." {
		t.Errorf("View() = %q, want 'Loading...'", view)
	}
}

func TestModelViewHelp(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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
	model := New(db, nil, nil, llm, privacy.Redactor{}, nil)

	if model.llm != llm {
		t.Error("New() did not set LLM client")
	}
}

func TestSearchResultsWithAnswer(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)

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

func TestSearchResultsShowsAllStructuredFilters(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	parsed := query.ParsedQuery{Filters: filter.Set{
		Sources:       []storage.Source{storage.SourceBrowser},
		Tags:          []string{"project"},
		ExcludedTags:  []string{"archived"},
		Collections:   []string{"reading"},
		PathPrefixes:  []string{"Work/"},
		Domains:       []string{"arxiv.org"},
		Kinds:         []string{"bookmark"},
		ExactPhrases:  []string{"launch plan"},
		ExcludedTerms: []string{"draft"},
		RelativeTime:  "last week",
	}}
	updated, _ := model.Update(searchResultsMsg{parsed: parsed})
	status := updated.(Model).statusMsg
	for _, label := range parsed.Filters.Labels() {
		if !strings.Contains(status, "["+label+"]") {
			t.Errorf("statusMsg = %q, want active filter %q", status, label)
		}
	}
}

func TestShowAnswer(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
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

func TestStreamingErrorUpdatesStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db, nil, nil, nil, privacy.Redactor{}, nil)
	model.streaming = true
	wantErr := errors.New("backend unavailable")

	updated, _ := model.Update(streamChunkMsg{err: wantErr})
	got := updated.(Model)
	if got.streaming {
		t.Error("streaming should stop after an error")
	}
	if !got.statusIsErr {
		t.Error("streaming error should set error status")
	}
	if !strings.Contains(got.statusMsg, wantErr.Error()) {
		t.Fatalf("status = %q, want it to contain %q", got.statusMsg, wantErr)
	}
}
