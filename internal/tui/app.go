package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jankowtf/mindcli/internal/privacy"
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
	panel        Panel
	results      []*storage.Document
	cursor       int
	showHelp     bool
	statusMsg    string
	statusIsErr  bool
	answerText   string // LLM-generated answer for the current query
	tagging      bool   // true when tag input mode is active
	tagInput     textinput.Model
	collecting   bool // true when collection input mode is active
	collectInput textinput.Model
	redactor     privacy.Redactor

	highlights    map[string][]string // matching snippets per document ID
	searchVersion int                 // increments per keystroke for debouncing
	sourceFilter  storage.Source      // active source filter ("" = all sources)

	browsingCollections bool                  // true when browsing collections list
	collections         []*storage.Collection // loaded collections
	collectionCounts    map[string]int        // doc count per collection ID
	collectionCursor    int                   // cursor in collections list
	prevResults         []*storage.Document   // saved results before browsing
	streaming           bool                  // true while streaming LLM answer
	streamCh            chan streamChunkMsg   // channel for streaming tokens
	streamCancel        context.CancelFunc    // cancel in-flight stream

	// reindex runs a full index pass; nil disables in-app indexing.
	reindex  func(context.Context) (indexed int, errs int, err error)
	indexing bool // true while an in-app index pass is running

	currentQuestion string                   // question currently being answered
	conversation    []query.ConversationTurn // recent Q&A turns for follow-ups

	// Dimensions
	width  int
	height int

	// Keybindings
	keys KeyMap
}

// New creates a new Model with the given database and search index.
// The hybrid searcher and LLM client are optional; if nil, those features are
// skipped. reindex, when non-nil, enables the in-app "index now" action.
func New(db *storage.DB, searchIndex *search.BleveIndex, hybrid *query.HybridSearcher, llm *query.LLMClient, redactor privacy.Redactor, reindex func(context.Context) (int, int, error)) Model {
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

	collectTi := textinput.New()
	collectTi.Placeholder = "Enter collection name..."
	collectTi.CharLimit = 64

	return Model{
		db:           db,
		search:       searchIndex,
		hybrid:       hybrid,
		llm:          llm,
		searchInput:  ti,
		preview:      vp,
		tagInput:     tagTi,
		collectInput: collectTi,
		panel:        PanelSearch,
		keys:         DefaultKeyMap(),
		redactor:     redactor,
		reindex:      reindex,
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
	source := m.sourceFilter
	return func() tea.Msg {
		ctx := context.Background()
		docs, err := m.db.ListDocuments(ctx, source)
		if err != nil {
			return errMsg{err}
		}
		return docsLoadedMsg{docs}
	}
}

// searchDocuments searches using hybrid search (BM25 + vector) when available.
// It uses the query parser to extract intent, source filters, and time filters.
func (m Model) searchDocuments(q string, live bool) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		parsed := query.ParseQuery(q)

		// Build search query with source filter (from the NL query, or the
		// active filter toggled with 'f').
		searchQ := parsed.SearchTerms
		if parsed.SourceFilter != "" {
			searchQ = searchQ + " source:" + parsed.SourceFilter
		} else if m.sourceFilter != "" {
			searchQ = searchQ + " source:" + string(m.sourceFilter)
		}

		var docs []*storage.Document
		highlights := make(map[string][]string)

		// Use hybrid search if available
		if m.hybrid != nil {
			results, err := m.hybrid.Search(ctx, searchQ, 50)
			if err != nil {
				return errMsg{err}
			}
			docs = make([]*storage.Document, 0, len(results))
			for _, r := range results {
				docs = append(docs, r.Document)
				if len(r.Highlights) > 0 {
					highlights[r.Document.ID] = r.Highlights
				}
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
				for _, frags := range r.Highlights {
					highlights[doc.ID] = append(highlights[doc.ID], frags...)
				}
			}
		} else {
			// Fallback to simple SQLite search
			var err error
			docs, err = m.db.SearchDocuments(ctx, parsed.SearchTerms, 50)
			if err != nil {
				return errMsg{err}
			}
		}

		// Apply any parsed time filter (e.g. "last week").
		docs = query.FilterDocumentsByTime(docs, parsed, time.Now())

		return searchResultsMsg{docs: docs, highlights: highlights, parsed: parsed, live: live}
	}
}

