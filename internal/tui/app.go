package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jankowtf/mindcli/internal/query"
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
	hybrid *query.HybridSearcher
	llm    *query.LLMClient

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
	answerText   string // LLM-generated answer for the current query
	tagging      bool   // true when tag input mode is active
	tagInput     textinput.Model
	streaming    bool               // true while streaming LLM answer
	streamCh     chan streamChunkMsg // channel for streaming tokens
	streamCancel context.CancelFunc // cancel in-flight stream

	// Dimensions
	width  int
	height int

	// Keybindings
	keys KeyMap
}

// New creates a new Model with the given database and search index.
// The hybrid searcher and LLM client are optional; if nil, those features are skipped.
func New(db *storage.DB, searchIndex *search.BleveIndex, hybrid *query.HybridSearcher, llm *query.LLMClient) Model {
	ti := textinput.New()
	ti.Placeholder = "Search your knowledge base..."
	ti.PromptStyle = styles.SearchPromptStyle
	ti.TextStyle = styles.SearchInputStyle
	ti.PlaceholderStyle = styles.SearchPlaceholderStyle
	ti.Prompt = "  "
	ti.CharLimit = 256
	ti.Focus()

	vp := viewport.New(0, 0)

	tagTi := textinput.New()
	tagTi.Placeholder = "Enter tag name..."
	tagTi.CharLimit = 64

	return Model{
		db:          db,
		search:      searchIndex,
		hybrid:      hybrid,
		llm:         llm,
		searchInput: ti,
		preview:     vp,
		tagInput:    tagTi,
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

// searchDocuments searches using hybrid search (BM25 + vector) when available.
// It uses the query parser to extract intent, source filters, and time filters.
func (m Model) searchDocuments(q string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		parsed := query.ParseQuery(q)

		// Build search query with source filter if detected.
		searchQ := parsed.SearchTerms
		if parsed.SourceFilter != "" {
			searchQ = searchQ + " source:" + parsed.SourceFilter
		}

		var docs []*storage.Document

		// Use hybrid search if available
		if m.hybrid != nil {
			results, err := m.hybrid.Search(ctx, searchQ, 50)
			if err != nil {
				return errMsg{err}
			}
			docs = make([]*storage.Document, 0, len(results))
			for _, r := range results {
				docs = append(docs, r.Document)
			}
		} else if m.search != nil {
			// Use Bleve, fall back to SQLite LIKE search
			results, err := m.search.Search(ctx, searchQ, 50)
			if err != nil {
				return errMsg{err}
			}

			docs = make([]*storage.Document, 0, len(results))
			for _, r := range results {
				doc, err := m.db.GetDocument(ctx, r.ID)
				if err != nil {
					continue
				}
				docs = append(docs, doc)
			}
		} else {
			// Fallback to simple SQLite search
			var err error
			docs, err = m.db.SearchDocuments(ctx, parsed.SearchTerms, 50)
			if err != nil {
				return errMsg{err}
			}
		}

		return searchResultsMsg{docs: docs, parsed: parsed}
	}
}

// Message types
type docsLoadedMsg struct {
	docs []*storage.Document
}

type searchResultsMsg struct {
	docs   []*storage.Document
	parsed query.ParsedQuery
}

type errMsg struct {
	err error
}

type streamChunkMsg struct {
	token string
	done  bool
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle tag input mode first
		if m.tagging {
			return m.updateTagInput(msg)
		}

		// Handle global keys first
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.cancelStream()
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
		m.answerText = ""
		status := fmt.Sprintf("%d results", len(m.results))
		if msg.parsed.SourceFilter != "" {
			status += fmt.Sprintf(" [source:%s]", msg.parsed.SourceFilter)
		}
		if msg.parsed.TimeFilter != "" {
			status += fmt.Sprintf(" [%s]", msg.parsed.TimeFilter)
		}
		m.statusMsg = status
		m.statusIsErr = false
		// Start streaming if intent is answer/summarize
		if m.llm != nil && len(m.results) > 0 &&
			(msg.parsed.Intent == query.IntentAnswer || msg.parsed.Intent == query.IntentSummarize) {
			m.showAnswer() // Shows "Thinking..."
			return m, m.startStreaming(msg.parsed.Original, m.results)
		}
		m.updatePreviewContent()
		return m, nil

	case streamChunkMsg:
		if msg.done {
			m.streaming = false
			m.showAnswer()
		} else {
			m.answerText += msg.token
			m.showAnswer()
			cmds = append(cmds, m.readNextChunk())
		}
		return m, tea.Batch(cmds...)

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
		m.cancelStream()
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

	case key.Matches(msg, m.keys.Open):
		if m.cursor < len(m.results) {
			doc := m.results[m.cursor]
			if doc.Path != "" && !strings.HasPrefix(doc.Path, "clipboard:") {
				go openFile(doc.Path)
				m.statusMsg = "Opening: " + doc.Path
				m.statusIsErr = false
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Copy):
		if m.cursor < len(m.results) {
			doc := m.results[m.cursor]
			if err := clipboard.WriteAll(doc.Path); err != nil {
				m.statusMsg = "Copy failed: " + err.Error()
				m.statusIsErr = true
			} else {
				m.statusMsg = "Copied: " + doc.Path
				m.statusIsErr = false
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Tag):
		if m.cursor < len(m.results) {
			m.tagging = true
			m.tagInput.SetValue("")
			m.tagInput.Focus()
			m.statusMsg = "Enter tag for: " + m.results[m.cursor].Title
			m.statusIsErr = false
		}
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		m.statusMsg = "Refreshing..."
		m.statusIsErr = false
		return m, m.loadDocuments()
	}

	return m, nil
}

func (m Model) updateTagInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		tag := strings.TrimSpace(m.tagInput.Value())
		if tag != "" && m.cursor < len(m.results) {
			doc := m.results[m.cursor]
			ctx := context.Background()
			if err := m.db.AddTag(ctx, doc.ID, tag); err != nil {
				m.statusMsg = "Tag error: " + err.Error()
				m.statusIsErr = true
			} else {
				m.statusMsg = fmt.Sprintf("Added tag %q to %s", tag, doc.Title)
				m.statusIsErr = false
				// Update metadata to reflect the new tag immediately
				if doc.Metadata == nil {
					doc.Metadata = make(map[string]string)
				}
				existing := doc.Metadata["tags"]
				if existing != "" {
					doc.Metadata["tags"] = existing + "," + tag
				} else {
					doc.Metadata["tags"] = tag
				}
				m.updatePreviewContent()
			}
		}
		m.tagging = false
		m.tagInput.Blur()
		return m, nil

	case tea.KeyEsc:
		m.tagging = false
		m.tagInput.Blur()
		m.statusMsg = ""
		return m, nil
	}

	var cmd tea.Cmd
	m.tagInput, cmd = m.tagInput.Update(msg)
	return m, cmd
}

