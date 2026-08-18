// Package query provides hybrid search combining full-text and semantic results.
package query

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/J-1000/mindcli/internal/embeddings"
	"github.com/J-1000/mindcli/internal/filter"
	"github.com/J-1000/mindcli/internal/search"
	"github.com/J-1000/mindcli/internal/storage"
)

// HybridSearcher combines BM25 full-text search with vector similarity search.
type HybridSearcher struct {
	bleve    *search.BleveIndex
	vectors  *storage.VectorStore
	embedder embeddings.Embedder
	db       *storage.DB

	// HybridWeight controls the balance: 0 = pure BM25, 1 = pure vector.
	HybridWeight float64
}

// NewHybridSearcher creates a hybrid searcher. The vector store and embedder
// may be nil, in which case only BM25 search is used.
func NewHybridSearcher(
	bleve *search.BleveIndex,
	vectors *storage.VectorStore,
	embedder embeddings.Embedder,
	db *storage.DB,
	hybridWeight float64,
) *HybridSearcher {
	return &HybridSearcher{
		bleve:        bleve,
		vectors:      vectors,
		embedder:     embedder,
		db:           db,
		HybridWeight: hybridWeight,
	}
}

// Search performs a hybrid search combining BM25 and vector results.
func (h *HybridSearcher) Search(ctx context.Context, queryStr string, limit int) (storage.SearchResults, error) {
	// If no vector search available, fall back to BM25 only.
	if h.vectors == nil || h.embedder == nil || h.vectors.Len() == 0 {
		return h.bm25Only(ctx, queryStr, limit)
	}

	// Run BM25 and vector search in parallel.
	type bm25Result struct {
		results []search.SearchResult
		err     error
	}
	type vecResult struct {
		results []storage.VectorResult
		err     error
	}

	bm25Ch := make(chan bm25Result, 1)
	vecCh := make(chan vecResult, 1)

	go func() {
		results, err := h.bleve.Search(ctx, queryStr, limit*2)
		bm25Ch <- bm25Result{results, err}
	}()

	go func() {
		// Generate embedding for the query.
		queryEmb, err := h.embedder.Embed(ctx, queryStr)
		if err != nil {
			vecCh <- vecResult{nil, err}
			return
		}
		results := h.vectors.Search(queryEmb, limit*2)
		vecCh <- vecResult{results, nil}
	}()

	bm25Res := <-bm25Ch
	vecRes := <-vecCh

	// If vector search failed, fall back to BM25 only.
	if vecRes.err != nil {
		return h.bm25Only(ctx, queryStr, limit)
	}
	if bm25Res.err != nil {
		return nil, bm25Res.err
	}

	// Fuse results using Reciprocal Rank Fusion.
	fused := h.fuseResults(bm25Res.results, vecRes.results)

	// Fetch full documents and build results.
	return h.buildResults(ctx, fused, filter.Set{}, limit)
}

// SearchParsed executes a typed parsed query consistently across full-text and
// semantic retrieval.
func (h *HybridSearcher) SearchParsed(ctx context.Context, parsed ParsedQuery, limit int) (storage.SearchResults, error) {
	filters, err := h.resolveFilters(ctx, parsed.Filters)
	if err != nil {
		return nil, err
	}
	text := parsed.Text
	if text == "" && len(filters.ExactPhrases) == 0 && parsed.SearchTerms != "" && filters.Empty() {
		text = parsed.SearchTerms
	}
	semanticText := strings.TrimSpace(strings.Join(append([]string{text}, filters.ExactPhrases...), " "))
	if semanticText == "" || h.vectors == nil || h.embedder == nil || h.vectors.Len() == 0 {
		return h.bm25OnlyFiltered(ctx, text, filters, limit)
	}

	candidateLimit := filteredCandidateLimit(limit)
	type bm25Result struct {
		results []search.SearchResult
		err     error
	}
	type vecResult struct {
		results []storage.VectorResult
		err     error
	}
	bm25Ch := make(chan bm25Result, 1)
	vecCh := make(chan vecResult, 1)
	go func() {
		results, err := h.bleve.SearchFiltered(ctx, text, filters, candidateLimit)
		bm25Ch <- bm25Result{results: results, err: err}
	}()
	go func() {
		embedding, err := h.embedder.Embed(ctx, semanticText)
		if err != nil {
			vecCh <- vecResult{err: err}
			return
		}
		vecCh <- vecResult{results: h.vectors.Search(embedding, candidateLimit)}
	}()

	bm25 := <-bm25Ch
	vector := <-vecCh
	if bm25.err != nil {
		return nil, bm25.err
	}
	if vector.err != nil {
		return h.bm25OnlyFiltered(ctx, text, filters, limit)
	}
	return h.buildResults(ctx, h.fuseResults(bm25.results, vector.results), filters, limit)
}

func (h *HybridSearcher) resolveFilters(ctx context.Context, filters filter.Set) (filter.Set, error) {
	return ResolveFilters(ctx, h.db, filters, time.Now())
}

// ResolveFilters resolves time conveniences and collection names into the
// concrete filter set used by every search backend.
func ResolveFilters(ctx context.Context, db *storage.DB, filters filter.Set, now time.Time) (filter.Set, error) {
	filters = filter.ResolveRelativeTime(filters, now)
	if len(filters.Collections) == 0 {
		return filters, nil
	}
	ids, err := db.ResolveCollectionDocumentIDs(ctx, filters.Collections)
	if err != nil {
		return filter.Set{}, err
	}
	filters.DocumentIDs = ids
	return filters, nil
}