// Message types
type docsLoadedMsg struct {
	docs []*storage.Document
}

type searchResultsMsg struct {
	docs       []*storage.Document
	highlights map[string][]string
	parsed     query.ParsedQuery
	live       bool // from search-as-you-type (suppresses LLM streaming)
}

type searchDebounceMsg struct {
	version int
	query   string
}

type errMsg struct {
	err error
}

type collectionsLoadedMsg struct {
	collections []*storage.Collection
	counts      map[string]int
}

type collectionDocsLoadedMsg struct {
	docs []*storage.Document
}

type streamChunkMsg struct {
	token string
	done  bool
	err   error
}

type reindexDoneMsg struct {
	indexed int
	errs    int
	err     error
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle modal input modes first
		if m.tagging {
			return m.updateTagInput(msg)
		}
		if m.collecting {
			return m.updateCollectInput(msg)
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
			m.conversation = nil
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
				m.conversation = nil
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
		m.highlights = nil
		m.cursor = 0
		m.statusMsg = fmt.Sprintf("%d documents", len(m.results))
		m.statusIsErr = false
		m.updatePreviewContent()
		return m, nil

	case searchResultsMsg:
		m.results = msg.docs
		m.highlights = msg.highlights
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
		// Start streaming if intent is answer/summarize (not for live,
		// keystroke-driven searches — only when the user commits with Enter).
		if !msg.live && m.llm != nil && len(m.results) > 0 &&
			(msg.parsed.Intent == query.IntentAnswer || msg.parsed.Intent == query.IntentSummarize) {
			m.currentQuestion = msg.parsed.Original
			m.showAnswer() // Shows "Thinking..."
			return m, m.startStreaming(msg.parsed.Original, m.results)
		}
		m.updatePreviewContent()
		return m, nil

	case streamChunkMsg:
		if msg.err != nil {
			m.streaming = false
			m.statusMsg = fmt.Sprintf("Answer generation failed: %v", msg.err)
			m.statusIsErr = true
			m.updatePreviewContent()
			return m, nil
		}
		if msg.done {
			m.streaming = false
			m.recordConversationTurn()
			m.showAnswer()
		} else {
			m.answerText += msg.token
			m.showAnswer()
			cmds = append(cmds, m.readNextChunk())
		}
		return m, tea.Batch(cmds...)

	case collectionsLoadedMsg:
		m.collections = msg.collections
		m.collectionCounts = msg.counts
		m.collectionCursor = 0
		if len(msg.collections) == 0 {
			m.statusMsg = "No collections found"
		} else {
			m.statusMsg = fmt.Sprintf("%d collections", len(msg.collections))
		}
		m.statusIsErr = false
		return m, nil

	case collectionDocsLoadedMsg:
		m.browsingCollections = false
		m.results = msg.docs
		m.cursor = 0
		m.statusMsg = fmt.Sprintf("%d documents in collection", len(msg.docs))
		m.statusIsErr = false
		m.updatePreviewContent()
		return m, nil

	case searchDebounceMsg:
		// Only act on the latest keystroke and only while editing the search.
		if msg.version != m.searchVersion || m.panel != PanelSearch {
			return m, nil
		}
		if strings.TrimSpace(msg.query) == "" {
			return m, m.loadDocuments()
		}
		return m, m.searchDocuments(msg.query, true)

	case reindexDoneMsg:
		m.indexing = false
		if msg.err != nil {
			m.statusMsg = "Index failed: " + msg.err.Error()
			m.statusIsErr = true
			return m, nil
		}
		m.statusMsg = fmt.Sprintf("Indexed %d documents (%d errors)", msg.indexed, msg.errs)
		m.statusIsErr = false
		return m, m.loadDocuments()

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
		return m, m.searchDocuments(query, false)

	case key.Matches(msg, m.keys.Down):
		if len(m.results) > 0 {
			m.panel = PanelResults
			m.searchInput.Blur()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)

	// Search-as-you-type: schedule a debounced search keyed by version so only
	// the latest keystroke triggers a query.
	m.searchVersion++
	v := m.searchVersion
	q := m.searchInput.Value()
	debounce := tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return searchDebounceMsg{version: v, query: q}
	})
	return m, tea.Batch(cmd, debounce)
}

