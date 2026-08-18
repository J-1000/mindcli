package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/storage"
)

func TestValidateSessionName(t *testing.T) {
	if got, err := validateSessionName("  release research  "); err != nil || got != "release research" {
		t.Fatalf("validateSessionName() = %q, %v", got, err)
	}
	for _, invalid := range []string{" ", "bad\nname", strings.Repeat("x", maxSessionNameRunes+1)} {
		if _, err := validateSessionName(invalid); err == nil {
			t.Errorf("validateSessionName(%q) succeeded", invalid)
		}
	}
}

func TestWriteSessionBriefMarksGeneratedContentAndRedacts(t *testing.T) {
	redactor, errs := privacy.NewRedactor([]string{"SECRET"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	base := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	session := &storage.ResearchSession{Name: "SECRET research", CreatedAt: base, UpdatedAt: base.Add(time.Hour)}
	turns := []*storage.SessionTurn{{
		Question: "Where is SECRET?", Answer: "Generated SECRET answer [1]", CreatedAt: base,
		Citations: []storage.SessionCitation{{DocumentID: "doc-1", Title: "SECRET source", Path: "/SECRET.md", Source: storage.SourceMarkdown}},
	}}
	documents := []*storage.SessionDocument{{
		Document: &storage.Document{ID: "doc-2", Title: "Context", Path: "/context.md", Source: storage.SourceMarkdown},
		State:    storage.SessionDocumentPinned,
	}}
	var output bytes.Buffer
	if err := writeSessionBrief(&output, session, turns, documents, redactor); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"# Research brief: [REDACTED] research", "## Final synthesis", "generated content", "**Generated answer**", "ID `doc-1`", "pinned", "## Source list"} {
		if !strings.Contains(text, want) {
			t.Errorf("session brief missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SECRET") {
		t.Fatalf("session brief leaked unredacted content:\n%s", text)
	}
}

func TestRunSessionManagesContextAndPrivateExport(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	t.Setenv("MINDCLI_CONFIG_PATH", filepath.Join(tmpDir, "missing.yaml"))
	t.Setenv("MINDCLI_STORAGE_PATH", dataDir)
	t.Setenv("MINDCLI_CAPTURE_INBOX", filepath.Join(tmpDir, "inbox"))

	if err := runSession([]string{"create", "release"}); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(filepath.Join(dataDir, "mindcli.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	doc := &storage.Document{ID: "session-doc", Source: storage.SourceMarkdown, Path: "/notes/release.md", Title: "Release notes", ContentHash: "hash", IndexedAt: now, ModifiedAt: now}
	if err := db.InsertDocument(context.Background(), doc); err != nil {
		closeTestDB(t, db)
		t.Fatal(err)
	}
	session, err := db.GetSessionByName(context.Background(), "release")
	if err != nil {
		closeTestDB(t, db)
		t.Fatal(err)
	}
	if err := db.AddSessionTurn(context.Background(), &storage.SessionTurn{
		SessionID: session.ID, Question: "Ready?", Answer: "Ready [1].",
		Citations: []storage.SessionCitation{citationForDocument(doc)},
	}); err != nil {
		closeTestDB(t, db)
		t.Fatal(err)
	}
	closeTestDB(t, db)

	if err := runSession([]string{"pin", "release", doc.ID}); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(tmpDir, "brief.md")
	if err := runSession([]string{"export", "release", "--format", "markdown", "--output", outputPath}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Ready [1].") || !strings.Contains(string(content), "pinned") {
		t.Fatalf("exported session brief:\n%s", content)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session brief mode = %v", info.Mode().Perm())
	}
	if err := runSession([]string{"delete", "release"}); err != nil {
		t.Fatal(err)
	}
}
