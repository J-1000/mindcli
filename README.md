# MindCLI

A fast, private TUI for personal knowledge management with AI-powered search.

Search across your notes, PDFs, emails, browser history, and clipboard — all from a single keyboard-driven interface. Everything stays local.

## Features

- **Multi-source indexing** — Markdown notes, PDFs, emails (mbox/maildir/emlx), browser history (Chrome/Firefox/Safari), clipboard
- **Hybrid search** — BM25 full-text search + semantic vector search with Reciprocal Rank Fusion
- **Local AI** — Embeddings and streaming LLM answers via Ollama (no API keys, no cloud)
- **Beautiful TUI** — Three-panel Bubble Tea interface with live preview and real-time streaming
- **Export** — Search results to JSON, CSV, or Markdown
- **Tagging** — Manual tags on any document, displayed in TUI and searchable
- **Collections** — Named groups of documents (like playlists), with CLI and TUI management
- **Fast** — Concurrent worker pool indexing, incremental updates, content-hash caching
- **File watcher** — Real-time re-indexing via fsnotify with debouncing
- **Private** — All data stays on your machine, password detection for clipboard

## Installation

```bash
# Homebrew (tap)
brew tap jankowtf/homebrew-tap
brew install mindcli

# Quick install script (latest release binary)
curl -fsSL https://raw.githubusercontent.com/jankowtf/mindcli/main/scripts/install.sh | sh

# Build from source
go install github.com/jankowtf/mindcli/cmd/mindcli@latest

# Or clone and build
git clone https://github.com/jankowtf/mindcli.git
cd mindcli
make build
```

