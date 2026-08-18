package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/J-1000/mindcli/internal/privacy"
	"github.com/J-1000/mindcli/internal/storage"
)

const maxSessionNameRunes = 80

func runSession(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mindcli session <create|resume|list|show|delete|add|pin|exclude|remove|export> [args...]")
	}
	if args[0] == "resume" {
		if len(args) != 2 {
			return fmt.Errorf("usage: mindcli session resume <name>")
		}
		return runTUIWithSession(args[1])
	}

	s, err := openStores(openOpts{})
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()

	switch args[0] {
	case "create":
		if len(args) != 2 {
			return fmt.Errorf("usage: mindcli session create <name>")
		}
		name, err := validateSessionName(args[1])
		if err != nil {
			return err
		}
		session := &storage.ResearchSession{Name: name}
		if err := s.db.CreateSession(ctx, session); err != nil {
			if errors.Is(err, storage.ErrSessionExists) {
				return fmt.Errorf("session %q already exists", name)
			}
			return err
		}
		fmt.Printf("Created session %q\n", session.Name)
		return nil

	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: mindcli session list")
		}
		sessions, err := s.db.ListSessions(ctx)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			fmt.Println("No research sessions found.")
			return nil
		}
		for _, session := range sessions {
			turns, err := s.db.ListSessionTurns(ctx, session.ID)
			if err != nil {
				return err
			}
			fmt.Printf("%s\t%d turns\tupdated %s\n", session.Name, len(turns), session.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		return nil

	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: mindcli session show <name>")
		}
		return printSession(ctx, s.db, args[1], buildRedactor(s.cfg))

	case "delete":
		if len(args) != 2 {
			return fmt.Errorf("usage: mindcli session delete <name>")
		}
		session, err := sessionByName(ctx, s.db, args[1])
		if err != nil {
			return err
		}
		if err := s.db.DeleteSession(ctx, session.ID); err != nil {
			return err
		}
		fmt.Printf("Deleted session %q\n", session.Name)
		return nil

	case "add", "pin", "exclude":
		if len(args) != 3 {
			return fmt.Errorf("usage: mindcli session %s <name> <document-path-or-id>", args[0])
		}
		state := storage.SessionDocumentIncluded
		if args[0] == "pin" {
			state = storage.SessionDocumentPinned
		} else if args[0] == "exclude" {
			state = storage.SessionDocumentExcluded
		}
		return setSessionDocument(ctx, s.db, args[1], args[2], state)

	case "remove":
		if len(args) != 3 {
			return fmt.Errorf("usage: mindcli session remove <name> <document-path-or-id>")
		}
		session, err := sessionByName(ctx, s.db, args[1])
		if err != nil {
			return err
		}
		doc, err := resolveSessionDocument(ctx, s.db, args[2])
		if err != nil {
			return err
		}
		if err := s.db.RemoveSessionDocument(ctx, session.ID, doc.ID); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return fmt.Errorf("document %q is not in session %q", doc.Title, session.Name)
			}
			return err
		}
		fmt.Printf("Removed %q from session %q context\n", doc.Title, session.Name)
		return nil

	case "export":
		return runSessionExport(ctx, s, args[1:])

	default:
		return fmt.Errorf("unknown session subcommand %q: use create, resume, list, show, delete, add, pin, exclude, remove, or export", args[0])
	}
}

func validateSessionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("session name must not be empty")
	}
	if utf8.RuneCountInString(name) > maxSessionNameRunes {
		return "", fmt.Errorf("session name exceeds %d characters", maxSessionNameRunes)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("session name must not contain control characters")
		}
	}
	return name, nil
}

func sessionByName(ctx context.Context, db *storage.DB, name string) (*storage.ResearchSession, error) {
	session, err := db.GetSessionByName(ctx, strings.TrimSpace(name))
	if errors.Is(err, storage.ErrNotFound) {
		return nil, fmt.Errorf("session %q not found", name)
	}
	return session, err
}

func resolveSessionDocument(ctx context.Context, db *storage.DB, reference string) (*storage.Document, error) {
	reference = strings.TrimSpace(reference)
	if doc, err := db.GetDocument(ctx, reference); err == nil {
		return doc, nil
	}
	if doc, err := db.GetDocumentByPath(ctx, reference); err == nil {
		return doc, nil
	}
	if !strings.Contains(reference, "://") {
		if absolute, err := filepath.Abs(reference); err == nil {
			if doc, err := db.GetDocumentByPath(ctx, absolute); err == nil {
				return doc, nil
			}
		}
	}
	return nil, fmt.Errorf("document %q not found in the index", reference)
}

func setSessionDocument(ctx context.Context, db *storage.DB, sessionName, reference string, state storage.SessionDocumentState) error {
	session, err := sessionByName(ctx, db, sessionName)
	if err != nil {
		return err
	}
	doc, err := resolveSessionDocument(ctx, db, reference)
	if err != nil {
		return err
	}
	if err := db.SetSessionDocumentState(ctx, session.ID, doc.ID, state); err != nil {
		return err
	}
	fmt.Printf("Set %q to %s in session %q\n", doc.Title, state, session.Name)
	return nil
}