// openFile opens a file with the system's default application.
func openFile(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "linux":
		cmd = exec.Command("xdg-open", path)
	default:
		return
	}
	cmd.Run()
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

func (m *Model) showAnswer() {
	var sb strings.Builder
	sb.WriteString(styles.PreviewTitleStyle.Render("Answer"))
	sb.WriteString("\n\n")
	if m.answerText == "" && m.streaming {
		sb.WriteString(styles.PreviewContentStyle.Render("Thinking..."))
	} else {
		sb.WriteString(styles.PreviewContentStyle.Render(m.answerText))
	}
	if m.streaming {
		sb.WriteString(styles.ResultSourceStyle.Render(" \u2588")) // block cursor
	}
	sb.WriteString("\n\n")
	sb.WriteString(styles.ResultSourceStyle.Render(fmt.Sprintf("Based on %d sources", min(5, len(m.results)))))
	m.preview.SetContent(sb.String())
}

func (m *Model) startStreaming(question string, docs []*storage.Document) tea.Cmd {
	// Cancel any existing stream.
	if m.streamCancel != nil {
		m.streamCancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.streamCancel = cancel
	m.streaming = true
	m.answerText = ""

	ch := make(chan streamChunkMsg, 64)
	m.streamCh = ch

	// Build contexts from top 5 docs.
	contexts := make([]string, 0, 5)
	for i, doc := range docs {
		if i >= 5 {
			break
		}
		content := doc.Content
		if len(content) > 1000 {
			content = content[:1000]
		}
		contexts = append(contexts, content)
	}

	go func() {
		defer close(ch)
		m.llm.GenerateAnswerStream(ctx, question, contexts, func(token string, done bool) {
			select {
			case ch <- streamChunkMsg{token: token, done: done}:
			case <-ctx.Done():
			}
		})
	}()

	return m.readNextChunk()
}

func (m *Model) cancelStream() {
	if m.streaming && m.streamCancel != nil {
		m.streamCancel()
		m.streaming = false
	}
}

func (m *Model) readNextChunk() tea.Cmd {
	ch := m.streamCh
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return streamChunkMsg{done: true}
		}
		return chunk
	}
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
	sb.WriteString("\n")
	if tags := doc.Metadata["tags"]; tags != "" {
		sb.WriteString("Tags: " + tags + "\n")
	}
	sb.WriteString("\n")

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
		var tagStr string
		if tags := doc.Metadata["tags"]; tags != "" {
			for _, t := range strings.Split(tags, ",") {
				tagStr += " " + styles.TagBadge(strings.TrimSpace(t))
			}
		}
		sb.WriteString(line + " " + source + tagStr + "\n")
	}

	// Show scroll indicator
	if len(m.results) > visibleCount {
		sb.WriteString(fmt.Sprintf("\n%d/%d", m.cursor+1, len(m.results)))
	}

	return sb.String()
}

func (m Model) renderStatusBar() string {
	if m.tagging {
		return styles.StatusBarStyle.Render(
			styles.HelpKeyStyle.Render("Tag: ") + m.tagInput.View() +
				styles.HelpDescStyle.Render("  (enter to save, esc to cancel)"),
		)
	}

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
		{"t", "Add tag"},
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
