# MindCLI Feature Roadmap

MindCLI already covers the core search and chat experience: multi-source
indexing, hybrid retrieval, cited RAG answers, conversational follow-ups, tags,
smart collections, filtering, background watching, diagnostics, exports, and
privacy controls. The next releases should deepen retrieval and make the local
index useful from other tools without weakening the product's fast, private,
local, keyboard-first identity.

## Priorities

| Priority | Feature | Expected value | Estimated effort |
| --- | --- | --- | --- |
| 1 | Browser pages as individual documents | Very high | Medium |
| 2 | Read-only MCP server | Very high | Medium |
| 3 | Quick capture and inbox | High | Small to medium |
| 4 | Structured query filters | High | Medium |
| 5 | Related documents and resurfacing | Medium to high | Small |
| 6 | Persistent research sessions | Medium to high | Medium |
| 7 | More formats and OCR | Medium | Large |
| 8 | Work/personal privacy profiles | Medium to high | Medium |

## 1. Browser Pages as Individual Documents

### Why

The current browser parser combines an entire browser database into one large
document. The `sources.browser.include_content` configuration option is also
reserved rather than implemented. This weakens ranking, previews, citations,
cleanup, and the central "what was that article I read?" use case.

Relevant code:

- `internal/index/sources/browser.go`, especially `buildBrowserDocument`
- `internal/config/config.go`, `BrowserSourceConfig.IncludeContent`
- `README.md`, browser source configuration
- `PRIVACY.md`, browser-specific privacy behavior

### Proposed behavior

- Store each browser history entry or bookmark as its own document.
- Use a stable identity based on the normalized URL and browser/profile where
  necessary.
- Preserve the original URL, browser, visit count, last-visit time, and whether
  the entry is history or a bookmark as structured metadata.
- Optionally fetch and index reader-mode page content when `include_content` is
  enabled.
- Preserve the URL as an openable source for results and citations.
- Deduplicate the same normalized URL across repeated visits while retaining
  useful visit metadata.

### Privacy and resilience requirements

- Keep page fetching disabled by default.
- Do not send browser cookies, credentials, or authenticated session state.
- Support domain allowlists and denylists.
- Set maximum response size, request timeout, concurrency, and retention limits.
- Restrict fetched content to supported textual content types.
- Handle redirects and failed/offline requests without failing browser-history
  indexing.
- Make network behavior explicit in `PRIVACY.md` and the default config.

### Suggested implementation sequence

1. Extend the source/indexer boundary so one scanned browser database can yield
   multiple documents.
2. Store individual history entries and bookmarks without page fetching.
3. Update cleanup and incremental indexing for virtual browser documents.
4. Add optional reader-mode fetching and extraction.
5. Add configuration, diagnostics, tests, and documentation.

## 2. Read-Only MCP Server

### Why

MindCLI's search, storage, collections, and cited-answer APIs already map well to
Model Context Protocol tools and resources. An MCP server would make the local
knowledge index available to coding agents, desktop assistants, and editors
without building a separate interface for each client.

MCP servers can expose typed tools and structured knowledge-base resources to
AI applications. Start with a local stdio transport and read-only behavior.

Reference:

- <https://modelcontextprotocol.io/docs/2026-07-28/learn/server-concepts>

### Proposed command

```console
mindcli mcp
```

### Initial tools

- `search`: Hybrid search with query, limit, and structured filters.
- `ask`: Generate a cited answer from retrieved context.
- `get_document`: Read a document by stable ID.
- `list_collections`: List collections and their descriptions.
- `show_collection`: Return collection metadata and documents.
- `recent_documents`: Return documents indexed or modified within a time range.
- `related_documents`: Return documents related to a selected document.

### Requirements

- Use stdio first; do not open a network port by default.
- Keep the first version read-only.
- Apply configured display-time redaction to every response.
- Return stable document IDs and source metadata with every result.
- Preserve citations and enough provenance for a client to display or open the
  original source.