func printSession(ctx context.Context, db *storage.DB, name string, redactor privacy.Redactor) error {
	session, err := sessionByName(ctx, db, name)
	if err != nil {
		return err
	}
	turns, err := db.ListSessionTurns(ctx, session.ID)
	if err != nil {
		return err
	}
	documents, err := db.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		return err
	}
	fmt.Printf("Session: %s\n", redactor.Redact(session.Name))
	fmt.Printf("Updated: %s\n", session.UpdatedAt.Local().Format("2006-01-02 15:04"))
	fmt.Printf("Turns: %d\n", len(turns))
	fmt.Printf("Context documents: %d\n", len(documents))
	for _, item := range documents {
		fmt.Printf("  [%s] %s (%s)\n", item.State, redactor.Redact(item.Document.Title), redactor.Redact(item.Document.Path))
	}
	if len(turns) > 0 {
		fmt.Println("Conversation:")
		for index, turn := range turns {
			fmt.Printf("  %d. Q: %s\n", index+1, redactor.Redact(truncateForDisplay(turn.Question, 120)))
			fmt.Printf("     Generated: %s\n", redactor.Redact(truncateForDisplay(turn.Answer, 160)))
		}
	}
	return nil
}

func runSessionExport(ctx context.Context, s *stores, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mindcli session export <name> [--format markdown] [--output file]")
	}
	name := args[0]
	fs := flag.NewFlagSet("session-export", flag.ContinueOnError)
	format := fs.String("format", "markdown", "Output format (markdown)")
	output := fs.String("output", "", "Output file (default: stdout)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("usage: mindcli session export <name> [--format markdown] [--output file]")
	}
	if *format != "markdown" {
		return fmt.Errorf("unsupported session format %q: use markdown", *format)
	}
	session, err := sessionByName(ctx, s.db, name)
	if err != nil {
		return err
	}
	turns, err := s.db.ListSessionTurns(ctx, session.ID)
	if err != nil {
		return err
	}
	documents, err := s.db.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		return err
	}

	writer, closeWriter, err := privateOutputWriter(*output)
	if err != nil {
		return err
	}
	if err := writeSessionBrief(writer, session, turns, documents, buildRedactor(s.cfg)); err != nil {
		if closeWriter != nil {
			_ = closeWriter()
		}
		return fmt.Errorf("writing session brief: %w", err)
	}
	if closeWriter != nil {
		if err := closeWriter(); err != nil {
			return fmt.Errorf("closing session brief: %w", err)
		}
	}
	return nil
}

func privateOutputWriter(path string) (io.Writer, func() error, error) {
	if strings.TrimSpace(path) == "" {
		return os.Stdout, nil, nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("creating output file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("protecting output file: %w", err)
	}
	return file, file.Close, nil
}

func writeSessionBrief(w io.Writer, session *storage.ResearchSession, turns []*storage.SessionTurn, contextDocuments []*storage.SessionDocument, redactor privacy.Redactor) error {
	redact := redactor.Redact
	if _, err := fmt.Fprintf(w, "# Research brief: %s\n\n", redact(session.Name)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "- **Created:** %s\n- **Updated:** %s\n- **Conversation turns:** %d\n\n", session.CreatedAt.Format("2006-01-02 15:04 MST"), session.UpdatedAt.Format("2006-01-02 15:04 MST"), len(turns)); err != nil {
		return err
	}
	if len(turns) > 0 {
		if _, err := fmt.Fprint(w, "## Final synthesis\n\n> The following is generated content from the final recorded answer.\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, redact(turns[len(turns)-1].Answer)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "\n## Conversation"); err != nil {
		return err
	}
	for index, turn := range turns {
		if _, err := fmt.Fprintf(w, "\n### Turn %d\n\n**Question**\n\n%s\n\n**Generated answer**\n\n%s\n", index+1, redact(turn.Question), redact(turn.Answer)); err != nil {
			return err
		}
		if len(turn.Citations) > 0 {
			if _, err := fmt.Fprint(w, "\n**Citations**\n\n"); err != nil {
				return err
			}
			for citationIndex, citation := range turn.Citations {
				if _, err := fmt.Fprintf(w, "%d. %s — `%s` (%s; ID `%s`)\n", citationIndex+1, redact(citation.Title), redact(citation.Path), citation.Source, citation.DocumentID); err != nil {
					return err
				}
			}
		}
	}

	type sourceItem struct {
		citation storage.SessionCitation
		state    storage.SessionDocumentState
	}
	sources := make(map[string]sourceItem)
	for _, document := range contextDocuments {
		sources[document.Document.ID] = sourceItem{citation: citationForDocument(document.Document), state: document.State}
	}
	for _, turn := range turns {
		for _, citation := range turn.Citations {
			if _, exists := sources[citation.DocumentID]; !exists {
				sources[citation.DocumentID] = sourceItem{citation: citation}
			}
		}
	}
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return strings.ToLower(sources[ids[i]].citation.Title) < strings.ToLower(sources[ids[j]].citation.Title)
	})
	if _, err := fmt.Fprintln(w, "\n## Source list"); err != nil {
		return err
	}
	if len(ids) == 0 {
		_, err := fmt.Fprintln(w, "\nNo sources were recorded.")
		return err
	}
	for _, id := range ids {
		item := sources[id]
		state := "cited"
		if item.state != "" {
			state = string(item.state)
		}
		if _, err := fmt.Fprintf(w, "\n- **%s** — `%s` (%s; %s; ID `%s`)", redact(item.citation.Title), redact(item.citation.Path), item.citation.Source, state, item.citation.DocumentID); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func citationForDocument(doc *storage.Document) storage.SessionCitation {
	return storage.SessionCitation{
		DocumentID: doc.ID,
		Title:      doc.Title,
		Path:       doc.Path,
		Source:     doc.Source,
	}
}
