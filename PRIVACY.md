# Privacy & Threat Model

MindCLI is built to keep your data on your machine. This document is explicit
about **what is stored, where, and in what form**, so you can make an informed
decision.

## What stays local

- **All indexed content** lives under your data directory (default
  `~/.local/share/mindcli`): the SQLite database, the Bleve full-text index, the
  HNSW vector graph, and the embedding cache.
- **No telemetry.** MindCLI makes no network calls except to the embedding/LLM
  backend you configure.
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

So a note, PDF, email, browser title, or clipboard entry that you index is
searchable in cleartext on disk.

## Redaction

Redaction has two layers, controlled by `privacy.redact_patterns`:

- **Display-time (default):** matches are replaced with `[REDACTED]` in search
  output, exports, and generated answers. The underlying stored content is
  **not** changed.
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
- **Browser:** only titles/URLs are indexed unless `include_content` is enabled.

## What MindCLI does not (yet) do

- **No at-rest encryption.** The database and indexes are not encrypted. If your
  threat model includes disk theft or multi-user machines, use **full-disk
  encryption** (FileVault, LUKS, BitLocker). A SQLCipher-backed build is a
  possible future option.
- **No per-document access control.** Anything you index is searchable by anyone
  with read access to the data directory.

## Removing data

- `mindcli clipboard clear` / `cleanup` — remove clipboard entries.
- `mindcli clean` — remove documents whose source files no longer exist.
- Delete the data directory to wipe everything.
