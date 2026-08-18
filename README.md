# MindCLI

A fast, private TUI for personal knowledge management with AI-powered search.

Search across notes, PDFs, office documents, ebooks, web archives, Org files,
code repositories, emails, browser history, and clipboard from a single
keyboard-driven interface. The default Ollama setup runs locally; the optional
OpenAI provider sends document chunks and questions to the configured
OpenAI-compatible API.

## Features

- **Multi-source indexing** — Markdown, page-aware PDFs, HTML/web archives, DOCX, EPUB chapters, Org sections, code repositories, email/attachments, individual browser pages, and clipboard
- **Optional local OCR** — Discoverable Tesseract + Poppler fallback for image-only PDFs, disabled by default
- **Hybrid search** — BM25 full-text search + semantic vector search with Reciprocal Rank Fusion
- **Local AI by default** — Embeddings and streaming LLM answers via Ollama, with optional OpenAI provider
- **Conversational follow-ups** — Ask a question, then follow up ("tell me more") with prior turns kept in context
- **Research sessions** — Explicitly persist named Q&A threads, source context, citations, and Markdown briefs
- **Beautiful TUI** — Three-panel Bubble Tea interface with live preview and real-time streaming
- **Export** — Search results to JSON, CSV, or Markdown
- **Tagging** — Manual tags on any document, displayed in TUI and searchable
- **Collections** — Named groups of documents (like playlists), with CLI and TUI management
- **Activity digests** — Track new collection members and export cited Markdown updates on demand
- **Quick capture** — Save thoughts, pasted text, and public web pages into a portable Markdown inbox
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

