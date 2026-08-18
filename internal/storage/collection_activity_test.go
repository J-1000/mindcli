package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCollectionViewTracksManualAndSmartActivity(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Now().UTC()
	documents := []*Document{
		{ID: "one", Source: SourceMarkdown, Path: "/one.md", Title: "One", ContentHash: "one", IndexedAt: now, ModifiedAt: now},
		{ID: "two", Source: SourceMarkdown, Path: "/two.md", Title: "Two", ContentHash: "two", IndexedAt: now, ModifiedAt: now},
	}
	for _, document := range documents {
		mustSucceed(t, db.InsertDocument(ctx, document))
	}

	manual := &Collection{Name: "manual"}
	mustSucceed(t, db.CreateCollection(ctx, manual))
	mustSucceed(t, db.AddToCollection(ctx, manual.ID, "one"))
	count, err := db.CountCollectionDocumentsAddedAfter(ctx, manual.ID, time.Time{})
	if err != nil || count != 1 {
		t.Fatalf("initial manual activity = %d, err=%v", count, err)
	}
	viewedAt := time.Now().UTC()
	mustSucceed(t, db.MarkCollectionViewed(ctx, manual.ID, []string{"one"}, viewedAt))
	loaded, err := db.GetCollection(ctx, manual.ID)
	if err != nil || loaded.LastViewedAt == nil || !loaded.LastViewedAt.Equal(viewedAt) {
		t.Fatalf("last viewed = %+v, err=%v", loaded, err)
	}
	count, err = db.CountCollectionDocumentsAddedAfter(ctx, manual.ID, viewedAt)
	if err != nil || count != 0 {
		t.Fatalf("viewed manual activity = %d, err=%v", count, err)
	}
	mustSucceed(t, db.AddToCollection(ctx, manual.ID, "two"))
	count, err = db.CountCollectionDocumentsAddedAfter(ctx, manual.ID, viewedAt)
	if err != nil || count != 1 {
		t.Fatalf("new manual activity = %d, err=%v", count, err)
	}
	added, err := db.ListCollectionDocumentIDsAddedAfter(ctx, manual.ID, viewedAt)
	if err != nil || len(added) != 1 || added[0] != "two" {
		t.Fatalf("recent manual additions = %#v, err=%v", added, err)
	}

	smart := &Collection{Name: "smart", Query: "alpha"}
	mustSucceed(t, db.CreateCollection(ctx, smart))
	unseen, err := db.FilterUnseenCollectionDocumentIDs(ctx, smart.ID, []string{"one", "two", "one"})
	if err != nil || len(unseen) != 2 {
		t.Fatalf("initial smart unseen = %#v, err=%v", unseen, err)
	}
	mustSucceed(t, db.MarkCollectionViewed(ctx, smart.ID, []string{"one"}, now))
	unseen, err = db.FilterUnseenCollectionDocumentIDs(ctx, smart.ID, []string{"one", "two"})
	if err != nil || len(unseen) != 1 || unseen[0] != "two" {
		t.Fatalf("incremental smart unseen = %#v, err=%v", unseen, err)
	}
	mustSucceed(t, db.MarkCollectionViewed(ctx, smart.ID, []string{"one", "two"}, now.Add(time.Minute)))
	unseen, err = db.FilterUnseenCollectionDocumentIDs(ctx, smart.ID, []string{"one", "two"})
	if err != nil || len(unseen) != 0 {
		t.Fatalf("viewed smart unseen = %#v, err=%v", unseen, err)
	}
	if err := db.MarkCollectionViewed(ctx, "missing", nil, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing collection view error = %v", err)
	}
}
