package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

	model := New(db)

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

	model := New(db)
	cmd := model.Init()

	if cmd == nil {
		t.Error("Init() returned nil cmd")
	}
}

func TestModelUpdateWindowSize(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db)

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

	model := New(db)

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

	model := New(db)

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

	model := New(db)

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

	model := New(db)

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

	model := New(db)
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

	model := New(db)
	// Don't set width/height

	view := model.View()

	if view != "Loading..." {
		t.Errorf("View() = %q, want 'Loading...'", view)
	}
}

func TestModelViewHelp(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	model := New(db)
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

	model := New(db)
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

	model := New(db)
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

	model := New(db)
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

	model := New(db)

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
