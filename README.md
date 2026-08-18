# MindCLI

A fast, private TUI for personal knowledge management with AI-powered search.

Search across your notes, PDFs, emails, browser history, and clipboard from a
single keyboard-driven interface. The default Ollama setup runs locally; the
optional OpenAI provider sends document chunks and questions to the configured
OpenAI-compatible API.

## Features

- **Multi-source indexing** — Markdown notes, PDFs, emails (mbox/maildir/emlx), individual browser pages (Chrome/Firefox/Safari), clipboard
- **Hybrid search** — BM25 full-text search + semantic vector search with Reciprocal Rank Fusion
- **Local AI by default** — Embeddings and streaming LLM answers via Ollama, with optional OpenAI provider
- **Conversational follow-ups** — Ask a question, then follow up ("tell me more") with prior turns kept in context
- **Beautiful TUI** — Three-panel Bubble Tea interface with live preview and real-time streaming
- **Export** — Search results to JSON, CSV, or Markdown
- **Tagging** — Manual tags on any document, displayed in TUI and searchable
- **Collections** — Named groups of documents (like playlists), with CLI and TUI management
- **Fast** — Concurrent worker pool indexing, incremental updates, content-hash caching
- **File watcher** — Real-time re-indexing via fsnotify with debouncing
- **Private by default** — Local storage, no telemetry, password detection for clipboard

## Installation

```bash
# Build from source
git clone https://github.com/J-1000/mindcli.git
cd mindcli
make build

# Optional: install the built binary on your PATH
mkdir -p ~/.local/bin
install -m 0755 bin/mindcli ~/.local/bin/mindcli
```

