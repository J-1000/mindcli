// Package query provides hybrid search combining full-text and semantic results.
package query

import (
	"context"
	"sort"
	"strings"

	"github.com/jankowtf/mindcli/internal/embeddings"
	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
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
	return h.buildResults(ctx, fused, limit)
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
func (h *HybridSearcher) buildResults(ctx context.Context, fused []fusedEntry, limit int) (storage.SearchResults, error) {
	if len(fused) > limit {
		fused = fused[:limit]
	}

	results := make(storage.SearchResults, 0, len(fused))
	for _, f := range fused {
		doc, err := h.db.GetDocument(ctx, f.docID)
		if err != nil || doc == nil {
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
