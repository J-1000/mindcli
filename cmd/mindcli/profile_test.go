package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/J-1000/mindcli/internal/config"
	"github.com/J-1000/mindcli/internal/storage"
)

func TestParseInvocationProfilePrecedenceAndValidation(t *testing.T) {
	t.Setenv("MINDCLI_PROFILE", "personal")
	profile, args, err := parseInvocation([]string{"search", "query"})
	if err != nil || profile != "personal" || strings.Join(args, " ") != "search query" {
		t.Fatalf("environment invocation = %q, %#v, %v", profile, args, err)
	}
	profile, args, err = parseInvocation([]string{"--profile", "work", "search", "query"})
	if err != nil || profile != "work" || strings.Join(args, " ") != "search query" {
		t.Fatalf("flag invocation = %q, %#v, %v", profile, args, err)
	}
	profile, args, err = parseInvocation([]string{"--profile=client-a", "doctor"})
	if err != nil || profile != "client-a" || len(args) != 1 || args[0] != "doctor" {
		t.Fatalf("equals invocation = %q, %#v, %v", profile, args, err)
	}
	t.Setenv("MINDCLI_PROFILE", "../broken")
	if profile, _, err = parseInvocation([]string{"--profile", "work", "stats"}); err != nil || profile != "work" {
		t.Fatalf("explicit profile did not override invalid env = %q, %v", profile, err)
	}
	if _, _, err := parseInvocation([]string{"stats"}); err == nil {
		t.Fatal("invalid environment profile succeeded")
	}
	if _, _, err := parseInvocation([]string{"--profile", "../escape", "stats"}); err == nil {
		t.Fatal("traversal profile succeeded")
	}
}

func TestRunProfileCreatesAndSafelyListsConfigs(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("MINDCLI_CONFIG_DIR", configDir)
	t.Setenv("MINDCLI_CONFIG_PATH", "")
	original := activeProfile
	activeProfile = "work"
	t.Cleanup(func() { activeProfile = original })

	output := captureProfileStdout(t, func() error { return runProfile([]string{"create", "work"}) })
	if !strings.Contains(output, `Created profile "work"`) {
		t.Fatalf("profile create output = %q", output)
	}
	path, err := config.ConfigPathForProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("profile config was not created: %v", err)
	}
	output = captureProfileStdout(t, func() error { return runProfile([]string{"list"}) })
	if !strings.Contains(output, "* work") || !strings.Contains(output, "  default") {
		t.Fatalf("profile list output = %q", output)
	}
	if err := runProfile([]string{"create", "work"}); err == nil {
		t.Fatal("duplicate profile creation succeeded")
	}
}

func TestOpenStoresNeverCrossesProfiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("MINDCLI_CONFIG_DIR", filepath.Join(tmpDir, "config"))
	t.Setenv("MINDCLI_CONFIG_PATH", "")
	t.Setenv("MINDCLI_STORAGE_PATH", "")
	t.Setenv("MINDCLI_CAPTURE_INBOX", "")
	original := activeProfile
	t.Cleanup(func() { activeProfile = original })

	for _, profile := range []string{"work", "personal"} {
		cfg, err := config.DefaultForProfile(profile)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Storage.Path = filepath.Join(tmpDir, profile, "data")
		cfg.Capture.Inbox = filepath.Join(tmpDir, profile, "inbox")
		cfg.Privacy.RedactPatterns = []string{profile + "-secret"}
		if err := cfg.SaveProfile(profile); err != nil {
			t.Fatal(err)
		}
	}

	activeProfile = "work"
	work, err := openStores(openOpts{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	doc := &storage.Document{ID: "work-only", Source: storage.SourceMarkdown, Path: "/work.md", Title: "Work", ContentHash: "hash", IndexedAt: now, ModifiedAt: now}
	if err := work.db.InsertDocument(context.Background(), doc); err != nil {
		work.Close()
		t.Fatal(err)
	}
	if err := work.db.CreateSession(context.Background(), &storage.ResearchSession{Name: "work-session"}); err != nil {
		work.Close()
		t.Fatal(err)
	}
	workDir := work.dataDir
	work.Close()

	activeProfile = "personal"
	personal, err := openStores(openOpts{})
	if err != nil {
		t.Fatal(err)
	}
	defer personal.Close()
	count, err := personal.db.CountDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := personal.db.ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(sessions) != 0 || personal.dataDir == workDir {
		t.Fatalf("profile isolation: count=%d sessions=%d personal=%q work=%q", count, len(sessions), personal.dataDir, workDir)
	}
	if personal.cfg.Capture.Inbox == filepath.Join(tmpDir, "work", "inbox") || personal.cfg.Privacy.RedactPatterns[0] != "personal-secret" {
		t.Fatalf("profile config crossed boundary: %+v", personal.cfg)
	}
}

func captureProfileStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })
	callErr := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	content, readErr := io.ReadAll(r)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if callErr != nil || readErr != nil {
		t.Fatalf("captured call error = %v, read = %v", callErr, readErr)
	}
	return string(content)
}