func (m Model) updateResults(msg tea.KeyMsg) (Model, tea.Cmd) {
	// Handle collection browsing mode.
	if m.browsingCollections {
		return m.updateBrowseCollections(msg)
	}

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

	case key.Matches(msg, m.keys.PageDown):
		m.moveCursor(m.pageStep())
		return m, nil

	case key.Matches(msg, m.keys.PageUp):
		m.moveCursor(-m.pageStep())
		return m, nil

	case key.Matches(msg, m.keys.HalfDown):
		m.moveCursor(m.pageStep() / 2)
		return m, nil

	case key.Matches(msg, m.keys.HalfUp):
		m.moveCursor(-m.pageStep() / 2)
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

	case key.Matches(msg, m.keys.BrowseCollections):
		m.browsingCollections = true
		m.collectionCursor = 0
		m.prevResults = m.results
		m.statusMsg = "Loading collections..."
		m.statusIsErr = false
		return m, func() tea.Msg {
			ctx := context.Background()
			cols, err := m.db.ListCollections(ctx)
			if err != nil {
				return errMsg{err}
			}
			counts := make(map[string]int, len(cols))
			for _, c := range cols {
				counts[c.ID], _ = m.db.CountCollectionDocuments(ctx, c.ID)
			}
			return collectionsLoadedMsg{collections: cols, counts: counts}
		}

	case key.Matches(msg, m.keys.Collection):
		if m.cursor < len(m.results) {
			m.collecting = true
			m.collectInput.SetValue("")
			m.collectInput.Focus()
			m.statusMsg = "Enter collection name:"
			m.statusIsErr = false
		}
		return m, nil

	case key.Matches(msg, m.keys.Refresh):
		m.statusMsg = "Refreshing..."
		m.statusIsErr = false
		return m, m.loadDocuments()

	case key.Matches(msg, m.keys.Index):
		if m.reindex != nil && !m.indexing {
			m.indexing = true
			m.statusMsg = "Indexing..."
			m.statusIsErr = false
			return m, m.startReindex()
		}
		return m, nil

	case key.Matches(msg, m.keys.Filter):
		m.sourceFilter = nextSourceFilter(m.sourceFilter)
		if q := strings.TrimSpace(m.searchInput.Value()); q != "" {
			return m, m.searchDocuments(q, false)
		}
		return m, m.loadDocuments()
	}

	return m, nil
}

// sourceFilterCycle is the order the 'f' key rotates through ("" = all).
var sourceFilterCycle = []storage.Source{
	"", storage.SourceMarkdown, storage.SourcePDF, storage.SourceEmail,
	storage.SourceBrowser, storage.SourceClipboard,
}

func nextSourceFilter(current storage.Source) storage.Source {
	for i, s := range sourceFilterCycle {
		if s == current {
			return sourceFilterCycle[(i+1)%len(sourceFilterCycle)]
		}
	}
	return ""
}

// startReindex runs a full index pass in the background and reports completion.
func (m *Model) startReindex() tea.Cmd {
	reindex := m.reindex
	return func() tea.Msg {
		indexed, errs, err := reindex(context.Background())
		return reindexDoneMsg{indexed: indexed, errs: errs, err: err}
	}
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

func (m Model) updateBrowseCollections(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Up):
		if m.collectionCursor > 0 {
			m.collectionCursor--
		}
		return m, nil

	case key.Matches(msg, m.keys.Down):
		if m.collectionCursor < len(m.collections)-1 {
			m.collectionCursor++
		}
		return m, nil

	case key.Matches(msg, m.keys.Enter):
		if m.collectionCursor < len(m.collections) {
			col := m.collections[m.collectionCursor]
			// Smart collection: run the saved query instead of listing members.
			if strings.TrimSpace(col.Query) != "" {
				m.browsingCollections = false
				m.searchInput.SetValue(col.Query)
				return m, m.searchDocuments(col.Query, false)
			}
			return m, func() tea.Msg {
				ctx := context.Background()
				docs, err := m.db.GetCollectionDocuments(ctx, col.ID)
				if err != nil {
					return errMsg{err}
				}
				return collectionDocsLoadedMsg{docs}
			}
		}
		return m, nil

	case key.Matches(msg, m.keys.Escape):
		m.browsingCollections = false
		m.results = m.prevResults
		m.cursor = 0
		m.statusMsg = ""
		m.updatePreviewContent()
		return m, nil
	}

	return m, nil
}

