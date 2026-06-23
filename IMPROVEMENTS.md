# MindCLI — Improvement Plan

A prioritized, actionable backlog from a full code review (2026-06-22). Each item lists
**severity**, **location** (`file:line`), the **problem**, and an **implementation hint**.
Complements `plan.md` (original build plan) and `progress.txt`.

Legend: 🔴 High · 🟠 Medium · 🟡 Low · ✨ Feature · 🧹 Polish · 🧪 Test

## Suggested order

The first five unblock or simplify the rest:

1. **#1** Embedding cache / model key (correctness landmine)
2. **#4** Persist vectors in the watcher (silent data loss)
3. **#7** Expand `~` in config paths (most users hit this)
4. **#2 / #3** Resolve the OpenAI + time-filter "documented but not implemented" gaps
5. **Polish P1** `openStores()` refactor of `main.go` — makes #3, #13 and future work one-liners

---

## 1. Correctness bugs

### 🔴 #1 — Embedding cache ignores the model name
- **Where:** `internal/embeddings/cache.go:105` (`contentHash(text)`), schema at `:28-36`; vector store `internal/storage/vector.go`.
- **Problem:** Cache key is `sha256(text)` only. Changing `embeddings.model` returns the *previous* model's cached vectors; differing dimensions then corrupt the HNSW graph silently. The graph has no model/dim stamp either.
- **Fix:**
  - Add a `model string` field to `CachedEmbedder`; pass it through `NewCachedEmbedder(inner, cachePath, model)`. Key on the composite: either `contentHash(model + "\x00" + text)` or make the table PK `(model, content_hash)`.
  - Stamp the vector store: write a sidecar `vectors.meta.json` (`{"model": "...", "dim": 768}`) next to `vectors.graph`. On load, compare to the configured model/dim; on mismatch, refuse to load and prompt a re-index (or auto-rebuild).
  - Defensive: in `VectorStore.Add`/`AddBatch`, reject vectors whose `len` differs from the established dimension.
- **Tie-in:** `mindcli doctor` (Feature T1) should surface a model/dim mismatch.

### 🟠 #2 — `provider: openai` is documented but unimplemented
- **Where:** validated at `internal/config/config.go:169`; only wired for ollama in `cmd/mindcli/main.go:262,367` etc.; README + `MINDCLI_EMBEDDINGS_OPENAI_KEY` env var; `plan.md` lists `embeddings/openai.go`. No such file exists.
- **Problem:** Selecting `openai` passes validation, then silently disables semantic search **and** LLM answers (embedder + `LLMClient` are ollama-only).
- **Fix (choose one):**
  - **Implement:** add `internal/embeddings/openai.go` implementing `Embedder` (POST `https://api.openai.com/v1/embeddings`, model `text-embedding-3-small`, `Authorization: Bearer <key>`). Add a `newEmbedder(cfg)` factory in `main.go` that switches on `cfg.Embeddings.Provider`. Make `Validate()` require `OpenAIKey` when provider is `openai`. Note the LLM (`ask`) path is still ollama-only — either add an OpenAI chat client or document the limitation.
  - **Or remove:** drop `openai` from `Validate()`, the README, the config comment, and the env-var docs until it's real.

### 🟠 #3 — `TimeFilter` is parsed but never applied
- **Where:** set at `internal/query/parser.go:158`; only consumed as a status label at `internal/tui/app.go:290`.
- **Problem:** README and `plan.md` Phase 9 advertise time filtering ("last week", "...last month"). Results are never filtered.
- **Fix:**
  - In `parser.go`, add `func TimeRange(filter string, now time.Time) (start, end time.Time, ok bool)` mapping the existing keywords to ranges. Inject `now` for testability.
  - Apply centrally: filter `SearchResults` by `doc.ModifiedAt ∈ [start,end]`. Cheapest correct spot is `HybridSearcher.buildResults`/`bm25Only` (post-fetch), so all callers benefit. (A Bleve date-range query is faster but requires `modified_at` to be a mapped date field in `search/bleve.go` — verify the mapping first.)
  - Thread the parsed range from `ParseQuery` into the searcher (extend `Search` signature or add `SearchWithFilters`).

