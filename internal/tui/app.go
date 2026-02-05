package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
	"github.com/jankowtf/mindcli/internal/tui/styles"
)

// Panel represents which panel is focused.
type Panel int

const (
	PanelSearch Panel = iota
	PanelResults
	PanelPreview
)

// Model is the main application model.
type Model struct {
	// Database and search
	db     *storage.DB
	search *search.BleveIndex

	// UI Components
	searchInput textinput.Model
	preview     viewport.Model

	// State
	panel       Panel
	results     []*storage.Document
	cursor      int
	showHelp    bool
	statusMsg   string
	statusIsErr bool

	// Dimensions
	width  int
	height int

	// Keybindings
	keys KeyMap
}

// New creates a new Model with the given database and search index.
func New(db *storage.DB, searchIndex *search.BleveIndex) Model {
	ti := textinput.New()
	ti.Placeholder = "Search your knowledge base..."
	ti.PromptStyle = styles.SearchPromptStyle
	ti.TextStyle = styles.SearchInputStyle
	ti.PlaceholderStyle = styles.SearchPlaceholderStyle
	ti.Prompt = "  "
	ti.CharLimit = 256
	ti.Focus()

	vp := viewport.New(0, 0)

	return Model{
		db:          db,
		search:      searchIndex,
		searchInput: ti,
		preview:     vp,
		panel:       PanelSearch,
		keys:        DefaultKeyMap(),
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadDocuments(),
	)
}

// loadDocuments loads documents from the database.
func (m Model) loadDocuments() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		docs, err := m.db.ListDocuments(ctx, "")
		if err != nil {
			return errMsg{err}
		}
		return docsLoadedMsg{docs}
	}
}

// searchDocuments searches using Bleve and fetches documents from the database.
func (m Model) searchDocuments(query string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()

		// Use Bleve if available, fall back to SQLite LIKE search
		if m.search != nil {
			results, err := m.search.Search(ctx, query, 50)
			if err != nil {
				return errMsg{err}
			}

			// Fetch full documents from database
			docs := make([]*storage.Document, 0, len(results))
			for _, r := range results {
				doc, err := m.db.GetDocument(ctx, r.ID)
				if err != nil {
					continue // Skip documents that can't be found
				}
				docs = append(docs, doc)
			}
			return searchResultsMsg{docs}
		}

		// Fallback to simple SQLite search
		docs, err := m.db.SearchDocuments(ctx, query, 50)
		if err != nil {
			return errMsg{err}
		}
		return searchResultsMsg{docs}
	}
}

// Message types
type docsLoadedMsg struct {
	docs []*storage.Document
}

type searchResultsMsg struct {
	docs []*storage.Document
}