func (m Model) updateCollectInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		name := strings.TrimSpace(m.collectInput.Value())
		if name != "" && m.cursor < len(m.results) {
			doc := m.results[m.cursor]
			ctx := context.Background()

			// Look up or create collection by name.
			col, err := m.db.GetCollectionByName(ctx, name)
			if err != nil {
				// Collection doesn't exist, create it.
				col = &storage.Collection{Name: name}
				if createErr := m.db.CreateCollection(ctx, col); createErr != nil {
					m.statusMsg = "Collection error: " + createErr.Error()
					m.statusIsErr = true
					m.collecting = false
					m.collectInput.Blur()
					return m, nil
				}
			}

			if err := m.db.AddToCollection(ctx, col.ID, doc.ID); err != nil {
				m.statusMsg = "Collection error: " + err.Error()
				m.statusIsErr = true
			} else {
				m.statusMsg = fmt.Sprintf("Added to collection %q", name)
				m.statusIsErr = false
				m.updatePreviewContent()
			}
		}
		m.collecting = false
		m.collectInput.Blur()
		return m, nil

	case tea.KeyEsc:
		m.collecting = false
		m.collectInput.Blur()
		m.statusMsg = ""
		return m, nil
	}

	var cmd tea.Cmd
	m.collectInput, cmd = m.collectInput.Update(msg)
	return m, cmd
}

// stripHighlightTags removes Bleve's HTML highlight markers from a fragment.
func stripHighlightTags(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	s = strings.ReplaceAll(s, "</mark>", "")
	return s
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
	_ = cmd.Run()
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

// pageStep returns the number of result rows that make up one page,
// derived from the visible results height (each row is ~2 lines).
func (m Model) pageStep() int {
	step := (m.height - 8) / 2
	if step < 1 {
		step = 1
	}
	return step
}

// moveCursor moves the results cursor by delta, clamping to range, and
// refreshes the preview.
func (m *Model) moveCursor(delta int) {
	if len(m.results) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.results)-1 {
		m.cursor = len(m.results) - 1
	}
	m.updatePreviewContent()
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
		sb.WriteString(styles.PreviewContentStyle.Render(m.redactor.Redact(m.answerText)))
	}
	if m.streaming {
		sb.WriteString(styles.ResultSourceStyle.Render(" \u2588")) // block cursor
	}
	sb.WriteString("\n\n")
	conf := query.EstimateAnswerConfidence(m.searchInput.Value(), m.answerContexts())
	sb.WriteString(styles.ResultSourceStyle.Render(
		fmt.Sprintf("Confidence: %s (%.2f)", strings.ToUpper(conf.Level), conf.Score),
	))
	sb.WriteString("\n")
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

	contexts := buildAnswerContexts(docs)
	history := m.conversation

	go func() {
		defer close(ch)
		err := m.llm.GenerateAnswerStreamWithHistory(ctx, question, contexts, history, func(token string, done bool) {
			select {
			case ch <- streamChunkMsg{token: token, done: done}:
			case <-ctx.Done():
			}
		})
		if err != nil {
			select {
			case ch <- streamChunkMsg{err: err}:
			case <-ctx.Done():
			}
		}
	}()

	return m.readNextChunk()
}

// recordConversationTurn appends the just-completed Q&A to the conversation
// history (capped to the last few turns) so follow-up questions have context.
func (m *Model) recordConversationTurn() {
	if m.currentQuestion == "" || m.answerText == "" {
		return
	}
	m.conversation = append(m.conversation, query.ConversationTurn{
		Question: m.currentQuestion,
		Answer:   m.answerText,
	})
	const maxTurns = 4
	if len(m.conversation) > maxTurns {
		m.conversation = m.conversation[len(m.conversation)-maxTurns:]
	}
}

func (m *Model) answerContexts() []string {
	return buildAnswerContexts(m.results)
}

