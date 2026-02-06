package query

import (
	"testing"

	"github.com/jankowtf/mindcli/internal/search"
	"github.com/jankowtf/mindcli/internal/storage"
)

func TestExtractDocID(t *testing.T) {
	tests := []struct {
		key    string
		expect string
	}{
		{"abc123:0", "abc123"},
		{"abc123:42", "abc123"},
		{"nodash", "nodash"},
		{"a:b:c", "a:b"},
	}

	for _, tt := range tests {
		got := extractDocID(tt.key)
		if got != tt.expect {
			t.Errorf("extractDocID(%q) = %q, want %q", tt.key, got, tt.expect)
		}
	}
}

func TestFuseResults(t *testing.T) {
	h := &HybridSearcher{HybridWeight: 0.5}

	bm25Results := []search.SearchResult{
		{ID: "doc1", Score: 1.5},
		{ID: "doc2", Score: 1.0},
		{ID: "doc3", Score: 0.5},
	}

	vecResults := []storage.VectorResult{
		{Key: "doc3:0", Score: 0.95},
		{Key: "doc1:0", Score: 0.8},
		{Key: "doc4:0", Score: 0.7},
	}

	fused := h.fuseResults(bm25Results, vecResults)

	if len(fused) != 4 {
		t.Fatalf("expected 4 fused entries, got %d", len(fused))
	}

	// doc1 and doc3 appear in both lists, so should have higher RRF scores.
	// The top result should be doc1 or doc3 since they're in both.
	topIDs := map[string]bool{fused[0].docID: true, fused[1].docID: true}
	if !topIDs["doc1"] || !topIDs["doc3"] {
		t.Errorf("expected doc1 and doc3 in top 2, got %s and %s",
			fused[0].docID, fused[1].docID)
	}

	// All scores should be positive.
	for _, f := range fused {
		if f.rrfScore <= 0 {
			t.Errorf("expected positive RRF score for %s, got %f", f.docID, f.rrfScore)
		}
	}
}

func TestFuseResultsPureBM25(t *testing.T) {
	h := &HybridSearcher{HybridWeight: 0.0} // Pure BM25

	bm25Results := []search.SearchResult{
		{ID: "doc1", Score: 1.5},
		{ID: "doc2", Score: 1.0},
	}

	vecResults := []storage.VectorResult{
		{Key: "doc2:0", Score: 0.95},
		{Key: "doc3:0", Score: 0.8},
	}

	fused := h.fuseResults(bm25Results, vecResults)

	// With weight=0 (pure BM25), vector results should have 0 contribution.
	// doc1 should be first since it's rank 1 in BM25.
	if fused[0].docID != "doc1" {
		t.Errorf("expected doc1 first with pure BM25 weight, got %s", fused[0].docID)
	}
}

func TestFuseResultsPureVector(t *testing.T) {
	h := &HybridSearcher{HybridWeight: 1.0} // Pure vector

	bm25Results := []search.SearchResult{
		{ID: "doc1", Score: 1.5},
		{ID: "doc2", Score: 1.0},
	}

	vecResults := []storage.VectorResult{
		{Key: "doc2:0", Score: 0.95},
		{Key: "doc3:0", Score: 0.8},
	}

	fused := h.fuseResults(bm25Results, vecResults)

	// With weight=1 (pure vector), BM25 results should have 0 contribution.
	// doc2 should be first since it's rank 1 in vector results.
	if fused[0].docID != "doc2" {
		t.Errorf("expected doc2 first with pure vector weight, got %s", fused[0].docID)
	}
}