type errMsg struct {
	err error
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle global keys first
		switch {
		case key.Matches(msg, m.keys.Quit):
			if m.panel != PanelSearch || m.searchInput.Value() == "" {
				return m, tea.Quit
			}
			// Clear search if in search mode with text
			m.searchInput.SetValue("")
			return m, m.loadDocuments()

		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
			return m, nil

		case key.Matches(msg, m.keys.Tab):
			m.nextPanel()
			return m, nil

		case key.Matches(msg, m.keys.ShiftTab):
			m.prevPanel()
			return m, nil

		case key.Matches(msg, m.keys.Escape):
			if m.panel == PanelSearch && m.searchInput.Value() != "" {
				m.searchInput.SetValue("")
				return m, m.loadDocuments()
			}
			m.panel = PanelSearch
			m.searchInput.Focus()
			return m, nil
		}

		// Panel-specific handling
		switch m.panel {
		case PanelSearch:
			return m.updateSearch(msg)
		case PanelResults:
			return m.updateResults(msg)
		case PanelPreview:
			return m.updatePreview(msg)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateViewportSize()
		return m, nil

	case docsLoadedMsg:
		m.results = msg.docs
		m.cursor = 0
		m.statusMsg = fmt.Sprintf("%d documents", len(m.results))
		m.statusIsErr = false
		m.updatePreviewContent()
		return m, nil

	case searchResultsMsg:
		m.results = msg.docs
		m.cursor = 0
		m.statusMsg = fmt.Sprintf("%d results", len(m.results))
		m.statusIsErr = false
		m.updatePreviewContent()
		return m, nil

	case errMsg:
		m.statusMsg = msg.err.Error()
		m.statusIsErr = true
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

func (m Model) updateSearch(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Enter):
		query := m.searchInput.Value()
		if query == "" {
			return m, m.loadDocuments()
		}
		return m, m.searchDocuments(query)

	case key.Matches(msg, m.keys.Down):
		if len(m.results) > 0 {
			m.panel = PanelResults
			m.searchInput.Blur()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

func (m Model) updateResults(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		if m.cursor > 0 {
			m.cursor--
			m.updatePreviewContent()
		} else {
			// Move to search panel
			m.panel = PanelSearch
			m.searchInput.Focus()
		}
		return m, nil

	case key.Matches(msg, m.keys.Down):
		if m.cursor < len(m.results)-1 {
			m.cursor++
			m.updatePreviewContent()
		}
		return m, nil

	case key.Matches(msg, m.keys.Enter):
		m.panel = PanelPreview
		return m, nil

	case key.Matches(msg, m.keys.Search):
		m.panel = PanelSearch
		m.searchInput.Focus()
		return m, nil

	case key.Matches(msg, m.keys.GotoStart):
		m.cursor = 0
		m.updatePreviewContent()
		return m, nil

	case key.Matches(msg, m.keys.GotoEnd):
		if len(m.results) > 0 {
			m.cursor = len(m.results) - 1
			m.updatePreviewContent()
		}
		return m, nil
	}

	return m, nil
}

func (m Model) updatePreview(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Search):
		m.panel = PanelSearch
		m.searchInput.Focus()
		return m, nil
	}

	var cmd tea.Cmd
	m.preview, cmd = m.preview.Update(msg)
	return m, cmd
}

func (m *Model) nextPanel() {
	m.panel = (m.panel + 1) % 3
	m.updateFocus()
}

func (m *Model) prevPanel() {
	m.panel = (m.panel + 2) % 3
	m.updateFocus()
}

func (m *Model) updateFocus() {
	if m.panel == PanelSearch {
		m.searchInput.Focus()
	} else {
		m.searchInput.Blur()
	}
}

func (m *Model) updateViewportSize() {
	// Preview panel takes up about 40% of width
	previewWidth := m.width * 40 / 100
	previewHeight := m.height - 8 // Account for header, search, status
	if previewHeight < 1 {
		previewHeight = 1
	}
	m.preview.Width = previewWidth
	m.preview.Height = previewHeight
}

func (m *Model) updatePreviewContent() {
	if len(m.results) == 0 || m.cursor >= len(m.results) {
		m.preview.SetContent("No document selected")
		return
	}

	doc := m.results[m.cursor]
	var sb strings.Builder

	sb.WriteString(styles.PreviewTitleStyle.Render(doc.Title))
	sb.WriteString("\n")
	sb.WriteString(styles.ResultSourceStyle.Render(string(doc.Source)))
	sb.WriteString(" • ")
	sb.WriteString(styles.PreviewMetadataStyle.Render(doc.Path))
	sb.WriteString("\n\n")

	content := doc.Content
	if len(content) > 2000 {
		content = content[:2000] + "..."
	}
	sb.WriteString(styles.PreviewContentStyle.Render(content))

	m.preview.SetContent(sb.String())
}

