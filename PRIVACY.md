# Privacy & Threat Model

MindCLI is built to keep your data on your machine. This document is explicit
about **what is stored, where, and in what form**, so you can make an informed
decision.

## What stays local

- **All indexed content** lives under your data directory (default
  `~/.local/share/mindcli`): the SQLite database, the Bleve full-text index, the
  HNSW vector graph, and the embedding cache.
- **No telemetry.** MindCLI makes no network calls except to the embedding/LLM
  backend you configure and, when explicitly enabled, public pages from your
  browser history or bookmarks.
- **Embedding/LLM backend.** With the default `ollama` provider, embeddings and
  answers are generated locally. If you switch to the `openai` provider, the
  text of your documents (chunks) and your questions are sent to OpenAI's API.
  This is the one case where content leaves your machine — opt in deliberately.

## What is stored, and in what form

By default, indexed content is stored **in cleartext**:

| Store | Location | Contents |
|-------|----------|----------|
| SQLite | `mindcli.db` | document title, full content, preview, metadata, tags, collections |
| Bleve | `search.bleve/` | tokenized full-text index of title + content |
| HNSW | `vectors.graph` | chunk embeddings (+ `vectors.graph.meta.json` model/dim) |
| Cache | `embeddings.db` | content-hash → embedding vectors |

So a note, PDF, email, browser title, fetched browser page, or clipboard entry
that you index is searchable in cleartext on disk.

## Redaction

Redaction has two layers, controlled by `privacy.redact_patterns`:

- **Display-time (default):** matches are replaced with `[REDACTED]` in search
  output, exports, generated answers, and every MCP tool result. The underlying
  stored content is **not** changed.
- **Index-time (opt-in):** set `privacy.redact_content: true` to apply the same
  patterns to document content and previews **before** they are written to
  SQLite and the search index. Secrets matching your patterns are then never
  stored. Trade-off: the original text is unrecoverable and not searchable.

```yaml
privacy:
  redact_content: true
  redact_patterns:
    - (?i)api[_-]?key\s*[:=]\s*[A-Za-z0-9_-]{16,}
    - (?i)secret\s*[:=]\s*[A-Za-z0-9_-]{16,}
    - \b[0-9]{16}\b
```

## Source-specific controls

- **Email:** with `sources.email.mask_sensitive_preview: true`, email addresses,
  bearer tokens, API-key-like strings, and long numbers are masked in **both**
  the preview and the stored body.
- **Clipboard:** with `sources.clipboard.skip_passwords: true`, entries that look
  like passwords are not indexed; `retention_days` bounds how long clipboard
  history is kept (`mindcli clipboard cleanup`).
- **Browser:** each normalized URL is stored as its own document with browser,
  profile, visit count, last-visit time, and bookmark/history metadata. Repeated
  visits within a profile are deduplicated. By default, only this local browser
  data is read and no page-content requests are made. Browser records are
  limited by `max_pages` and `retention_days` on each index pass.

  Setting `sources.browser.include_content: true` opts in to network requests
  for the indexed URLs. These requests use a new cookie-free HTTP client: no
  browser cookies, authorization headers, client certificates, or authenticated
  browser session state are copied. The standard system proxy environment may
  still apply. Only HTML, XHTML, and plain-text responses are accepted.

  `allowed_domains` restricts requests to exact domains and their subdomains;
  an empty list allows all domains. `denied_domains` always takes precedence,
  including for redirects. `max_response_bytes`, `request_timeout_seconds`, and
  `fetch_concurrency` bound each fetch and the total concurrent work. Redirects
  are limited and rechecked against the same domain policy. Failed, blocked,
  oversized, unsupported, and offline pages do not fail history indexing: the
  title and URL remain searchable with a content-status marker.

## MCP boundary

`mindcli mcp` is a read-only stdio server. It opens no listening socket and
does not provide write, tag, collection-mutation, capture, index, or delete
tools. Result counts and textual fields are bounded, stable document IDs and
source provenance are preserved, and configured display-time redaction is
applied before any tool response is returned. Protocol stdout is kept separate
from diagnostics on stderr.

Read-only does not mean that returned data remains local. An MCP client that can
launch MindCLI can search and read the configured index, then copy those
results elsewhere. If that client uses a remote model, its requests may send
MindCLI results to that provider. MindCLI cannot enforce the client's access,
retention, or training policy after a response crosses the stdio boundary; only
configure clients you trust.

The `ask` tool also uses the configured LLM provider, and semantic search or
related-document scoring may use the configured embedding provider. With a
remote provider, the same document/question transmission described above under
"What stays local" applies. Display-time redaction protects MCP responses but
does not alter the stored source text sent to a configured model provider; use
index-time redaction when that is required by your threat model.

## What MindCLI does not (yet) do

- **No at-rest encryption.** The database and indexes are not encrypted. If your
  threat model includes disk theft or multi-user machines, use **full-disk
  encryption** (FileVault, LUKS, BitLocker). A SQLCipher-backed build is a
  possible future option.
- **No per-document access control.** Anything you index is searchable by anyone
  with read access to the data directory or permission to launch the MCP server.

## Removing data

- `mindcli clipboard clear` / `cleanup` — remove clipboard entries.
- `mindcli clean` — remove documents whose source files no longer exist.
- Delete the data directory to wipe everything.