### 🟠 #4 — Watcher never persists embeddings
- **Where:** `SaveVectors()` called only at `cmd/mindcli/main.go:294` (batch index). `internal/index/watcher.go:150` calls `IndexFile` (in-memory add) but never saves.
- **Problem:** Embeddings created during `mindcli watch` / `index -watch` are lost on exit and grow memory unbounded.
- **Fix:** In `watcher.go`'s `processPending`, track a `dirty` flag; after processing a tick's batch, if dirty call `w.indexer.SaveVectors()` once. Also save on shutdown (the `ctx.Done()`/`w.done` branches in `debounceLoop`). Saving per tick (not per file) keeps it cheap.

### 🟠 #5 — `ContentHash` is stored everywhere but never used for change detection
- **Where:** `internal/index/indexer.go:185-194` compares mtime only (comment says "compare hash"). Hash computed in every source's `Parse`; column indexed at `storage/sqlite.go:70` but never read for diffing.
- **Problem:** Content-restored files (git checkout, `rsync -t`) with unchanged mtime are missed; `touch`ed files re-embed needlessly.
- **Fix:** Keep mtime as the cheap fast-path skip. When mtime is newer, `Parse`, then compare `doc.ContentHash == existing.ContentHash` — if equal, skip re-embedding/upsert (optionally just bump `modified_at`). For a stronger guarantee, compute a cheap hash during `Scan` and carry it on `sources.FileInfo` so you can skip `Parse` entirely.

### 🟠 #6 — Renames/moves leak orphaned documents
- **Where:** `internal/index/watcher.go:79` (`handleEvent`); batch `index` never prunes either.
- **Problem:** A rename creates a new doc but the old path's document/chunks/vectors/Bleve entry persist forever.
- **Fix:**
  - In `handleEvent`, treat `fsnotify.Rename` on `event.Name` like a removal (`RemoveFile`); the follow-up `Create` indexes the new path.
  - Add a reconcile/prune pass usable by both `index` and a new `mindcli clean`: list filesystem-source docs, `os.Stat` each `path`, `RemoveFile` the missing ones. This also fixes batch `index` never noticing deletions.

