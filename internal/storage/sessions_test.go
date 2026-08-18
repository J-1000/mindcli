package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResearchSessionLifecyclePersistsTurnsAndContext(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

	session := &ResearchSession{Name: "Release Research", CreatedAt: base, UpdatedAt: base}
	mustSucceed(t, db.CreateSession(ctx, session))
	if err := db.CreateSession(ctx, &ResearchSession{Name: "release research"}); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("case-insensitive duplicate error = %v", err)
	}
	loaded, err := db.GetSessionByName(ctx, "RELEASE RESEARCH")
	if err != nil || loaded.ID != session.ID {
		t.Fatalf("loaded session = %+v, err=%v", loaded, err)
	}

	documents := []*Document{
		{ID: "included", Source: SourceMarkdown, Path: "/included.md", Title: "Included", ContentHash: "i", IndexedAt: base, ModifiedAt: base},
		{ID: "pinned", Source: SourcePDF, Path: "/pinned.pdf", Title: "Pinned", ContentHash: "p", IndexedAt: base, ModifiedAt: base},
		{ID: "excluded", Source: SourceBrowser, Path: "https://example.com", Title: "Excluded", ContentHash: "e", IndexedAt: base, ModifiedAt: base},
	}
	for _, document := range documents {
		mustSucceed(t, db.InsertDocument(ctx, document))
	}
	mustSucceed(t, db.SetSessionDocumentState(ctx, session.ID, "included", SessionDocumentIncluded))
	mustSucceed(t, db.SetSessionDocumentState(ctx, session.ID, "pinned", SessionDocumentPinned))
	mustSucceed(t, db.SetSessionDocumentState(ctx, session.ID, "excluded", SessionDocumentExcluded))
	if err := db.SetSessionDocumentState(ctx, session.ID, "included", "unknown"); err == nil {
		t.Fatal("invalid session document state succeeded")
	}
	contextDocuments, err := db.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(contextDocuments) != 3 || contextDocuments[0].Document.ID != "pinned" || contextDocuments[1].Document.ID != "included" || contextDocuments[2].Document.ID != "excluded" {
		t.Fatalf("session document order = %+v", contextDocuments)
	}

	turns := []*SessionTurn{
		{SessionID: session.ID, Question: "What changed?", Answer: "The API changed [1].", CreatedAt: base.Add(time.Minute), Citations: []SessionCitation{{DocumentID: "pinned", Title: "Pinned", Path: "/pinned.pdf", Source: SourcePDF}}},
		{SessionID: session.ID, Question: "What next?", Answer: "Ship it.", CreatedAt: base.Add(2 * time.Minute)},
	}
	for _, turn := range turns {
		mustSucceed(t, db.AddSessionTurn(ctx, turn))
	}
	gotTurns, err := db.ListSessionTurns(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTurns) != 2 || gotTurns[0].Question != turns[0].Question || len(gotTurns[0].Citations) != 1 || gotTurns[0].Citations[0].DocumentID != "pinned" {
		t.Fatalf("session turns = %+v", gotTurns)
	}

	// Citation snapshots survive source deletion while live context joins do not.
	mustSucceed(t, db.DeleteDocument(ctx, "pinned"))
	gotTurns, err = db.ListSessionTurns(ctx, session.ID)
	if err != nil || gotTurns[0].Citations[0].Path != "/pinned.pdf" {
		t.Fatalf("citation after document deletion = %+v, err=%v", gotTurns, err)
	}
	contextDocuments, err = db.ListSessionDocuments(ctx, session.ID)
	if err != nil || len(contextDocuments) != 2 {
		t.Fatalf("context after document deletion = %+v, err=%v", contextDocuments, err)
	}

	mustSucceed(t, db.RemoveSessionDocument(ctx, session.ID, "included"))
	if err := db.RemoveSessionDocument(ctx, session.ID, "included"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second context removal error = %v", err)
	}
	mustSucceed(t, db.DeleteSession(ctx, session.ID))
	if _, err := db.GetSession(ctx, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session lookup error = %v", err)
	}
	if turns, err := db.ListSessionTurns(ctx, session.ID); err != nil || len(turns) != 0 {
		t.Fatalf("turns after session deletion = %+v, err=%v", turns, err)
	}
}

func TestResearchSessionsListByRecentActivity(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	older := &ResearchSession{Name: "older", CreatedAt: base, UpdatedAt: base}
	newer := &ResearchSession{Name: "newer", CreatedAt: base, UpdatedAt: base.Add(time.Hour)}
	mustSucceed(t, db.CreateSession(ctx, older))
	mustSucceed(t, db.CreateSession(ctx, newer))
	sessions, err := db.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != newer.ID || sessions[1].ID != older.ID {
		t.Fatalf("session activity order = %+v", sessions)
	}
}
