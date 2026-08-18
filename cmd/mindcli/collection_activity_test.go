package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/config"
	"github.com/J-1000/mindcli/internal/search"
	"github.com/J-1000/mindcli/internal/storage"
)

func TestRunCollectionShowReportsAndAdvancesActivity(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	t.Setenv("MINDCLI_CONFIG_PATH", filepath.Join(tmpDir, "missing.yaml"))
	t.Setenv("MINDCLI_STORAGE_PATH", dataDir)
	t.Setenv("MINDCLI_CAPTURE_INBOX", filepath.Join(tmpDir, "inbox"))
	original := activeProfile
	activeProfile = config.DefaultProfileName
	t.Cleanup(func() { activeProfile = original })
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(dataDir, "mindcli.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	doc := &storage.Document{ID: "reading-doc", Source: storage.SourceMarkdown, Path: "/reading.md", Title: "Reading", ContentHash: "hash", IndexedAt: now, ModifiedAt: now}
	if err := db.InsertDocument(context.Background(), doc); err != nil {
		closeTestDB(t, db)
		t.Fatal(err)
	}
	collection := &storage.Collection{Name: "reading"}
	if err := db.CreateCollection(context.Background(), collection); err != nil {
		closeTestDB(t, db)
		t.Fatal(err)
	}
	if err := db.AddToCollection(context.Background(), collection.ID, doc.ID); err != nil {
		closeTestDB(t, db)
		t.Fatal(err)
	}
	closeTestDB(t, db)

	first := captureProfileStdout(t, func() error { return runCollection([]string{"show", "reading"}) })
	second := captureProfileStdout(t, func() error { return runCollection([]string{"show", "reading"}) })
	if !strings.Contains(first, "New since last view: 1") || !strings.Contains(first, "Last viewed: never") {
		t.Fatalf("first collection view:\n%s", first)
	}
	if !strings.Contains(second, "New since last view: 0") || strings.Contains(second, "Last viewed: never") {
		t.Fatalf("second collection view:\n%s", second)
	}
}

func TestSmartCollectionDigestUsesSeenSnapshot(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.Open(filepath.Join(tmpDir, "mindcli.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestDB(t, db)
	searchIndex, err := search.NewBleveIndex(filepath.Join(tmpDir, "search.bleve"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestIndex(t, searchIndex)
	now := time.Now().UTC()
	doc := &storage.Document{ID: "old-match", Source: storage.SourceMarkdown, Path: "/alpha.md", Title: "Alpha", Content: "alpha material", ContentHash: "hash", IndexedAt: now.Add(-30 * 24 * time.Hour), ModifiedAt: now.Add(-30 * 24 * time.Hour)}
	if err := db.InsertDocument(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	if err := searchIndex.Index(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	collection := &storage.Collection{Name: "smart", Query: "alpha"}
	if err := db.CreateCollection(context.Background(), collection); err != nil {
		t.Fatal(err)
	}
	s := &stores{db: db, bleve: searchIndex, cfg: config.Default()}
	after := now.Add(-7 * 24 * time.Hour)
	report, loaded, currentIDs, err := buildDigestReport(context.Background(), s, collection.Name, after, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != collection.ID || len(report.Items) != 1 || !strings.Contains(strings.Join(report.Items[0].Reasons, ","), "new in collection") {
		t.Fatalf("initial smart digest = %+v", report)
	}
	if err := db.MarkCollectionViewed(context.Background(), collection.ID, currentIDs, now); err != nil {
		t.Fatal(err)
	}
	report, _, _, err = buildDigestReport(context.Background(), s, collection.Name, after, now.Add(time.Second), 10)
	if err != nil || len(report.Items) != 0 {
		t.Fatalf("viewed smart digest = %+v, err=%v", report, err)
	}
}