**Requirements:** Go 1.25.13+ and CGO enabled (for SQLite). Optional:
[Ollama](https://ollama.ai) for semantic search and LLM features; Tesseract and
Poppler's `pdftoppm` for explicitly enabled PDF OCR.

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
mindcli index -paths ~/notes                 # Override Markdown paths for this run
mindcli index -watch                         # Index then watch for changes
mindcli index -force                         # Re-index, ignoring unchanged-file checks
mindcli reindex                              # Full rebuild (e.g. after model change)
mindcli reindex -paths ~/notes               # Full rebuild with Markdown path override
mindcli watch                                # Watch directories for changes
mindcli search "Go concurrency"              # Search and print results
mindcli search 'tag:project after:2026-07-01 "launch plan"'
mindcli related ~/notes/project.md            # Find related documents
mindcli related --id DOCUMENT_ID --limit 10   # Use a stable document ID
mindcli mcp                                   # Serve read-only MCP over stdio
mindcli add "Idea for the search ranking"     # Capture text into the Markdown inbox
pbpaste | mindcli add --tag inbox             # Capture stdin with tags
mindcli add --editor --collection research    # Capture through $VISUAL or $EDITOR
mindcli save https://example.com/article      # Save a normalized URL, optionally with reader text
mindcli session create release-research       # Create an explicitly persistent research session
mindcli session resume release-research       # Resume the session in the TUI
mindcli session export release-research --format markdown --output brief.md
mindcli profile create work                  # Create isolated config/data/inbox defaults
mindcli --profile work search "launch plan"  # Never searches another profile implicitly
MINDCLI_PROFILE=personal mindcli doctor      # Environment selection is also supported
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
mindcli digest --since 7d --collection research --output digest.md
mindcli ask "what did I write about Go?"     # Ask a question (streaming RAG via configured LLM)
mindcli config                               # Initialize default config file
mindcli config --path                        # Print the active config path
mindcli config --force                       # Replace an existing config with defaults
mindcli version                              # Show version info
mindcli help                                 # Show help
```

Run `mindcli help`, `mindcli export -h`, or a subcommand without required
arguments to see command-specific usage.

For `index` and `reindex`, `-paths` replaces only the configured Markdown
paths. It is not a global source filter: every other enabled source still runs
with its configured paths and settings.

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
| `R` | Replace results with documents related to the selection |
| `i` | Index sources now (in-app) |
| `Ctrl+n` | Quick capture a thought to the Markdown inbox |
| `A` / `P` / `X` | Add, pin, or exclude the selected document in a resumed session |
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

Configuration lives under the OS user-config directory. The default profile
uses `mindcli/config.yaml` there, while named profiles use
`mindcli/profiles/NAME.yaml`. Typical default-profile locations are
`~/.config/mindcli/config.yaml` on Linux (or `$XDG_CONFIG_HOME` when set),
`~/Library/Application Support/mindcli/config.yaml` on macOS, and
`%AppData%\mindcli\config.yaml` on Windows. `MINDCLI_CONFIG_DIR` overrides the
base directory.

Run `mindcli config` (optionally after `--profile NAME`) to generate the active
file. Existing configuration is preserved unless you explicitly pass
`--force`; `mindcli config --path` prints the resolved location without
writing. File watching starts only with `mindcli index -watch` or
`mindcli watch`.

Environment variables can override config values at runtime:

- Profile/config/storage: `MINDCLI_PROFILE`, `MINDCLI_CONFIG_PATH`, `MINDCLI_CONFIG_DIR`, `MINDCLI_STORAGE_PATH`
- Indexing/search: `MINDCLI_INDEXING_WORKERS`, `MINDCLI_SEARCH_HYBRID_WEIGHT`, `MINDCLI_SEARCH_RESULTS_LIMIT`
- Embeddings/LLM: `MINDCLI_EMBEDDINGS_PROVIDER`, `MINDCLI_EMBEDDINGS_MODEL`, `MINDCLI_EMBEDDINGS_LLM_MODEL`, `MINDCLI_EMBEDDINGS_OLLAMA_URL`, `MINDCLI_EMBEDDINGS_OPENAI_KEY`
- Markdown: `MINDCLI_SOURCES_MARKDOWN_ENABLED`, `MINDCLI_SOURCES_MARKDOWN_PATHS`, `MINDCLI_SOURCES_MARKDOWN_EXTENSIONS`, `MINDCLI_SOURCES_MARKDOWN_IGNORE`
- PDF: `MINDCLI_SOURCES_PDF_ENABLED`, `MINDCLI_SOURCES_PDF_PATHS`, plus `MINDCLI_SOURCES_PDF_OCR_ENABLED`, `MINDCLI_SOURCES_PDF_OCR_COMMAND`, `MINDCLI_SOURCES_PDF_OCR_RENDERER`, `MINDCLI_SOURCES_PDF_OCR_LANGUAGES`, `MINDCLI_SOURCES_PDF_OCR_MAX_PAGES`, `MINDCLI_SOURCES_PDF_OCR_TIMEOUT_SECONDS`, `MINDCLI_SOURCES_PDF_OCR_MIN_TEXT_CHARS`
- Email: `MINDCLI_SOURCES_EMAIL_ENABLED`, `MINDCLI_SOURCES_EMAIL_PATHS`, `MINDCLI_SOURCES_EMAIL_FORMATS`, `MINDCLI_SOURCES_EMAIL_IGNORE`, `MINDCLI_SOURCES_EMAIL_MASK_SENSITIVE_PREVIEW`, `MINDCLI_SOURCES_EMAIL_EXTRACT_ATTACHMENTS`, `MINDCLI_SOURCES_EMAIL_MAX_ATTACHMENT_BYTES`, `MINDCLI_SOURCES_EMAIL_MAX_DECOMPRESSED_BYTES`, `MINDCLI_SOURCES_EMAIL_MAX_ARCHIVE_DEPTH`
- HTML/DOCX/EPUB/Org: `MINDCLI_SOURCES_FORMAT_ENABLED`, `MINDCLI_SOURCES_FORMAT_PATHS`, `MINDCLI_SOURCES_FORMAT_IGNORE`, `MINDCLI_SOURCES_FORMAT_MAX_FILE_BYTES`, `MINDCLI_SOURCES_FORMAT_MAX_DECOMPRESSED_BYTES` (replace `FORMAT` with `HTML`, `DOCX`, `EPUB`, or `ORG`)
- Code: `MINDCLI_SOURCES_CODE_ENABLED`, `MINDCLI_SOURCES_CODE_PATHS`, `MINDCLI_SOURCES_CODE_IGNORE`, `MINDCLI_SOURCES_CODE_MAX_FILE_BYTES`, `MINDCLI_SOURCES_CODE_MAX_FILES`
- Browser: `MINDCLI_SOURCES_BROWSER_ENABLED`, `MINDCLI_SOURCES_BROWSER_BROWSERS`, `MINDCLI_SOURCES_BROWSER_INCLUDE_CONTENT`, `MINDCLI_SOURCES_BROWSER_ALLOWED_DOMAINS`, `MINDCLI_SOURCES_BROWSER_DENIED_DOMAINS`, `MINDCLI_SOURCES_BROWSER_MAX_RESPONSE_BYTES`, `MINDCLI_SOURCES_BROWSER_REQUEST_TIMEOUT_SECONDS`, `MINDCLI_SOURCES_BROWSER_FETCH_CONCURRENCY`, `MINDCLI_SOURCES_BROWSER_MAX_PAGES`, `MINDCLI_SOURCES_BROWSER_RETENTION_DAYS`
- Clipboard: `MINDCLI_SOURCES_CLIPBOARD_ENABLED`, `MINDCLI_SOURCES_CLIPBOARD_RETENTION_DAYS`, `MINDCLI_SOURCES_CLIPBOARD_SKIP_PASSWORDS`
- Capture: `MINDCLI_CAPTURE_INBOX`
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
    # OCR is a local, optional fallback for low/no-text PDFs.
    ocr_enabled: false
    ocr_command: tesseract
    ocr_renderer: pdftoppm
    ocr_languages: ["eng"]
    ocr_max_pages: 25
    ocr_timeout_seconds: 120
    ocr_min_text_chars: 80

  email:
    enabled: false
    paths: []
    formats: ["mbox", "maildir"] # .eml/.emlx files are also detected
    ignore: []
    mask_sensitive_preview: true
    extract_attachments: false
    max_attachment_bytes: 16777216
    max_decompressed_bytes: 67108864
    max_archive_depth: 1

  # Additional file sources are opt-in. max_decompressed_bytes is the archive
  # expansion budget; Org uses the same shared bounded-source shape.
  html:
    enabled: false
    paths: []
    ignore: [".git", "node_modules"]
    max_file_bytes: 16777216
    max_decompressed_bytes: 33554432

  docx:
    enabled: false
    paths: []
    ignore: [".git", "node_modules"]
    max_file_bytes: 67108864
    max_decompressed_bytes: 134217728

  epub:
    enabled: false
    paths: []
    ignore: [".git", "node_modules"]
    max_file_bytes: 67108864
    max_decompressed_bytes: 134217728

  org:
    enabled: false
    paths: []
    ignore: [".git", "node_modules"]
    max_file_bytes: 16777216
    max_decompressed_bytes: 16777216

  code:
    enabled: false
    paths: []
    ignore: [".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build", ".idea", ".vscode"]
    max_file_bytes: 1048576
    max_files: 20000

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

capture:
  inbox: ~/Documents/MindCLI Inbox

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

storage:
  path: ~/.local/share/mindcli

privacy:
  redact_content: false   # true also redacts stored content/preview at index time
  redact_patterns: []     # empty by default; add patterns for sensitive text
```

## Extended local formats and OCR

HTML (`.html`, `.htm`), MHTML (`.mhtml`, `.mht`), Safari `.webarchive`, DOCX,
EPUB, and Org-mode sources are disabled until paths are configured. EPUB spine
items and top-level Org sections become independent searchable documents with
stable IDs; their metadata retains the original openable file, format,
section/location, and extraction method. DOCX and saved-archive parsing is
plain-text extraction, so unsupported layout, styling, images, or embedded
objects are not presented as source text. File and archive-expansion limits are
enforced before content is indexed.

PDF extraction records page boundaries as `## Page N` and exposes page counts,
method, and confidence metadata. When a PDF has little embedded text and OCR is
off, the low-confidence result retains an explicit extraction warning. Setting
`sources.pdf.ocr_enabled: true` runs the configured local `pdftoppm` renderer
and Tesseract command for at most `ocr_max_pages` within the configured timeout.
OCR output is labeled `ocr_tesseract` with low confidence and reports
truncation. `mindcli doctor` shows whether both optional commands are available.

Email attachment extraction is also opt-in. Supported textual attachments are
HTML/web archives, DOCX, EPUB, Org, Markdown/plain structured text, and PDFs.
Each extracted attachment becomes a child document citing the owning email;
failures and skipped limits remain visible on the email metadata. Per-file,
total expansion, and archive-depth bounds apply.

Code indexing recognizes common source/configuration languages, skips symbolic
links, secret-like `.env` files, minified JS/CSS, and configured dependency or
build directories. It stores repository-relative paths and language metadata.
Semantic indexing chunks at functions/types and then at line boundaries for
oversized declarations. `source:html`, `source:docx`, `source:epub`,
`source:org`, and `source:code` work anywhere structured filters are accepted.

## Quick capture and inbox

`mindcli add` accepts text as command arguments or stdin. Pass `--editor` to
open `$VISUAL` or `$EDITOR`; `--title`, repeatable or comma-separated `--tag`,
`--collection`, and `--source-url` add portable metadata. A missing named
collection is created automatically. Captures are limited to 5 MiB.

`mindcli save URL` stores a normalized HTTP or HTTPS URL. When
`sources.browser.include_content` is true, it also attempts the same bounded,
cookie-free reader-mode fetch used by browser indexing. A failed fetch still
saves the URL. Common tracking parameters and fragments are removed during
normalization, and saving the same normalized URL again returns the existing
file without overwriting it.

Every capture is atomically written as Markdown under `capture.inbox` and then
indexed immediately, without requiring embeddings or an LLM. The capture inbox
is treated as a Markdown source even when the ordinary Markdown source is
disabled. User-supplied frontmatter is preserved. Press `Ctrl+n` in the TUI for
a single-line capture tagged `inbox`.

## Privacy

There is no telemetry. With the default `ollama` provider, indexed content,
embeddings, and generated answers stay on your machine. If you switch
`embeddings.provider` to `openai`, document chunks and questions are sent to the
configured OpenAI-compatible API. By default indexed content is stored in
cleartext under the data directory and `redact_patterns` is empty. Configured
patterns redact matching text in supported display surfaces, but standard
exports are field-specific: JSON and Markdown redact previews only, while CSV
contains no preview and does not apply the redactor. Titles, paths, tags,
source labels, and JSON metadata are exported unchanged. Set
`privacy.redact_content: true` to redact document content and previews before
they are stored; it does not redact those other fields. See
[PRIVACY.md](PRIVACY.md) for the full threat model, source-specific controls,
and at-rest-encryption guidance.

## Persistent research sessions

Named sessions are opt-in. The ordinary `mindcli` TUI keeps only its four most
recent follow-up turns in memory and clears them when search is cleared; it does
not write that conversation to disk. `mindcli session resume NAME` is the explicit
boundary that persists completed questions, generated answers, timestamps, and
citation snapshots in the active SQLite database.

```bash
mindcli session create release-research
mindcli session resume release-research
mindcli session list
mindcli session show release-research

# Manage reusable context from scripts as well as the TUI.
mindcli session add release-research ~/notes/plan.md
mindcli session pin release-research DOCUMENT_ID
mindcli session exclude release-research ~/notes/obsolete.md
mindcli session remove release-research DOCUMENT_ID

mindcli session export release-research --format markdown --output brief.md
mindcli session delete release-research
```

In a resumed TUI, `A` adds the selected document to the reusable context set,
`P` pins it ahead of ordinary search results, and `X` excludes it from later
answers. Each prompt uses at most five distinct documents in this deterministic
order: pinned, added, then current search results. Each document contributes at
most 1,000 Unicode characters. Follow-ups use the newest four turns, bounded to
1,000 question and 4,000 answer characters per turn. The answer preview shows
both used/available counts and says when older history was omitted.

Markdown briefs contain the conversation, the last answer as a final synthesis,
per-turn citations with stable document IDs, and a deduplicated source list.
Generated answers are labeled as generated content. Brief files created through
`--output` use mode `0600`.

## Work and personal profiles

Profiles are hard local store boundaries selected before the command:

```bash
mindcli profile create work
mindcli profile create personal
mindcli profile list

mindcli --profile work config
mindcli --profile work index
mindcli --profile work
mindcli --profile work session list
mindcli --profile work mcp

MINDCLI_PROFILE=personal mindcli search "travel plans"
```

Profile names are limited to 32 ASCII letters, numbers, hyphens, and
underscores, must start with a letter or number, and cannot contain path
separators. A command-line `--profile` takes precedence over `MINDCLI_PROFILE`.
`profile list` reads safe config filenames only; it never opens a profile's
database or indexed content.

The historical `default` profile keeps the existing data and inbox paths. A
named profile defaults to `~/.local/share/mindcli/profiles/NAME` and
`~/Documents/MindCLI Inbox/NAME`. Its SQLite database, Bleve index, vector graph,
embedding cache, source paths, providers, redaction rules, tags, collections,
sessions, and captures are therefore separate. Default source paths still point
at the same home folders until edited, so set each profile's sources deliberately.
The active profile is always shown in the TUI header and `mindcli doctor`.

`MINDCLI_CONFIG_PATH`, `MINDCLI_STORAGE_PATH`, and `MINDCLI_CAPTURE_INBOX` are
explicit exact-path overrides. Reusing one override across profiles can make
them share configuration, storage, or captured files; avoid that when isolation
is the goal.

## MCP server

`mindcli mcp` exposes the local index to MCP-compatible assistants and editors
over stdio. It does not listen on a network port. Configure a client to launch
the MindCLI binary as a subprocess, for example:

```json
{
  "mcpServers": {
    "mindcli": {
      "command": "/absolute/path/to/mindcli",
      "args": ["--profile", "default", "mcp"]
    }
  }
}
```

The server implements MCP `2026-07-28` through the official Go SDK and remains
compatible with supported older clients. It exposes seven read-only tools:
`search`, `ask`, `get_document`, `list_collections`, `show_collection`,
`recent_documents`, and `related_documents`. Every result includes stable
document IDs and source provenance where applicable. The `search` and `ask`
tools accept the same structured filters as the CLI, either inside the query or
through a typed `filters` object.

Tool inputs and outputs are bounded: queries are limited to 4 KiB, result lists
to 50 items, individual document content to 20 KiB, previews and metadata
values to 1 KiB, and generated answers to 32 KiB. `get_document` accepts a
smaller `max_content_bytes`; `recent_documents` accepts lookbacks such as `7d`,
`2w`, and `24h`. Invalid types, dates, filters, and limits return visible tool
errors. Protocol traffic is written only to stdout; diagnostics go to stderr.

Configured display-time redaction is applied to every tool result. See
[PRIVACY.md](PRIVACY.md) before connecting MindCLI to a client backed by a
remote model: once a client receives local content, that client's own privacy
and retention policy applies.

## Running in the background

To keep the index current automatically, run `mindcli watch` as a service.
Example unit files are provided in [`init/`](init/) for systemd (Linux) and
launchd (macOS); both explicitly select `--profile default`. Duplicate and
rename a service with another explicit profile to watch more than one.

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

### Collection activity and digests

`mindcli collection list` shows manual-collection activity and view state;
`collection show` and the TUI collection browser also resolve smart-collection
activity. Manual collections compare membership timestamps to `last_viewed_at`.
Smart collections snapshot the bounded set of matching document IDs when
viewed, so later matches appear as new. The first view treats every current
member as new. Opening a collection in the CLI/TUI or successfully exporting
its digest advances the view boundary.

Digests are generated only when requested; MindCLI does not schedule
notifications:

```bash
mindcli digest                         # All activity from the last 7 days
mindcli digest --since 24h
mindcli digest --since 2w --collection research
mindcli digest --since 2026-08-01 --limit 100 --output digest.md
```

`--since` accepts hours, days, weeks, `YYYY-MM-DD`, or RFC 3339 and is bounded
to ten years for duration lookbacks. Results are capped at 100 (default 50).
The Markdown report contains an activity summary plus stable document IDs,
source types, paths, activity reasons, and numbered citations. When the
configured LLM is available, the summary is clearly labeled as generated and
uses the first five document excerpts; otherwise a deterministic count summary
is emitted. Display-time redaction applies, and `--output` files use mode
`0600`.

### Related documents

`mindcli related <path>` and `mindcli related --id <document-id>` rank other
documents using semantic similarity, lexical similarity, shared tags, and
shared Markdown links. Each result includes a `Why:` line naming the signals
that contributed to its rank. The selected source document is excluded, result
counts are capped at 100, and `--limit` defaults to 10.

Press `R` on a TUI result or preview to replace the result list with related
documents. Relation reasons appear in each related document's preview.

Semantic scoring is used when a compatible vector index and configured
embedding provider are available. If vectors are missing or embedding fails,
related discovery continues with local full-text, tag, and link signals; it
does not require an LLM. Semantic scoring embeds bounded source text through
the provider already configured for search, with the same privacy implications
described in [PRIVACY.md](PRIVACY.md).

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
├── cmd/mindcli/             # CLI commands, exports, digests, profiles, sessions
├── init/                    # systemd and launchd watcher services
├── internal/
│   ├── capture/             # Atomic Markdown inbox writer
│   ├── config/              # YAML configuration
│   ├── embeddings/          # Ollama/OpenAI embedders + content-hash cache
│   ├── filter/              # Structured query filters
│   ├── index/               # Indexing pipeline
│   │   ├── indexer.go       # Worker pool orchestrator
│   │   ├── watcher.go       # fsnotify file watcher
│   │   └── sources/         # Source implementations
│   │       ├── archive.go   # Bounded archive helpers
│   │       ├── browser.go   # Chrome/Firefox/Safari history
│   │       ├── browser_fetch.go # Optional bounded page fetches
│   │       ├── clipboard.go # Clipboard with password detection
│   │       ├── code.go      # Bounded source-code repositories
│   │       ├── docx.go      # Word documents
│   │       ├── email.go     # Mbox/Maildir/emlx and attachments
│   │       ├── epub.go      # EPUB chapters
│   │       ├── html.go      # HTML, MHTML, and web archives
│   │       ├── markdown.go  # Markdown/notes parser
│   │       ├── org.go       # Org-mode sections
│   │       ├── pdf.go       # PDF text extraction and optional OCR
│   │       ├── scanner.go   # Shared bounded filesystem scanner
│   │       └── source.go    # Source interface
│   ├── mcpserver/           # Read-only MCP server and service layer
│   ├── privacy/             # Configurable text redaction
│   ├── query/               # Hybrid search + LLM query parser
│   ├── search/              # Bleve full-text search
│   ├── storage/             # SQLite, sessions, collections, HNSW vectors
│   └── tui/                 # Bubble Tea interface
│       ├── app.go           # Main model + three-panel layout
│       ├── keys.go          # Keybindings
│       └── styles/          # Lip Gloss styling
├── pkg/chunker/             # Text and code-aware chunking
└── scripts/                 # Install and release smoke scripts
```

## License

MIT
