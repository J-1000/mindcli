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

Named research sessions are also stored in `mindcli.db`: full questions,
generated answers, timestamps, citation snapshots, and included/pinned/excluded
document IDs. This happens only after `mindcli session resume NAME`; the default
TUI's follow-up history remains memory-only.

Collection activity metadata is stored in the same database: the last-viewed
timestamp and document IDs previously observed in smart-collection result sets.

So a note, PDF/OCR result, HTML archive, DOCX, EPUB chapter, Org section, source
file, email/attachment, browser title, fetched browser page, capture, or
clipboard entry that you index is searchable in cleartext on disk. Captures
also remain as cleartext Markdown source files in the configured inbox.

## Redaction

Redaction has two layers, controlled by `privacy.redact_patterns`, which is
empty by default:

- **Display-time (default):** matches are replaced with `[REDACTED]` in search
  output, generated answers, session output, digests, and every MCP tool result.
  Standard search exports are narrower: JSON and Markdown redact only document
  previews; CSV contains no preview and does not apply the redactor. Exported
  titles, paths, tags, source labels, timestamps, and JSON metadata remain
  unchanged. The underlying stored content is **not** changed.
- **Index-time (opt-in):** set `privacy.redact_content: true` to apply the same
  patterns to document content and previews **before** they are written to
  SQLite and the search index. Matching text in those fields is then never
  stored. Titles, paths, tags, and metadata are not covered. Trade-off: the
  redacted original text is unrecoverable and not searchable.

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
  the preview and the stored body. Attachment extraction is disabled unless
  `extract_attachments` is true. When enabled, supported attachments are MIME
  decoded locally and stored as separate searchable child documents. Their
  filenames, content types, extraction status, and owning email path are stored
  as metadata. `max_attachment_bytes`, `max_decompressed_bytes`, and
  `max_archive_depth` bound individual input, aggregate expansion, and nested
  containers. Temporary attachment files use private permissions and are
  removed after parsing; process interruption can still leave operating-system
  temporary data until normal cleanup.
- **Local document archives:** HTML/webarchive, DOCX, EPUB, and Org sources are
  disabled by default. They read only explicitly configured paths. File and
  decompression budgets are enforced; ZIP-backed formats are parsed in memory
  without extracting archive paths onto the filesystem. Extracted text is
  lossy: page styling, images, embedded objects, and unsupported markup are not
  silently represented as original source content. Extraction method and
  section/location metadata remain attached to each document.
- **PDF OCR:** OCR is disabled by default and ordinary low-text extraction is
  marked with a warning. Enabling it executes the configured local renderer
  and Tesseract binaries with the source PDF path. Rendered page images live in
  a private temporary directory and are removed best-effort after the bounded,
  timed operation. OCR text is explicitly marked low-confidence with page and
  truncation metadata. MindCLI itself makes no OCR network request, but you are
  responsible for the behavior and provenance of replacement command paths.
- **Code repositories:** code ingestion is disabled by default, reads only
  configured roots, does not follow symbolic links, and bounds both file size
  and file count. Default ignores cover common VCS, dependency, editor, build,
  `.env`, and minified-asset paths. Review custom roots and ignores carefully:
  source code commonly contains credentials that do not match simple filename
  rules. If a remote embedding provider is configured, indexed code chunks are
  sent to it just like other document text.
- **Clipboard:** with `sources.clipboard.skip_passwords: true`, entries that look
  like passwords are not indexed; `retention_days` bounds how long clipboard
  history is kept (`mindcli clipboard cleanup`).
- **Capture:** `mindcli add`, `mindcli save`, and TUI quick capture write
  portable Markdown files under `capture.inbox` before indexing them. MindCLI
  creates the inbox with mode `0700` and new capture files with mode `0600`, but
  these permissions do not encrypt the content or override access held by your
  user account. Text can come from arguments, stdin, or a local editor process.
  Captures are limited to 5 MiB and are immediately stored in the same local
  indexes listed above. `mindcli save` makes no page request unless
  `sources.browser.include_content` is true; when enabled, the browser fetch
  limits, content-type checks, cookie-free client, and domain policy below also
  apply. Failed fetches fall back to a URL-only Markdown capture.
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

## Research-session boundary

Resumed research sessions deliberately persist the full completed question and
generated answer, plus a snapshot of each cited source's ID, title, path, and
source type. Context rules reference live indexed documents. Deleting a session
removes its turns and context rules but does not delete the cited source
documents; deleting a source removes its live context rule while prior citation
snapshots remain in the session brief.

Prompt construction is deterministic and visible in the TUI: at most five
documents contribute 1,000 Unicode characters each, ordered pinned, added, then
search results. Only the newest four turns are sent as follow-up history, with
question/answer bounds shown in the interface. Older persisted turns remain
available for export but are not silently sent as prompt history.

Session output and Markdown briefs receive display-time redaction. The raw
session stored in SQLite is not changed unless index-time redaction already
removed matching text from its document sources; questions and model answers
themselves are stored as entered/generated. Brief files created with `--output`
use mode `0600`. If the configured LLM is remote, new questions, bounded source
context, and bounded recent session history are sent to that provider.

## Profile boundary

`--profile NAME` and `MINDCLI_PROFILE=NAME` select one profile before any store
is opened. MindCLI never implicitly searches across profiles. Each named
profile has its own configuration and defaults to a separate data directory and
capture inbox. That separates SQLite content (including tags, collections, and
sessions), the Bleve index, vector graph, embedding cache, source/provider
settings, redaction rules, and portable captures. The active profile is visible
in the TUI and diagnostics. `mindcli profile list` inspects validated config
filenames only and does not open databases or reveal indexed content.

Profiles are an organizational boundary within one operating-system account,
not encryption or multi-user access control. Their default note/PDF source paths
overlap until you edit each profile config, so configure work and personal
sources deliberately. Exact environment overrides such as
`MINDCLI_CONFIG_PATH`, `MINDCLI_STORAGE_PATH`, and `MINDCLI_CAPTURE_INBOX` can
also point multiple profiles at the same location; doing so explicitly weakens
the isolation described above.

## Digest boundary

`mindcli digest` is on-demand only; MindCLI does not schedule notifications or
send digest content anywhere by itself. Reports contain source titles, stable
IDs, paths, tags, previews, and activity times, so treat exported Markdown as
private indexed content. Display-time redaction is applied, and files created
through `--output` use mode `0600`.

If the configured LLM is available, digest synthesis sends the first five
bounded document excerpts and the activity question through that provider. A
remote provider therefore receives that content under its own privacy policy.
If synthesis fails or no LLM is configured, MindCLI writes a deterministic
local count summary instead. Successfully exporting a collection digest records
the collection as viewed; a failed write does not advance the boundary.

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
- `mindcli session delete NAME` — remove a session, its conversation, and its
  context rules.
- Delete one profile's data directory to wipe its indexes without touching
  another profile. Delete its capture inbox separately to remove portable
  capture files. Deleting the root data directory wipes every default-layout
  profile's indexes, but not source files or capture inboxes stored elsewhere.