func filteredCandidateLimit(limit int) int {
	if limit < 1 {
		limit = 1
	}
	candidates := limit * 20
	if candidates < 200 {
		candidates = 200
	}
	if candidates > 5000 {
		candidates = 5000
	}
	return candidates
}

// fusedEntry holds the combined RRF score for a document.
type fusedEntry struct {
	docID      string
	bm25Score  float64
	vecScore   float64
	rrfScore   float64
	chunkKey   string
	highlights map[string][]string
}

// fuseResults combines BM25 and vector results using Reciprocal Rank Fusion.
// RRF score = sum(1 / (k + rank)) for each result list.
func (h *HybridSearcher) fuseResults(bm25Results []search.SearchResult, vecResults []storage.VectorResult) []fusedEntry {
	const k = 60 // Standard RRF constant.

	entries := make(map[string]*fusedEntry)

	bm25Weight := 1.0 - h.HybridWeight
	vecWeight := h.HybridWeight

	// Score BM25 results by rank.
	for rank, r := range bm25Results {
		rrfContrib := bm25Weight * (1.0 / float64(k+rank+1))
		if e, ok := entries[r.ID]; ok {
			e.rrfScore += rrfContrib
			e.bm25Score = r.Score
			e.highlights = r.Highlights
		} else {
			entries[r.ID] = &fusedEntry{
				docID:      r.ID,
				bm25Score:  r.Score,
				rrfScore:   rrfContrib,
				highlights: r.Highlights,
			}
		}
	}

	// Score vector results by rank.
	for rank, r := range vecResults {
		docID := extractDocID(r.Key)
		rrfContrib := vecWeight * (1.0 / float64(k+rank+1))

		if e, ok := entries[docID]; ok {
			e.rrfScore += rrfContrib
			e.vecScore = r.Score
			if e.chunkKey == "" {
				e.chunkKey = r.Key
			}
		} else {
			entries[docID] = &fusedEntry{
				docID:    docID,
				vecScore: r.Score,
				rrfScore: rrfContrib,
				chunkKey: r.Key,
			}
		}
	}

	// Sort by RRF score.
	result := make([]fusedEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, *e)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].rrfScore > result[j].rrfScore
	})

	return result
}

// buildResults fetches full documents for the fused results.
func (h *HybridSearcher) buildResults(ctx context.Context, fused []fusedEntry, filters filter.Set, limit int) (storage.SearchResults, error) {
	results := make(storage.SearchResults, 0, min(limit, len(fused)))
	now := time.Now()
	for _, f := range fused {
		doc, err := h.db.GetDocument(ctx, f.docID)
		if err != nil || !filter.MatchesDocument(doc, filters, now) {
			continue
		}

		var highlights []string
		if f.highlights != nil {
			for _, frags := range f.highlights {
				highlights = append(highlights, frags...)
			}
		}

		results = append(results, &storage.SearchResult{
			Document:    doc,
			Score:       f.rrfScore,
			BM25Score:   f.bm25Score,
			VectorScore: f.vecScore,
			Highlights:  highlights,
			ChunkID:     f.chunkKey,
		})
		if len(results) >= limit {
			break
		}
	}

	return results, nil
}

func (h *HybridSearcher) bm25OnlyFiltered(ctx context.Context, text string, filters filter.Set, limit int) (storage.SearchResults, error) {
	bleveResults, err := h.bleve.SearchFiltered(ctx, text, filters, filteredCandidateLimit(limit))
	if err != nil {
		return nil, err
	}
	results := make(storage.SearchResults, 0, min(limit, len(bleveResults)))
	now := time.Now()
	for _, result := range bleveResults {
		doc, err := h.db.GetDocument(ctx, result.ID)
		if err != nil || !filter.MatchesDocument(doc, filters, now) {
			continue
		}
		var highlights []string
		for _, fragments := range result.Highlights {
			highlights = append(highlights, fragments...)
		}
		results = append(results, &storage.SearchResult{
			Document: doc, Score: result.Score, BM25Score: result.Score, Highlights: highlights,
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// bm25Only performs BM25-only search and returns full results.
func (h *HybridSearcher) bm25Only(ctx context.Context, queryStr string, limit int) (storage.SearchResults, error) {
	bleveResults, err := h.bleve.Search(ctx, queryStr, limit)
	if err != nil {
		return nil, err
	}

	results := make(storage.SearchResults, 0, len(bleveResults))
	for _, r := range bleveResults {
		doc, err := h.db.GetDocument(ctx, r.ID)
		if err != nil || doc == nil {
			continue
		}

		var highlights []string
		for _, frags := range r.Highlights {
			highlights = append(highlights, frags...)
		}

		results = append(results, &storage.SearchResult{
			Document:   doc,
			Score:      r.Score,
			BM25Score:  r.Score,
			Highlights: highlights,
		})
	}

	return results, nil
}

// extractDocID extracts the document ID from a chunk key (format: "docID:chunkIndex").
func extractDocID(chunkKey string) string {
	if idx := strings.LastIndex(chunkKey, ":"); idx != -1 {
		return chunkKey[:idx]
	}
	return chunkKey
}