- Put explicit bounds on result count and content size.
- Send protocol logs to stderr so stdout remains valid MCP traffic.
- Add integration tests covering initialization, tool discovery, calls,
  redaction, cancellation, and malformed inputs.

### Possible later additions

- Read-only MCP resources for individual documents and collections.
- An authenticated local HTTP transport.
- Write tools only after an explicit permission and audit model exists.

## 3. Quick Capture and Inbox

### Why

MindCLI currently indexes knowledge created elsewhere. Deliberate capture would
let it handle ideas, pasted text, and URLs at the moment the user encounters
them.

### Proposed commands

```console
mindcli add "Idea for the search ranking"
pbpaste | mindcli add --tag inbox
mindcli add --editor --collection research
mindcli save https://example.com/article --collection reading
```

### Proposed behavior

- Write captures into a configurable Markdown inbox so the portable file is the
  source of truth rather than a database-only record.
- Support text supplied as arguments, stdin, or `$EDITOR`.
- Support title, tags, collection, and source URL metadata.
- Index the new Markdown file immediately.
- Add a TUI shortcut for capture.
- Reuse browser reader-mode extraction for `mindcli save URL` when enabled.

### Requirements

- Avoid duplicate captures when the same URL is saved repeatedly.
- Use safe filenames and atomic file creation.
- Preserve frontmatter written by the user.
- Work without embeddings or an LLM.
- Make capture destinations explicit per profile once profiles exist.

## 4. Structured Query Filters

### Why

The current parser recognizes a small set of natural-language source and time
phrases. Explicit, composable filters would give power users predictable
retrieval and provide a clean query surface for MCP clients and scripts.

### Proposed syntax

```text
source:email tag:project after:2026-07-01 "launch plan"
collection:reading domain:arxiv.org -tag:archived
path:work/ type:pdf before:2025-01-01
source:browser kind:bookmark this week databases
```

### Initial filters

- `source:` or `type:`
- `tag:` and `-tag:`
- `collection:`
- `after:` and `before:`
- `path:`
- `domain:`
- `kind:` for source-specific record kinds such as browser bookmarks
- Quoted exact phrases and negated terms

### Requirements

- Keep natural-language parsing as a convenience layer.
- Parse structured filters deterministically before intent heuristics.
- Represent filters in a typed query structure rather than reconstructing
  strings throughout the application.
- Apply filters consistently to CLI search, export, ask, TUI search, smart
  collections, and MCP.
- Display active filters clearly in the TUI.
- Return helpful errors for malformed dates and unknown filter names.
- Document precedence and escaping.

## 5. Related Documents and Resurfacing

### Why

The vector index already contains most of the machinery needed to move from
reactive search toward discovery. This is likely the best small feature after
the browser model is corrected.

### Proposed commands and TUI behavior

```console
mindcli related ~/notes/project.md
mindcli related --id DOCUMENT_ID --limit 10
mindcli digest --since 7d --collection research
```

- Add a TUI key that replaces the result list with documents related to the
  current selection.
- Explain whether a relation came from semantic similarity, shared tags,
  shared links, or lexical similarity.
- Fall back to tags and full-text similarity when vectors are unavailable.

### Resurfacing

- Track documents added to smart collections since the collection was last
  viewed.
- Add a digest that summarizes new or changed documents for a time range.
- Keep digest generation on demand initially; scheduled notifications can come
  later.
- Export digests as Markdown with citations.

## 6. Persistent Research Sessions

### Why

Conversational follow-up history currently lives only in the active TUI model.
Named sessions would make the answer workflow useful for sustained research
instead of a single terminal process.

### Proposed commands

```console
mindcli session create release-research
mindcli session resume release-research
mindcli session list
mindcli session export release-research --format markdown
```

### Proposed behavior

- Persist questions, answers, timestamps, citations, and selected source IDs.
- Resume a prior conversation in the TUI.
- Pin documents to the context set.
- Exclude irrelevant documents from subsequent answers.
- Add documents while researching.
- Export a Markdown brief containing the conversation, final synthesis,
  citations, and source list.