### 🟠 #7 — `~` not expanded in config paths
- **Where:** `expandUserPath` applied only to `MINDCLI_CONFIG_*` (`config.go:224,237`); `storage.path` and `sources.*.paths` are used raw. The watcher has its own `expandWatchPath` (`watcher.go:174`) — so expansion is inconsistent (watch works, index/storage don't).
- **Problem:** README's config example (`~/notes`, `~/.local/share/mindcli`) creates literal `~` directories for anyone who edits config by hand.
- **Fix:** In `config.Load()`, after unmarshal + env overrides, expand all path fields: `Storage.Path`, and each of `Sources.{Markdown,PDF,Email}.Paths`. Add `expandPaths([]string) []string`. Then delete the watcher's bespoke `expandWatchPath` and rely on already-expanded config.

### 🟠 #8 — Privacy is display-only; stored content is cleartext
- **Where:** `Redactor` used at `main.go:487,592,1058` and `app.go:673,788` (output only). Email masking only touches `preview` (`internal/index/sources/email.go` ~463); `Content: content` is the raw body.
- **Problem:** `Document.Content` in SQLite + Bleve is never redacted; emails store full addresses/tokens even with `mask_sensitive_preview: true`. Undercuts the "Private" headline.
- **Fix (layered):**
  - Give `Indexer` a `privacy.Redactor`; after `Parse`, redact `doc.Content` (and `doc.Preview`) before upsert/index when redaction is enabled. (Trade-off: original text is unrecoverable — make it opt-in via a `privacy.redact_content: true` flag.)
  - For email, when masking is on, also mask `Content` (or add `mask_content`).
  - Write `docs/PRIVACY.md` (see Bigger Ideas) documenting what is stored where.

### 🟡 #9 — Silent failures in the embed path
- **Where:** `internal/index/indexer.go:367` (`DeleteChunksByDocument` err dropped), `:402` (`InsertChunk` err dropped), `:405` (`AddBatch` is `void`); embed failure at `:384` returns without incrementing the error counter.
- **Problem:** A document can land in SQLite+Bleve with no vectors while the run reports success.
- **Fix:** Check and surface each error via `idx.progress.OnError`; make `VectorStore.AddBatch` return `error`; count embed failures toward `stats.Errors` (or a distinct `EmbedErrors`).

### 🟡 #10 — CLI `search` diverges from the TUI/export
- **Where:** `cmd/mindcli/main.go:458-490` — Bleve-only, hardcoded limit `20`, ignores `cfg.Search.ResultsLimit`. `export`/`ask` build a hybrid searcher.
- **Fix:** Route `runSearch` through the same shared search helper as `export`/`ask` (falls out naturally from the `openStores` refactor) and use `cfg.Search.ResultsLimit`.

### 🟡 #11 — `VectorStore.DeleteByPrefix` is a dead no-op
- **Where:** `internal/storage/vector.go:106-113` — empty body, misleading doc comment.
- **Fix:** Delete it (deletion already works via `deleteDocumentVectors` → per-chunk `Delete`). Or, if you want prefix delete, maintain an external `map[docID][]chunkKey` since `coder/hnsw` can't iterate.

### 🟡 #12 — Safari history query is nondeterministic
- **Where:** `internal/index/sources/browser.go:351`.
- **Problem:** `GROUP BY hi.url` with bare `hv.title`/`hi.visit_count` and post-group `ORDER BY hv.visit_time` picks an arbitrary visit's title/recency.
- **Fix:** Aggregate explicitly, e.g. join each url to its latest visit:
  ```sql
  SELECT hi.url, hv.title, hi.visit_count, MAX(hv.visit_time) AS vt
  FROM history_items hi
  JOIN history_visits hv ON hi.id = hv.history_item
  WHERE hv.title IS NOT NULL AND hv.title != ''
  GROUP BY hi.url
  ORDER BY vt DESC
  LIMIT 5000;
  ```
  Also add `rows.Err()` checks after the scan loops (applies to all browser readers).

### 🟡 #13 — `go vet` fails
- **Where:** `internal/index/sources/scanner_test.go:146,162` — `cancel` not used on all paths (context leak).
- **Fix:** `ctx, cancel := …; defer cancel()` immediately after creation.

---

## 2. Polish & code quality

### 🧹 P1 — Collapse `main.go` boilerplate (highest-leverage)
- **Where:** `cmd/mindcli/main.go` — `runTUI/runIndex/runWatch/runSearch/runExport/runClipboard/runAsk` each repeat `config → DataDir → DatabasePath → storage.Open → Bleve → vectors → embedder`. Divergence here caused #10.
- **Fix:** Introduce:
  ```go
  type stores struct {
      cfg      *config.Config
      db       *storage.DB
      bleve    *search.BleveIndex
      vectors  *storage.VectorStore   // may be nil
      embedder embeddings.Embedder    // may be nil
      llm      *query.LLMClient       // may be nil
      hybrid   *query.HybridSearcher  // may be nil
  }
  func openStores(cfg *config.Config, opts storeOpts) (*stores, error) { ... }
  func (s *stores) Close() error { ... }
  ```
  `opts` gates whether vectors/embedder/LLM are required vs optional. Each command shrinks to ~5 lines and shares one consistent search path.

### 🧹 P2 — Break up the TUI monolith
- **Where:** `internal/tui/app.go` (1049 lines); `internal/tui/components/` exists but is **empty**; `plan.md` specified `search.go/results.go/preview.go/status.go`.
- **Fix:** Extract render + update logic per panel into `components/`. Keeps `Model` thin and makes the TUI unit-testable (see 🧪 T2).

### 🧹 P3 — Dead keymap bindings
- **Where:** `internal/tui/keys.go:20-25` (`PageUp/PageDown/HalfUp/HalfDown`) — never referenced in `app.go`. They work in Preview only because `viewport` handles them; Results-pane half/page scroll silently does nothing.
- **Fix:** Either wire them in `updateResults` (move `m.cursor` by a page) or remove them and let `viewport` own scrolling in Preview.

### 🧹 P4 — Add `LICENSE`
- **Where:** repo root — README says MIT, no file present.
- **Fix:** Add a standard MIT `LICENSE` (author: Jan Kowalski / `jankowtf`, year 2026).

### 🧹 P5 — Real migration path
- **Where:** `internal/storage/sqlite.go:54-117` — `schema_version` inserted but never read; all `CREATE IF NOT EXISTS`.
- **Fix:** Read `schema_version`, keep an ordered `[]migration{version, sql}` list, apply those above the current version in a transaction, then bump. Needed before any column change ships.

### 🧹 P6 — Per-keystroke DB I/O in the TUI
- **Where:** `internal/tui/app.go:773` — `GetDocumentCollections` runs synchronously on every `j`/`k`.
- **Fix:** Load collection membership via a `tea.Cmd` (async) and cache per doc ID, or fold it into the initial `docsLoadedMsg`.

### 🧹 P7 — Status-bar width math uses bytes
- **Where:** `internal/tui/app.go:992` — `len(m.statusMsg)` counts bytes; misaligns with multibyte/wide text.
- **Fix:** Use `lipgloss.Width(...)` for padding calculations.

### 🧹 P8 — Storage duplication (note only)
- Content is stored in `documents.content`, `chunks.content`, and Bleve. Fine at personal scale; revisit if targeting large corpora (e.g. drop `chunks.content` — it's only read for IDs in `deleteDocumentVectors`).

---

## 3. Testing

- 🧪 **T1 — Hybrid `Search()`** (`internal/query/hybrid.go:44`, currently 0%): test the full flow with a fake `Embedder`, a tmp Bleve index, and a tmp SQLite DB — assert RRF ordering, the BM25-only fallback (nil vectors), and the embed-failure fallback.
- 🧪 **T2 — Watcher** (`internal/index/watcher.go`, 0%): temp dir → write file → assert re-index; rename → assert old removed + new added (covers #6); delete → assert removed; verify debounce coalescing. Replace fixed `time.Sleep(100ms)` with an `eventually()` poll helper to kill flakiness (same fix for `bleve_test.go`/`indexer_test.go`).
- 🧪 **T3 — Source parsers** happy paths (browser/email/pdf/clipboard ~0%): commit small fixtures (a tiny mbox, a generated SQLite history DB, a 1-page PDF) and assert extracted fields.
- 🧪 **T4 — Benchmarks:** `BenchmarkChunkerSplit`, `BenchmarkHybridSearch`, `BenchmarkBleveIndex` — the `plan.md` perf targets (10k docs < 5 min, search < 100ms) are currently unverified.
- 🧪 **T5:** fix the `go vet` leak (#13) so CI lint stays green.

---

## 4. Features

### Tier 1 — close the biggest UX gaps
- ✨ **In-TUI indexing + first-run empty state.** Today a fresh user sees an empty TUI and `r` only reloads from the DB (`app.go:472`). Add an `i` action that runs `IndexAll` via a `tea.Cmd`, streaming the existing `ProgressReporter` into the status bar; show "No documents yet — press `i` to index" when `results` is empty.
- ✨ **Show match snippets/highlights.** `Highlights` are computed and fused (`hybrid.go:180`) but unused in the UI — results show only titles (`app.go:884`) and preview shows raw content head. Render the highlighted fragment in both.
- ✨ **Search-as-you-type** (debounced) instead of Enter-only (`updateSearch`, `app.go:344`). Use a trailing-debounce `tea.Tick` keyed on input version.
- ✨ **`mindcli doctor`.** Check Ollama reachability + that the model is pulled, that configured paths exist/are readable, and that the stored vector dim matches the configured model (surfaces #1).

### Tier 2 — leverage what's already half-built
- ✨ **Smart collections.** `Collection.Query` exists in the schema (`storage/sqlite.go:92`) and is settable via `--query` but never executed. Make a query-backed collection run its search live instead of showing a static list.
- ✨ **In-TUI filters** for source / tag / date range (toggle keys), not just the NL "in my emails" path.
- ✨ **Conversational follow-ups** (plan Phase 9). The `ask`/stream is single-shot; add a chat history buffer + "tell me more" that re-feeds prior turns.
- ✨ **`mindcli stats`** — counts by source, index size on disk, embedding coverage (% docs with vectors), last-indexed. Data already in the DB (`CountDocumentsBySource`, etc.).
- ✨ **`mindcli reindex --force` / `mindcli clean`** — full rebuild and orphan prune (shares the reconcile pass from #6). Plus `config --edit` (`$EDITOR`) and `config --path`.

### Tier 3 — reach
- ✨ More sources: code repos (language-aware chunking), Obsidian/Apple Notes, `.docx`/EPUB, RSS, chat exports.
- ✨ **OCR fallback** for image-only PDFs (was in `plan.md` Phase 4) via an external tool (`tesseract`) behind a config flag.
- ✨ **Inline `[1][2]` citations** in RAG answers tied to the source list (`runAsk`, `main.go:1082`).
- ✨ **Daemon mode** — ship launchd/systemd unit files so the watcher runs in the background (plan Phase 8).

---

## 5. Bigger ideas

- 💡 **First-class embedding-model upgrades.** Building on #1: stamp model+dim in the vector store, detect mismatch on startup, and offer a guided re-embed. Turns a footgun into a feature.
- 💡 **Privacy & threat model doc + at-rest encryption.** Write `docs/PRIVACY.md` stating plainly that browser/clipboard/email/notes content is stored in cleartext SQLite and that redaction is display-time. Then consider a build-tagged SQLCipher option (`mattn/go-sqlite3` supports SQLCipher builds) as a real differentiator.
- 💡 **Verify the performance targets.** Land the T4 benchmarks and add a "Performance" section to the README with measured numbers, replacing the aspirational `plan.md` metrics.

---

## Status

All correctness bugs and polish items are implemented, along with most of the
feature backlog. Remaining items are deliberately deferred (see below).

```
Correctness
[x] #1  cache keys on model + dim-stamp vector store
[x] #2  implement OpenAI provider (embeddings + chat)
[x] #3  apply TimeFilter to results
[x] #4  watcher persists vectors (per-batch + on shutdown)
[x] #5  use ContentHash for change detection
[x] #6  prune pass for deleted files (mindcli clean)
[x] #7  expand ~ in all config paths (+ env overrides without a config file)
[x] #8  index-time redaction (opt-in) + email content masking
[x] #9  stop swallowing embed-path errors
[x] #10 route CLI search through hybrid + results_limit
[x] #11 remove DeleteByPrefix no-op
[x] #12 fix Safari query determinism + rows.Err()
[x] #13 fix go vet context leak

Polish
[x] P1 openStores() refactor    [~] P2 split TUI components (deferred)
[x] P3 keymap bindings          [x] P4 LICENSE
[x] P5 real migrations          [x] P6 async collection counts
[x] P7 lipgloss.Width padding   [ ] P8 storage dedup (optional, not pursued)

Testing
[x] T1 hybrid Search   [x] T2 watcher   [x] T3 parsers (pre-existing)
[x] T4 benchmarks      [x] T5 vet clean

Features
[x] T1: in-TUI index, snippets, search-as-you-type, doctor
[x] T2: smart collections, filters, follow-ups, stats, reindex/clean
[~] T3: citations [x], daemon files [x]; more sources / OCR (deferred)
```

## Deferred (with rationale)

- **P2 — split the TUI into components.** Pure refactor with real regression
  risk against a now feature-rich `app.go`, and no user-facing benefit. The
  empty `components/` dir remains as the intended home if/when this is done.
- **T3 — additional source types (docx, EPUB, RSS) and OCR.** Each adds a parser
  and external dependency (OCR needs `tesseract`); shipping them half-built
  would lower quality. Note: the markdown source already indexes arbitrary text
  extensions via `sources.markdown.extensions`, covering org-mode/AsciiDoc/code.
- **P8 — storage de-duplication.** Acceptable at personal scale; revisit only if
  targeting very large corpora.
