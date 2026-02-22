# MindCLI Implementation Plan

A fast, private TUI for personal knowledge management with AI-powered search.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         MindCLI TUI                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────────┐ │
│  │ Search   │  │ Results  │  │ Preview  │  │ Status/Progress  │ │
│  │ Input    │  │ List     │  │ Panel    │  │ Bar              │ │
│  └──────────┘  └──────────┘  └──────────┘  └──────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Query Engine                              │
│  ┌─────────────────┐    ┌─────────────────┐                     │
│  │ Natural Language│───▶│ Hybrid Search   │                     │
│  │ Parser          │    │ (BM25 + Vector) │                     │
│  └─────────────────┘    └─────────────────┘                     │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Storage Layer                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │ SQLite      │  │ Vector      │  │ Full-Text Index (Bleve) │  │
│  │ (metadata)  │  │ Store       │  │                         │  │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                              ▲
                              │
┌─────────────────────────────────────────────────────────────────┐
│                    Indexing Pipeline                            │
│  ┌────────┐ ┌────────┐ ┌────────┐ ┌────────┐ ┌────────────────┐ │
│  │ Notes  │ │ PDFs   │ │ Email  │ │ Clip-  │ │ Browser        │ │
│  │ (.md)  │ │        │ │        │ │ board  │ │ History        │ │
│  └────────┘ └────────┘ └────────┘ └────────┘ └────────────────┘ │
│       │         │          │          │              │          │
│       └─────────┴──────────┴──────────┴──────────────┘          │
│                            │                                    │
│                            ▼                                    │
│               ┌─────────────────────────┐                       │
│               │   Concurrent Workers    │                       │
│               │   (Goroutine Pool)      │                       │
│               └─────────────────────────┘                       │
└─────────────────────────────────────────────────────────────────┘
```

---

## Project Structure

```
mindcli/
├── cmd/
│   └── mindcli/
│       └── main.go              # Entry point
├── internal/
│   ├── tui/
│   │   ├── app.go               # Main Bubble Tea application
│   │   ├── components/
│   │   │   ├── search.go        # Search input component
│   │   │   ├── results.go       # Results list component
│   │   │   ├── preview.go       # Content preview component
│   │   │   └── status.go        # Status bar component
│   │   ├── styles/
│   │   │   └── styles.go        # Lip Gloss styles
│   │   └── keys.go              # Keybindings
│   ├── index/
│   │   ├── indexer.go           # Main indexer orchestrator
│   │   ├── worker.go            # Worker pool implementation
│   │   ├── watcher.go           # File system watcher
│   │   └── sources/
│   │       ├── source.go        # Source interface
│   │       ├── markdown.go      # Markdown/notes indexer
│   │       ├── pdf.go           # PDF indexer
│   │       ├── email.go         # Email indexer (mbox/maildir)
│   │       ├── clipboard.go     # Clipboard history
│   │       └── browser.go       # Browser history (Chrome/Firefox/Safari)
│   ├── storage/
│   │   ├── sqlite.go            # SQLite for metadata
│   │   ├── vector.go            # Vector storage (hnswlib or custom)
│   │   └── bleve.go             # Full-text search index
│   ├── embeddings/
│   │   ├── embedder.go          # Embedding interface
│   │   ├── ollama.go            # Ollama local embeddings
│   │   ├── openai.go            # OpenAI embeddings (optional)
│   │   └── cache.go             # Embedding cache
│   ├── query/
│   │   ├── parser.go            # Natural language query parser
│   │   ├── hybrid.go            # Hybrid search (BM25 + semantic)
│   │   └── reranker.go          # Result reranking
│   └── config/
│       └── config.go            # Configuration management
├── pkg/
│   └── chunker/
│       └── chunker.go           # Text chunking utilities
├── go.mod
├── go.sum
├── config.yaml                  # Default configuration
├── Makefile
└── README.md
```

---

## Implementation Phases

### Phase 1: Foundation

**Goal:** Basic TUI shell with SQLite storage

1. **Project Setup**
   - Initialize Go module
   - Set up directory structure
   - Add core dependencies:
     - `github.com/charmbracelet/bubbletea` - TUI framework
     - `github.com/charmbracelet/lipgloss` - Styling
     - `github.com/charmbracelet/bubbles` - UI components
     - `github.com/mattn/go-sqlite3` - SQLite

2. **Basic TUI**
   - Create main Bubble Tea model
   - Implement three-panel layout (search, results, preview)
   - Add basic keybindings (quit, navigate, select)
   - Style with Lip Gloss

3. **SQLite Storage**
   - Design schema for documents table
   - Implement CRUD operations
   - Add migration system

**Deliverable:** Running TUI that can display mock data

---

### Phase 2: Markdown Indexing

**Goal:** Index and search markdown notes

1. **File Scanner**
   - Recursive directory walker
   - File type detection
   - Modification time tracking (incremental indexing)

2. **Markdown Parser**
   - Extract frontmatter (YAML)
   - Parse headings, links, tags
   - Preserve structure for preview

3. **Full-Text Search**
   - Integrate Bleve for FTS
   - Implement BM25 ranking
   - Wire search to TUI

4. **Concurrent Indexing**
   - Worker pool with configurable size
   - Progress reporting to TUI
   - Graceful cancellation

**Deliverable:** Search and preview markdown files

---

### Phase 3: Semantic Search

**Goal:** Add embedding-based vector search

1. **Embedding Integration**
   - Ollama client for local models (nomic-embed-text, mxbai-embed-large)
   - Chunking strategy (sliding window, semantic boundaries)
   - Batch processing for efficiency

2. **Vector Storage**
   - Implement HNSW index (via `github.com/coder/hnsw` or custom)
   - Cosine similarity search
   - Persistence to disk

3. **Hybrid Search**
   - Combine BM25 + vector scores
   - Reciprocal Rank Fusion (RRF) for merging
   - Configurable weights

4. **Embedding Cache**
   - Content-hash based caching
   - Avoid re-embedding unchanged content

**Deliverable:** Semantic search "what were my thoughts on productivity"

---

### Phase 4: PDF Support

**Goal:** Index PDF documents

1. **PDF Parser**
   - Use `github.com/ledongthuc/pdf` or `pdfcpu`
   - Extract text layer
   - Handle OCR fallback (optional, via external tool)

2. **PDF Preview**
   - Text extraction for preview panel
   - Page-aware chunking
   - Link to open in external viewer

**Deliverable:** Search across PDF library

---

### Phase 5: Email Integration

**Goal:** Index local email archives

1. **Email Parsers**
   - Mbox format (Thunderbird exports)
   - Maildir format
   - Apple Mail `.emlx` files

2. **Email Processing**
   - Parse headers (from, to, subject, date)
   - Extract body (plain text, strip HTML)
   - Handle attachments metadata

3. **Privacy Controls**
   - Configurable folders to index
   - Exclusion patterns
   - Sensitive field masking in preview

**Deliverable:** Search emails alongside notes

---

### Phase 6: Browser History

**Goal:** Index browser history and bookmarks

1. **Browser Databases**
   - Chrome: `~/.config/google-chrome/Default/History` (SQLite)
   - Firefox: `~/.mozilla/firefox/*.default/places.sqlite`
   - Safari: `~/Library/Safari/History.db`

2. **History Extraction**
   - URL, title, visit count, timestamps
   - Bookmarks with tags/folders
   - Handle locked database (copy first)

3. **Optional: Page Content**
   - Fetch and index page content (user opt-in)
   - Respect robots.txt
   - Rate limiting

**Deliverable:** "What was that article I read about X"

---

### Phase 7: Clipboard History

**Goal:** Track and index clipboard contents

1. **Clipboard Monitor**
   - Platform-specific clipboard access
   - Polling or event-based monitoring
   - Deduplication

2. **Content Types**
   - Plain text (primary)
   - URLs (extract and optionally fetch)
   - Image OCR (optional, via external tool)

3. **Privacy**
   - Password detection heuristics (skip indexing)
   - Configurable retention period
   - Manual clear option

**Deliverable:** Find "that thing I copied"

---

### Phase 8: File Watcher

**Goal:** Real-time index updates

1. **FSNotify Integration**
   - Watch configured directories
   - Debounce rapid changes
   - Handle rename/move events

2. **Incremental Updates**
   - Update only changed documents
   - Remove deleted documents
   - Batch updates for efficiency

3. **Background Service**
   - Optional daemon mode
   - System tray integration (future)

**Deliverable:** Index stays current automatically

---

### Phase 9: LLM Integration

**Goal:** Natural language query understanding

1. **Query Parser**
   - Intent detection (search, summarize, compare)
   - Entity extraction (dates, topics, sources)
   - Query expansion

2. **Conversational Search**
   - Follow-up questions
   - Context retention
   - "Tell me more about this"

3. **Answer Generation**
   - RAG-style answers from indexed content
   - Source attribution
   - Confidence indicators

**Deliverable:** "Summarize what I wrote about Go concurrency last month"

---

### Phase 10: Polish

**Goal:** Production-ready release

1. **Performance**
   - Profiling and optimization
   - Memory usage tuning
   - Lazy loading for large result sets

2. **Configuration**
   - YAML config file
   - CLI flags
   - Environment variables
   - Sensible defaults
   - Privacy redaction patterns

3. **Documentation**
   - README with screenshots
   - Configuration guide
   - Architecture docs

4. **Distribution**
   - Homebrew formula
   - Release binaries (goreleaser)
   - Install script

---

## Data Models

### Document

```go
type Document struct {
    ID          string    `db:"id"`           // UUID or content hash
    Source      string    `db:"source"`       // "markdown", "pdf", "email", "browser", "clipboard"
    Path        string    `db:"path"`         // File path or URL
    Title       string    `db:"title"`        // Extracted title
    Content     string    `db:"content"`      // Full text content
    Preview     string    `db:"preview"`      // First ~500 chars
    Metadata    JSON      `db:"metadata"`     // Source-specific metadata
    ContentHash string    `db:"content_hash"` // For change detection
    IndexedAt   time.Time `db:"indexed_at"`
    ModifiedAt  time.Time `db:"modified_at"`
}
```

### Chunk (for embeddings)

```go
type Chunk struct {
    ID         string    `db:"id"`
    DocumentID string    `db:"document_id"`
    Content    string    `db:"content"`
    Embedding  []float32 `db:"-"`  // Stored in vector index
    StartPos   int       `db:"start_pos"`
    EndPos     int       `db:"end_pos"`
}
```

### SearchResult

```go
type SearchResult struct {
    Document    *Document
    Score       float64   // Combined score
    BM25Score   float64
    VectorScore float64
    Highlights  []string  // Matching snippets
    ChunkID     string    // Which chunk matched (for vector search)
}
```

---

## Key Technical Decisions

### TUI Framework: Bubble Tea
- Elm-architecture fits well for reactive UIs
- Rich ecosystem (Lip Gloss, Bubbles)
- Good performance for terminal rendering

### Vector Search: HNSW
- Fast approximate nearest neighbor
- Good recall/speed tradeoff
- Pure Go implementations available

### Full-Text Search: Bleve
- Pure Go, no CGO required (optional)
- BM25 ranking out of the box
- Faceted search for filtering

### Embeddings: Ollama (default)
- Local, private, no API keys
- Good models available (nomic-embed-text)
- Fallback to OpenAI for users who prefer it

### Database: SQLite
- Single file, portable
- ACID compliant
- Good enough for personal scale

---

## Concurrency Model

```go
// Worker pool for indexing
type IndexerPool struct {
    workers   int
    jobs      chan IndexJob
    results   chan IndexResult
    wg        sync.WaitGroup
    ctx       context.Context
    cancel    context.CancelFunc
}

// Each source runs as separate goroutine
func (p *IndexerPool) Start() {
    for i := 0; i < p.workers; i++ {
        p.wg.Add(1)
        go p.worker(i)
    }
}

// Progress reported via channel
type IndexProgress struct {
    Source     string
    Total      int
    Processed  int
    Current    string
    Errors     []error
}
```

---

## Configuration

```yaml
# ~/.config/mindcli/config.yaml

sources:
  markdown:
    enabled: true
    paths:
      - ~/notes
      - ~/Documents/obsidian
    extensions: [".md", ".txt"]
    ignore: ["node_modules", ".git"]

  pdf:
    enabled: true
    paths:
      - ~/Documents/papers
      - ~/Books

  email:
    enabled: false  # Opt-in
    paths:
      - ~/Mail
    formats: ["mbox", "maildir"]

  browser:
    enabled: true
    browsers: ["chrome", "firefox"]
    include_content: false  # Just titles/URLs

  clipboard:
    enabled: true
    retention_days: 30
    skip_passwords: true

embeddings:
  provider: ollama  # or "openai"
  model: nomic-embed-text
  ollama_url: http://localhost:11434

search:
  hybrid_weight: 0.5  # 0 = pure BM25, 1 = pure vector
  results_limit: 50

indexing:
  workers: 4
  watch: true

storage:
  path: ~/.local/share/mindcli
```

---

## Keybindings

| Key | Action |
|-----|--------|
| `/` | Focus search |
| `Enter` | Execute search / Open selected |
| `j/k` or `↓/↑` | Navigate results |
| `Tab` | Cycle panels |
| `o` | Open in external app |
| `y` | Copy to clipboard |
| `r` | Refresh index |
| `?` | Help |
| `q` / `Ctrl+C` | Quit |

---

## Dependencies

```go
// Core
github.com/charmbracelet/bubbletea    // TUI framework
github.com/charmbracelet/lipgloss     // Styling
github.com/charmbracelet/bubbles      // Components

// Storage
github.com/mattn/go-sqlite3           // SQLite driver
github.com/blevesearch/bleve/v2       // Full-text search

// Vector Search
github.com/coder/hnsw                 // HNSW implementation

// File Processing
github.com/ledongthuc/pdf             // PDF parsing
github.com/yuin/goldmark              // Markdown parsing
github.com/fsnotify/fsnotify          // File watching

// Embeddings
github.com/ollama/ollama/api          // Ollama client

// Utilities
github.com/spf13/viper                // Configuration
github.com/google/uuid                // UUIDs
golang.org/x/sync/errgroup            // Concurrency
```

---

## Success Metrics

1. **Performance**
   - Index 10,000 documents in < 5 minutes
   - Search results in < 100ms
   - TUI renders at 60fps

2. **Usability**
   - First useful search within 30 seconds of install
   - Zero configuration required for basic use
   - Intuitive keyboard-driven interface

3. **Privacy**
   - All data stays local by default
   - No telemetry
   - Sensitive content detection

---

## Future Enhancements

- **Tagging system** - Manual tags in addition to auto-extracted
- **Collections** - Save searches and group related items
- **Sync** - Optional encrypted sync between machines
- **Mobile companion** - Quick capture app
- **Plugin system** - Custom sources and processors
- **Graph view** - Visualize connections between documents
- **Export** - Generate reports from search results