### Requirements

- Make context truncation visible and deterministic.
- Do not silently retain full prompt payloads when session persistence is
  disabled.
- Allow session deletion and provide clear privacy documentation.
- Keep generated answers distinguishable from source content.

## 7. More Formats and OCR

### Why

Additional local formats increase coverage without forcing users into cloud
connectors. They should be added through reusable parser boundaries rather than
as unrelated one-off implementations.

### Recommended order

1. HTML and saved web archives
2. DOCX
3. EPUB
4. Org-mode
5. OCR for image-only PDFs
6. Email attachment extraction
7. Code repositories with language-aware chunking

### Architecture guidance

- Introduce a generic multi-document ingestion contract before adding several
  new sources.
- Keep original path, format, section/page location, and extraction method in
  metadata.
- Make external tools such as Tesseract optional and discoverable through
  `mindcli doctor`.
- Treat OCR output as lower-confidence content and expose page references.
- Limit archive depth, attachment size, and decompressed size.
- Add small deterministic fixtures for every parser.

### What not to do initially

- Do not add many cloud integrations in parallel.
- Do not make a required external service part of the default local workflow.
- Do not hide lossy extraction or OCR failures.

## 8. Work/Personal Privacy Profiles

### Why

Emails, clipboard entries, browser history, and notes can have very different
sensitivity and provider requirements. Profiles provide useful isolation even
without implementing encryption across every store.

### Proposed usage

```console
mindcli --profile personal
mindcli --profile work search "launch plan"
MINDCLI_PROFILE=work mindcli ask "what changed this week?"
```

### Proposed isolation

Each profile should have separate:

- Configuration
- SQLite database
- Bleve index
- Vector graph and metadata
- Embedding cache
- Source paths
- Redaction rules
- Embedding and LLM provider settings
- Collections, tags, sessions, and capture inbox

### Requirements

- Never search across profiles implicitly.
- Display the active profile prominently in the TUI and diagnostic output.
- Validate profile names and prevent path traversal.
- Make service definitions select a profile explicitly.
- Provide a safe profile-list command that does not expose indexed content.
- If at-rest encryption is later added, cover all stored artifacts rather than
  only SQLite. Until then, continue recommending operating-system full-disk
  encryption for disk-theft threats.

## Recommended Release Sequence

### Release 1: Better Retrieval

- Store browser entries as individual documents.
- Add structured query filters.
- Add related-document discovery.

### Release 2: Use MindCLI Everywhere

- Add the read-only stdio MCP server.
- Add quick capture and the Markdown inbox.
- Implement optional browser reader-mode content fetching.

### Release 3: Research and Trust

- Add persistent research sessions and Markdown briefs.
- Add work/personal profiles.
- Add collection change tracking and on-demand digests.

### Later Releases

- Add selected document formats.
- Add PDF OCR and email attachment extraction.
- Consider authenticated remote access only after permissions, audit logging,
  and the privacy model are designed.

## Features to Defer

The following ideas are attractive but should not be near-term priorities:

- A general-purpose web UI
- Cloud synchronization
- Mobile applications
- Autonomous agents that can modify source files
- A visual knowledge graph
- Broad team collaboration and access control

These would substantially expand the product and operational surface while
weakening MindCLI's clearest differentiation: fast, private, local,
keyboard-first retrieval. A read-only MCP interface provides much of the
integration value at a fraction of that scope.

## Implementation and Commit Guidance

Each roadmap item should be decomposed into small, atomic commits following the
repository's `AGENTS.md` policy. A typical feature should be ordered as:

1. Data model and migrations
2. Core behavior or parser changes
3. CLI/API/TUI wiring
4. Tests
5. Documentation and changelog updates

Avoid combining an architectural refactor, user-visible behavior, and broad
test changes in one commit unless they are genuinely inseparable.

