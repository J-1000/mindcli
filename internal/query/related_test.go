package query

import (
	"context"
	"testing"

	"github.com/J-1000/mindcli/internal/storage"
)

func TestRelatedCombinesAndExplainsSignals(t *testing.T) {
	db, bleve, vectors := newHybridTestStores(t)
	ctx := context.Background()

	source, err := db.GetDocument(ctx, "doc1")
	if err != nil {
		t.Fatal(err)
	}
	source.Metadata = map[string]string{"tags": "project,go", "links": "https://example.com/spec"}
	candidate, err := db.GetDocument(ctx, "doc2")
	if err != nil {
		t.Fatal(err)
	}
	candidate.Content = "go language ownership and concurrency"
	candidate.Metadata = map[string]string{"tags": "project,rust", "links": "https://example.com/spec"}
	for _, doc := range []*storage.Document{source, candidate} {
		if err := db.UpdateDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
		if err := bleve.Index(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHybridSearcher(bleve, vectors, keywordEmbedder{}, db, 0.5)
	results, err := h.Related(ctx, source.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Document.ID != candidate.ID {
		t.Fatalf("Related() = %+v, want only doc2", results)
	}
	wantKinds := map[RelationKind]bool{
		RelationSemantic: true,
		RelationLexical:  true,
		RelationTags:     true,
		RelationLinks:    true,
	}
	for _, reason := range results[0].Reasons {
		delete(wantKinds, reason.Kind)
		if reason.Label() == "" {
			t.Errorf("empty label for reason %+v", reason)
		}
	}
	if len(wantKinds) != 0 {
		t.Errorf("missing relation reasons: %v; got %+v", wantKinds, results[0].Reasons)
	}
}

func TestRelatedFallsBackWithoutVectors(t *testing.T) {
	db, bleve, _ := newHybridTestStores(t)
	ctx := context.Background()

	source, err := db.GetDocument(ctx, "doc1")
	if err != nil {
		t.Fatal(err)
	}
	source.Metadata = map[string]string{"tags": "shared", "links": "reference"}
	candidate, err := db.GetDocument(ctx, "doc2")
	if err != nil {
		t.Fatal(err)
	}
	candidate.Content = "go programming patterns"
	candidate.Metadata = map[string]string{"tags": "shared", "links": "reference"}
	for _, doc := range []*storage.Document{source, candidate} {
		if err := db.UpdateDocument(ctx, doc); err != nil {
			t.Fatal(err)
		}
		if err := bleve.Index(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	h := NewHybridSearcher(bleve, nil, nil, db, 0.5)
	results, err := h.Related(ctx, source.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Document.ID != candidate.ID {
		t.Fatalf("fallback Related() = %+v, want doc2", results)
	}
	for _, reason := range results[0].Reasons {
		if reason.Kind == RelationSemantic {
			t.Fatalf("fallback unexpectedly reported semantic reason: %+v", results[0].Reasons)
		}
	}
}

func TestRelatedHandlesLimitsAndMissingDocuments(t *testing.T) {
	db, bleve, vectors := newHybridTestStores(t)
	h := NewHybridSearcher(bleve, vectors, keywordEmbedder{}, db, 0.5)

	results, err := h.Related(context.Background(), "doc1", 0)
	if err != nil || len(results) != 0 {
		t.Fatalf("Related(limit=0) = %+v, %v", results, err)
	}
	if _, err := h.Related(context.Background(), "missing", 10); err == nil {
		t.Fatal("Related() succeeded for missing source document")
	}
}