// View renders the UI.
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	if m.showHelp {
		return m.renderHelp()
	}

	// Calculate layout
	resultsWidth := m.width*60/100 - 4
	previewWidth := m.width*40/100 - 4
	contentHeight := m.height - 6 // Header, search, status

	// Header
	header := styles.TitleStyle.Render("MindCLI") +
		styles.SubtitleStyle.Render(" - Personal Knowledge Search")

	// Search input
	searchStyle := styles.PanelStyle
	if m.panel == PanelSearch {
		searchStyle = styles.FocusedPanelStyle
	}
	searchBox := searchStyle.Width(m.width - 4).Render(m.searchInput.View())

	// Results panel
	resultsStyle := styles.PanelStyle.Width(resultsWidth).Height(contentHeight)
	if m.panel == PanelResults {
		resultsStyle = styles.FocusedPanelStyle.Width(resultsWidth).Height(contentHeight)
	}
	resultsContent := m.renderResults(resultsWidth-2, contentHeight-2)
	resultsPanel := resultsStyle.Render(
		styles.PanelTitleStyle.Render("Results") + "\n" + resultsContent,
	)

	// Preview panel
	previewStyle := styles.PanelStyle.Width(previewWidth).Height(contentHeight)
	if m.panel == PanelPreview {
		previewStyle = styles.FocusedPanelStyle.Width(previewWidth).Height(contentHeight)
	}
	m.preview.Width = previewWidth - 2
	m.preview.Height = contentHeight - 3
	previewPanel := previewStyle.Render(
		styles.PanelTitleStyle.Render("Preview") + "\n" + m.preview.View(),
	)

	// Content area (results + preview side by side)
	content := lipgloss.JoinHorizontal(lipgloss.Top, resultsPanel, previewPanel)

	// Status bar
	statusBar := m.renderStatusBar()

	// Compose final view
	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		searchBox,
		content,
		statusBar,
	)
}

func (m Model) renderResults(width, height int) string {
	if len(m.results) == 0 {
		return styles.ResultPreviewStyle.Render("No results. Press / to search.")
	}

	var sb strings.Builder
	visibleCount := height / 2 // Each result takes ~2 lines
	if visibleCount < 1 {
		visibleCount = 1
	}

	start := 0
	if m.cursor >= visibleCount {
		start = m.cursor - visibleCount + 1
	}
	end := start + visibleCount
	if end > len(m.results) {
		end = len(m.results)
	}

	for i := start; i < end; i++ {
		doc := m.results[i]

		title := doc.Title
		if title == "" {
			title = doc.Path
		}
		if len(title) > width-4 {
			title = title[:width-7] + "..."
		}

		var line string
		if i == m.cursor {
			line = styles.SelectedResultStyle.Render(title)
		} else {
			line = styles.ResultItemStyle.Render(title)
		}

		source := styles.SourceBadge(string(doc.Source)).Render(string(doc.Source))
		sb.WriteString(line + " " + source + "\n")
	}

	// Show scroll indicator
	if len(m.results) > visibleCount {
		sb.WriteString(fmt.Sprintf("\n%d/%d", m.cursor+1, len(m.results)))
	}

	return sb.String()
}

func (m Model) renderStatusBar() string {
	var status string
	if m.statusIsErr {
		status = styles.StatusErrorStyle.Render(m.statusMsg)
	} else {
		status = styles.StatusValueStyle.Render(m.statusMsg)
	}

	help := styles.HelpKeyStyle.Render("?") +
		styles.HelpDescStyle.Render(" help") +
		styles.HelpSeparatorStyle.Render(" • ") +
		styles.HelpKeyStyle.Render("q") +
		styles.HelpDescStyle.Render(" quit")

	return styles.StatusBarStyle.Render(
		status + strings.Repeat(" ", max(0, m.width-len(m.statusMsg)-len(" help • q quit")-10)) + help,
	)
}

func (m Model) renderHelp() string {
	var sb strings.Builder

	sb.WriteString(styles.TitleStyle.Render("Keyboard Shortcuts"))
	sb.WriteString("\n\n")

	helpItems := []struct {
		key  string
		desc string
	}{
		{"/", "Focus search"},
		{"Enter", "Execute search / Select item"},
		{"j/k or ↑/↓", "Navigate results"},
		{"Tab", "Cycle panels"},
		{"Shift+Tab", "Cycle panels (reverse)"},
		{"o", "Open in external app"},
		{"y", "Copy path to clipboard"},
		{"r", "Refresh index"},
		{"g/G", "Go to start/end"},
		{"Ctrl+u/d", "Half page up/down"},
		{"Esc", "Cancel / Clear search"},
		{"?", "Toggle help"},
		{"q", "Quit"},
	}

	for _, item := range helpItems {
		sb.WriteString(styles.HelpKeyStyle.Render(fmt.Sprintf("%12s", item.key)))
		sb.WriteString("  ")
		sb.WriteString(styles.HelpDescStyle.Render(item.desc))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(styles.HelpDescStyle.Render("Press ? to close help"))

	return styles.AppStyle.Render(sb.String())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