**Requirements:** Go 1.25+ and CGO enabled (for SQLite). Optional: [Ollama](https://ollama.ai) for semantic search and LLM features.

The install script supports:
- `VERSION=vX.Y.Z` to pin a release
- `INSTALL_DIR=/custom/bin` to customize install location
- `MINDCLI_REPO=owner/repo` to install from a fork

## Quick Start

```bash
# 1. Initialize config (optional — sensible defaults are used otherwise)
mindcli config

# 2. Index your documents
mindcli index

# 3. Launch the TUI
mindcli
```

## Usage

```bash
mindcli                                      # Start the TUI
mindcli index                                # Index all configured sources
mindcli index -paths ~/notes                 # Index specific paths
mindcli index -watch                         # Index then watch for changes
mindcli reindex                              # Full rebuild (e.g. after model change)
mindcli watch                                # Watch directories for changes
mindcli search "Go concurrency"              # Search and print results
mindcli stats                                # Show index statistics
mindcli clean                                # Remove docs whose files are gone
mindcli doctor                               # Check config and service health
mindcli export "Go" --format json            # Export results as JSON/CSV/Markdown
mindcli tag add ~/notes/foo.md mytag         # Add a tag to a document
mindcli tag list                             # List all tags
mindcli clipboard clear                      # Remove all indexed clipboard entries
mindcli clipboard cleanup                    # Remove old indexed clipboard entries
mindcli collection create "reading-list"     # Create a collection
mindcli collection add reading-list ~/f.md   # Add a document to a collection
mindcli collection list                      # List all collections
mindcli collection show reading-list         # Show collection details and documents
mindcli ask "what did I write about Go?"     # Ask a question (streaming RAG via Ollama)
mindcli config                               # Initialize default config file
mindcli version                              # Show version info
mindcli help                                 # Show help
```

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `/` | Focus search |
| `Enter` | Execute search / Select |
| `j/k` or `Up/Down` | Navigate results |
| `Tab` / `Shift+Tab` | Cycle panels |
| `o` | Open in external app |
| `y` | Copy file path to clipboard |
| `r` | Refresh document list |
| `i` | Index sources now (in-app) |
| `t` | Add tag to selected document |
| `c` | Add to collection |
| `C` | Browse collections |
| `g` / `G` | Go to start / end of results |
| `Ctrl+u` / `Ctrl+d` | Half page up / down (preview) |
| `PgUp` / `PgDn` | Page up / down |
| `Esc` | Clear search / Cancel |
| `?` | Toggle help |
| `q` / `Ctrl+C` | Quit |

## Configuration

MindCLI looks for `~/.config/mindcli/config.yaml`. Run `mindcli config` to generate a default config file.

Environment variables can override config values at runtime. Useful examples:
- `MINDCLI_CONFIG_PATH` (custom config file path)
- `MINDCLI_CONFIG_DIR` (custom config directory)
- `MINDCLI_STORAGE_PATH`
- `MINDCLI_INDEXING_WORKERS`, `MINDCLI_INDEXING_WATCH`
- `MINDCLI_SEARCH_HYBRID_WEIGHT`, `MINDCLI_SEARCH_RESULTS_LIMIT`
- `MINDCLI_EMBEDDINGS_PROVIDER`, `MINDCLI_EMBEDDINGS_MODEL`, `MINDCLI_EMBEDDINGS_LLM_MODEL`, `MINDCLI_EMBEDDINGS_OLLAMA_URL`, `MINDCLI_EMBEDDINGS_OPENAI_KEY`
- `MINDCLI_SOURCES_MARKDOWN_PATHS`, `MINDCLI_SOURCES_PDF_PATHS`, `MINDCLI_SOURCES_EMAIL_PATHS`, `MINDCLI_SOURCES_EMAIL_IGNORE`, `MINDCLI_SOURCES_EMAIL_MASK_SENSITIVE_PREVIEW`, `MINDCLI_SOURCES_BROWSER_BROWSERS`
- `MINDCLI_PRIVACY_REDACT_PATTERNS`, `MINDCLI_PRIVACY_REDACT_CONTENT`

```yaml
sources:
  markdown:
    enabled: true
    paths:
      - ~/notes
    extensions: [".md", ".txt"]
    ignore: ["node_modules", ".git", ".obsidian"]

  pdf:
    enabled: true
    paths:
      - ~/Documents

  email:
    enabled: false
    paths: []
    formats: ["mbox", "maildir"]
    ignore: []
    mask_sensitive_preview: true

  browser:
    enabled: true
    browsers: ["chrome", "firefox", "safari"]
    include_content: false

  clipboard:
    enabled: true
    retention_days: 30
    skip_passwords: true

embeddings:
  provider: ollama       # or "openai"
  model: nomic-embed-text
  llm_model: llama3.2   # model for answer generation
  ollama_url: http://localhost:11434
  # For provider: openai, set openai_key (required) and use OpenAI models, e.g.
  # model: text-embedding-3-small, llm_model: gpt-4o-mini. Override the endpoint
  # with the OPENAI_BASE_URL env var to target an OpenAI-compatible server.
  openai_key: ""

search:
  hybrid_weight: 0.5    # 0 = pure BM25, 1 = pure vector
  results_limit: 50

indexing:
  workers: 4
  watch: true

storage:
  path: ~/.local/share/mindcli

privacy:
  redact_content: false   # true also redacts stored content/preview at index time
  redact_patterns:
    - (?i)api[_-]?key\s*[:=]\s*[A-Za-z0-9_-]{16,}
    - (?i)secret\s*[:=]\s*[A-Za-z0-9_-]{16,}
    - \b[0-9]{16}\b
```

## Privacy

All data stays on your machine and there is no telemetry. By default indexed
content is stored in cleartext under the data directory, and `redact_patterns`
applies at display time only. Set `privacy.redact_content: true` to redact
content before it is stored. See [PRIVACY.md](PRIVACY.md) for the full threat
model, source-specific controls, and at-rest-encryption guidance.

## Running in the background

To keep the index current automatically, run `mindcli watch` as a service.
Example unit files are provided in [`init/`](init/) for systemd (Linux) and
launchd (macOS).

## How Search Works

MindCLI uses a hybrid search approach:

1. **Query parsing** — Extracts intent (search/summarize/answer), source filters ("in my emails"), and time references ("last week")
2. **BM25** (via Bleve) for keyword matching
3. **Vector similarity** (via HNSW) for semantic understanding
4. **Reciprocal Rank Fusion** merges both result sets into a single ranked list

Natural language queries like `"what did I write about Go in my notes last week"` are parsed to filter by source and time automatically.

When the query intent is "answer" or "summarize" and Ollama is available, MindCLI generates a RAG-style answer from the top search results with a confidence indicator (low/medium/high) based on source coverage and query overlap. When Ollama is not available, search gracefully falls back to BM25-only mode.

## Development

```bash
make build           # Build binary to bin/mindcli
make run             # Build and run
make test            # Run tests
make test-race       # Run with race detector
make test-coverage   # Generate coverage report
make lint            # Run golangci-lint
make fmt             # Format code
make clean           # Clean build artifacts
./scripts/release_smoke.sh  # Verify release archive/install flow
```

### Project Structure

```
mindcli/
├── cmd/mindcli/             # CLI entry point
├── internal/
│   ├── config/              # YAML configuration
│   ├── embeddings/          # Ollama embedder + content-hash cache
│   ├── index/               # Indexing pipeline
│   │   ├── indexer.go       # Worker pool orchestrator
│   │   ├── watcher.go       # fsnotify file watcher
│   │   └── sources/         # Source implementations
│   │       ├── source.go    # Source interface
│   │       ├── markdown.go  # Markdown/notes parser
│   │       ├── pdf.go       # PDF text extraction
│   │       ├── email.go     # Mbox/Maildir/emlx parser
│   │       ├── browser.go   # Chrome/Firefox/Safari history
│   │       └── clipboard.go # Clipboard with password detection
│   ├── query/               # Hybrid search + LLM query parser
│   ├── search/              # Bleve full-text search
│   ├── storage/             # SQLite + HNSW vector store
│   └── tui/                 # Bubble Tea interface
│       ├── app.go           # Main model + three-panel layout
│       ├── keys.go          # Keybindings
│       └── styles/          # Lip Gloss styling
└── pkg/chunker/             # Sliding window text chunker
```

## License

MIT