func buildAnswerContexts(docs []*storage.Document) []string {
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
	return contexts
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
	// Show collection memberships.
	if cols, err := m.db.GetDocumentCollections(context.Background(), doc.ID); err == nil && len(cols) > 0 {
		for i, c := range cols {
			if i > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(styles.CollectionBadge(c.Name))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Show matching snippets (from search highlights) above the content.
	if frags := m.highlights[doc.ID]; len(frags) > 0 {
		sb.WriteString(styles.ResultSourceStyle.Render("Matches:"))
		sb.WriteString("\n")
		for i, frag := range frags {
			if i >= 3 {
				break
			}
			snippet := m.redactor.Redact(stripHighlightTags(frag))
			sb.WriteString(styles.PreviewContentStyle.Render("… " + strings.TrimSpace(snippet) + " …"))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	content := doc.Content
	if len(content) > 2000 {
		content = content[:2000] + "..."
	}
	content = m.redactor.Redact(content)
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
	resultsPanelTitle := "Results"
	if m.browsingCollections {
		resultsPanelTitle = "Collections"
	}
	resultsPanel := resultsStyle.Render(
		styles.PanelTitleStyle.Render(resultsPanelTitle) + "\n" + resultsContent,
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
	if m.browsingCollections {
		return m.renderCollectionsList(width, height)
	}

	if len(m.results) == 0 {
		if m.searchInput.Value() == "" && m.reindex != nil {
			return styles.ResultPreviewStyle.Render("No documents yet. Press i to index your sources, or / to search.")
		}
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
		fmt.Fprintf(&sb, "\n%d/%d", m.cursor+1, len(m.results))
	}

	return sb.String()
}

func (m Model) renderCollectionsList(width, height int) string {
	if len(m.collections) == 0 {
		return styles.ResultPreviewStyle.Render("No collections. Use 'c' to create one.")
	}

	var sb strings.Builder
	visibleCount := height / 2
	if visibleCount < 1 {
		visibleCount = 1
	}

	start := 0
	if m.collectionCursor >= visibleCount {
		start = m.collectionCursor - visibleCount + 1
	}
	end := start + visibleCount
	if end > len(m.collections) {
		end = len(m.collections)
	}

	for i := start; i < end; i++ {
		col := m.collections[i]
		label := fmt.Sprintf("%s (%d docs)", col.Name, m.collectionCounts[col.ID])
		if len(label) > width-4 {
			label = label[:width-7] + "..."
		}

		var line string
		if i == m.collectionCursor {
			line = styles.SelectedResultStyle.Render(label)
		} else {
			line = styles.ResultItemStyle.Render(label)
		}
		sb.WriteString(line + "\n")
	}

	if len(m.collections) > visibleCount {
		sb.WriteString(fmt.Sprintf("\n%d/%d", m.collectionCursor+1, len(m.collections)))
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
	if m.collecting {
		return styles.StatusBarStyle.Render(
			styles.HelpKeyStyle.Render("Collection: ") + m.collectInput.View() +
				styles.HelpDescStyle.Render("  (enter to save, esc to cancel)"),
		)
	}

	statusText := m.statusMsg
	if m.sourceFilter != "" {
		statusText = fmt.Sprintf("[%s] %s", m.sourceFilter, statusText)
	}

	var status string
	if m.statusIsErr {
		status = styles.StatusErrorStyle.Render(statusText)
	} else {
		status = styles.StatusValueStyle.Render(statusText)
	}

	help := styles.HelpKeyStyle.Render("?") +
		styles.HelpDescStyle.Render(" help") +
		styles.HelpSeparatorStyle.Render(" • ") +
		styles.HelpKeyStyle.Render("q") +
		styles.HelpDescStyle.Render(" quit")

	return styles.StatusBarStyle.Render(
		status + strings.Repeat(" ", max(0, m.width-lipgloss.Width(statusText)-lipgloss.Width(" help • q quit")-10)) + help,
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
		{"r", "Refresh list"},
		{"i", "Index sources now"},
		{"f", "Cycle source filter"},
		{"t", "Add tag"},
		{"c", "Add to collection"},
		{"C", "Browse collections"},
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