**Requirements:** Go 1.25.13+ and CGO enabled (for SQLite). Optional: [Ollama](https://ollama.ai) for semantic search and LLM features.

Release binaries and a Homebrew formula are not published yet. Until the first
release exists, use the source build above.

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
mindcli index -force                         # Re-index, ignoring unchanged-file checks
mindcli reindex                              # Full rebuild (e.g. after model change)
mindcli reindex -paths ~/notes               # Full rebuild for specific paths
mindcli watch                                # Watch directories for changes
mindcli search "Go concurrency"              # Search and print results
mindcli search 'tag:project after:2026-07-01 "launch plan"'
mindcli stats                                # Show index statistics
mindcli clean                                # Remove docs whose files are gone
mindcli doctor                               # Check config and service health
mindcli export --format json --limit 25 "Go" # Export results as JSON/CSV/Markdown
mindcli export --output results.json "Go"    # Write export output to a file
mindcli tag add ~/notes/foo.md mytag         # Add a tag to a document
mindcli tag remove ~/notes/foo.md mytag      # Remove a tag from a document
mindcli tag list                             # List all tags
mindcli tag list ~/notes/foo.md              # List tags for one document
mindcli clipboard clear                      # Remove all indexed clipboard entries
mindcli clipboard cleanup                    # Remove old indexed clipboard entries
mindcli collection create "reading-list"     # Create a collection
mindcli collection create go --query "Go"    # Create a smart collection from a saved query
mindcli collection add reading-list ~/f.md   # Add a document to a collection
mindcli collection remove reading-list ~/f.md # Remove a document from a collection
mindcli collection list                      # List all collections
mindcli collection show reading-list         # Show collection details and documents
mindcli collection rename old-name new-name  # Rename a collection
mindcli collection delete reading-list       # Delete a collection
mindcli ask "what did I write about Go?"     # Ask a question (streaming RAG via configured LLM)
mindcli config                               # Initialize default config file
mindcli config --path                        # Print the active config path
mindcli config --force                       # Replace an existing config with defaults
mindcli version                              # Show version info
mindcli help                                 # Show help
```

Run `mindcli help`, `mindcli export -h`, or a subcommand without required
arguments to see command-specific usage.

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
| `f` | Cycle source filter (all → markdown → pdf → …) |
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

MindCLI looks for `~/.config/mindcli/config.yaml`. Run `mindcli config` to
generate it. Existing configuration is preserved unless you explicitly pass
`--force`; `mindcli config --path` prints the resolved location without writing.

Environment variables can override config values at runtime:

- Config/storage: `MINDCLI_CONFIG_PATH`, `MINDCLI_CONFIG_DIR`, `MINDCLI_STORAGE_PATH`
- Indexing/search: `MINDCLI_INDEXING_WORKERS`, `MINDCLI_INDEXING_WATCH`, `MINDCLI_SEARCH_HYBRID_WEIGHT`, `MINDCLI_SEARCH_RESULTS_LIMIT`
- Embeddings/LLM: `MINDCLI_EMBEDDINGS_PROVIDER`, `MINDCLI_EMBEDDINGS_MODEL`, `MINDCLI_EMBEDDINGS_LLM_MODEL`, `MINDCLI_EMBEDDINGS_OLLAMA_URL`, `MINDCLI_EMBEDDINGS_OPENAI_KEY`
- Markdown: `MINDCLI_SOURCES_MARKDOWN_ENABLED`, `MINDCLI_SOURCES_MARKDOWN_PATHS`, `MINDCLI_SOURCES_MARKDOWN_EXTENSIONS`, `MINDCLI_SOURCES_MARKDOWN_IGNORE`
- PDF: `MINDCLI_SOURCES_PDF_ENABLED`, `MINDCLI_SOURCES_PDF_PATHS`
- Email: `MINDCLI_SOURCES_EMAIL_ENABLED`, `MINDCLI_SOURCES_EMAIL_PATHS`, `MINDCLI_SOURCES_EMAIL_FORMATS`, `MINDCLI_SOURCES_EMAIL_IGNORE`, `MINDCLI_SOURCES_EMAIL_MASK_SENSITIVE_PREVIEW`
- Browser: `MINDCLI_SOURCES_BROWSER_ENABLED`, `MINDCLI_SOURCES_BROWSER_BROWSERS`, `MINDCLI_SOURCES_BROWSER_INCLUDE_CONTENT`, `MINDCLI_SOURCES_BROWSER_ALLOWED_DOMAINS`, `MINDCLI_SOURCES_BROWSER_DENIED_DOMAINS`, `MINDCLI_SOURCES_BROWSER_MAX_RESPONSE_BYTES`, `MINDCLI_SOURCES_BROWSER_REQUEST_TIMEOUT_SECONDS`, `MINDCLI_SOURCES_BROWSER_FETCH_CONCURRENCY`, `MINDCLI_SOURCES_BROWSER_MAX_PAGES`, `MINDCLI_SOURCES_BROWSER_RETENTION_DAYS`
- Clipboard: `MINDCLI_SOURCES_CLIPBOARD_ENABLED`, `MINDCLI_SOURCES_CLIPBOARD_RETENTION_DAYS`, `MINDCLI_SOURCES_CLIPBOARD_SKIP_PASSWORDS`
- Privacy: `MINDCLI_PRIVACY_REDACT_PATTERNS`, `MINDCLI_PRIVACY_REDACT_CONTENT`

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
    formats: ["mbox", "maildir"] # .eml/.emlx files are also detected
    ignore: []
    mask_sensitive_preview: true

  browser:
    enabled: true
    browsers: ["chrome", "firefox", "safari"]
    # false indexes browser titles, URLs, visits, and bookmarks without making
    # page-content network requests. true fetches public textual pages with a
    # cookie-free client and the bounds below.
    include_content: false
    allowed_domains: [] # empty allows every domain not explicitly denied
    denied_domains: []  # deny rules take precedence; subdomains also match
    max_response_bytes: 2097152
    request_timeout_seconds: 10
    fetch_concurrency: 4
    max_pages: 5000
    retention_days: 365

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

There is no telemetry. With the default `ollama` provider, indexed content,
embeddings, and generated answers stay on your machine. If you switch
`embeddings.provider` to `openai`, document chunks and questions are sent to the
configured OpenAI-compatible API. By default indexed content is stored in
cleartext under the data directory, and `redact_patterns` applies at display
time only. Set `privacy.redact_content: true` to redact content before it is
stored. See [PRIVACY.md](PRIVACY.md) for the full threat model,
source-specific controls, and at-rest-encryption guidance.

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

### Structured query filters

Explicit filters provide predictable, composable searches in `search`,
`export`, `ask`, the TUI, and saved smart-collection queries:

```text
source:email tag:project after:2026-07-01 "launch plan"
collection:reading domain:arxiv.org -tag:archived
path:work/ type:pdf before:2025-01-01
source:browser kind:bookmark this week databases
```

Supported filters are `source:` (or `type:`), `tag:`, `-tag:`,
`collection:`, `after:`, `before:`, `path:`, `domain:`, and `kind:`. Dates
accept `YYYY-MM-DD` or RFC 3339. `after:` is inclusive and `before:` is
exclusive. Repeated sources, domains, kinds, and collections are alternatives;
positive tags and paths are cumulative. Domain filters include subdomains.

Double quotes make an exact phrase. Quote a filter value that contains spaces,
such as `tag:"Project Alpha"`. A leading `-` excludes a word or quoted phrase,
as in `-draft -"old version"`. A backslash escapes the following character, so
`launch\ plan` is an exact phrase and `foo\:bar` searches for a literal colon.
Malformed dates, unknown filter names, unterminated quotes, and incomplete
escapes produce an error.

Structured filters are parsed before natural-language conveniences and take
precedence when they overlap. For example, `source:email in my notes` searches
email, and `after:2026-07-01 last week` uses the explicit date. The TUI shows
every active filter beside the result count.

When the query intent is "answer" or "summarize" and an LLM backend is
available, MindCLI generates a RAG-style answer from the top search results with
inline `[n]` citations and a confidence indicator (low/medium/high) based on
source coverage and query overlap. If the LLM is unavailable, answer commands
show the top search results instead. If embeddings are unavailable, search
gracefully falls back to BM25-only mode.

Follow-up questions in the TUI keep recent Q&A turns in context, so asking "tell me more" or "what about the second one?" works as a conversation. The history resets when you clear the search.

### Browser indexing

Each browser history URL or bookmark is stored as an individual document. URLs
are normalized to remove fragments, default ports, and common tracking
parameters; repeated visits to the same normalized URL within a browser profile
are combined while retaining visit count, last-visit time, and bookmark/history
metadata. Search results and citations keep the original URL as their openable
source.

Page-content fetching is opt-in through `sources.browser.include_content`.
MindCLI sends ordinary unauthenticated GET requests without browser cookies,
credentials, or session state. Domain allow/deny rules also apply after
redirects, responses are limited to HTML/XHTML/plain text, and byte, timeout,
worker, page-count, and age limits are enforced. A failed or offline page is
recorded as unavailable while its browser title and URL continue to index.

## Performance

Indexing runs a concurrent worker pool and skips unchanged files (by mtime,
then content hash), so re-indexing is incremental. Search fuses BM25 and vector
results with Reciprocal Rank Fusion. Benchmarks live alongside the code:

```bash
go test ./pkg/chunker/ -bench . -benchmem
go test ./internal/query/ -bench . -benchmem
```

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
│   ├── embeddings/          # Ollama/OpenAI embedders + content-hash cache
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
