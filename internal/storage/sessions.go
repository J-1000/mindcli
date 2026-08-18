package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateSession creates a named research session.
func (d *DB) CreateSession(ctx context.Context, session *ResearchSession) error {
	if session == nil {
		return errors.New("research session must not be nil")
	}
	session.Name = strings.TrimSpace(session.Name)
	if session.Name == "" {
		return errors.New("research session name must not be empty")
	}
	if session.ID == "" {
		session.ID = generateID()
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	if session.UpdatedAt.IsZero() {
		session.UpdatedAt = session.CreatedAt
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO research_sessions (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		session.ID, session.Name, session.CreatedAt.UTC(), session.UpdatedAt.UTC(),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrSessionExists
		}
		return fmt.Errorf("creating research session: %w", err)
	}
	return nil
}

func scanResearchSession(row interface{ Scan(...any) error }) (*ResearchSession, error) {
	var session ResearchSession
	if err := row.Scan(&session.ID, &session.Name, &session.CreatedAt, &session.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning research session: %w", err)
	}
	return &session, nil
}

// GetSession retrieves a research session by stable ID.
func (d *DB) GetSession(ctx context.Context, id string) (*ResearchSession, error) {
	return scanResearchSession(d.db.QueryRowContext(ctx,
		`SELECT id, name, created_at, updated_at FROM research_sessions WHERE id = ?`, strings.TrimSpace(id),
	))
}

// GetSessionByName retrieves a research session by case-insensitive name.
func (d *DB) GetSessionByName(ctx context.Context, name string) (*ResearchSession, error) {
	return scanResearchSession(d.db.QueryRowContext(ctx,
		`SELECT id, name, created_at, updated_at FROM research_sessions WHERE name = ? COLLATE NOCASE`, strings.TrimSpace(name),
	))
}

// ListSessions returns sessions ordered from most recently active.
func (d *DB) ListSessions(ctx context.Context) ([]*ResearchSession, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, name, created_at, updated_at FROM research_sessions ORDER BY updated_at DESC, name COLLATE NOCASE`,
	)
	if err != nil {
		return nil, fmt.Errorf("listing research sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var sessions []*ResearchSession
	for rows.Next() {
		session, err := scanResearchSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// DeleteSession deletes a session, its turns, and its document context.
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	result, err := d.db.ExecContext(ctx, `DELETE FROM research_sessions WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("deleting research session: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking deleted research session: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

// AddSessionTurn persists a completed generated answer and its source snapshot.
func (d *DB) AddSessionTurn(ctx context.Context, turn *SessionTurn) error {
	if turn == nil {
		return errors.New("session turn must not be nil")
	}
	turn.SessionID = strings.TrimSpace(turn.SessionID)
	turn.Question = strings.TrimSpace(turn.Question)
	turn.Answer = strings.TrimSpace(turn.Answer)
	if turn.SessionID == "" || turn.Question == "" || turn.Answer == "" {
		return errors.New("session turn requires session, question, and answer")
	}
	if turn.ID == "" {
		turn.ID = generateID()
	}
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	citations, err := json.Marshal(turn.Citations)
	if err != nil {
		return fmt.Errorf("encoding session citations: %w", err)
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting session turn: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO session_turns (id, session_id, question, answer, citations, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		turn.ID, turn.SessionID, turn.Question, turn.Answer, string(citations), turn.CreatedAt.UTC(),
	); err != nil {
		return fmt.Errorf("adding session turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE research_sessions SET updated_at = ? WHERE id = ?`, turn.CreatedAt.UTC(), turn.SessionID); err != nil {
		return fmt.Errorf("updating research session activity: %w", err)
	}
	return tx.Commit()
}

// ListSessionTurns returns every persisted turn in conversation order.
func (d *DB) ListSessionTurns(ctx context.Context, sessionID string) ([]*SessionTurn, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT id, session_id, question, answer, citations, created_at
		FROM session_turns WHERE session_id = ? ORDER BY created_at, rowid`, strings.TrimSpace(sessionID),
	)
	if err != nil {
		return nil, fmt.Errorf("listing session turns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var turns []*SessionTurn
	for rows.Next() {
		var turn SessionTurn
		var citations string
		if err := rows.Scan(&turn.ID, &turn.SessionID, &turn.Question, &turn.Answer, &citations, &turn.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning session turn: %w", err)
		}
		if err := json.Unmarshal([]byte(citations), &turn.Citations); err != nil {
			return nil, fmt.Errorf("decoding session citations: %w", err)
		}
		turns = append(turns, &turn)
	}
	return turns, rows.Err()
}

func validSessionDocumentState(state SessionDocumentState) bool {
	switch state {
	case SessionDocumentIncluded, SessionDocumentPinned, SessionDocumentExcluded:
		return true
	default:
		return false
	}
}

// SetSessionDocumentState adds or updates a document in the session context.
func (d *DB) SetSessionDocumentState(ctx context.Context, sessionID, documentID string, state SessionDocumentState) error {
	if !validSessionDocumentState(state) {
		return fmt.Errorf("invalid session document state %q", state)
	}
	now := time.Now().UTC()
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting session document update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_documents (session_id, document_id, state, added_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id, document_id) DO UPDATE SET state = excluded.state`,
		strings.TrimSpace(sessionID), strings.TrimSpace(documentID), state, now,
	); err != nil {
		return fmt.Errorf("setting session document state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE research_sessions SET updated_at = ? WHERE id = ?`, now, strings.TrimSpace(sessionID)); err != nil {
		return fmt.Errorf("updating research session activity: %w", err)
	}
	return tx.Commit()
}

// RemoveSessionDocument removes a document-specific context rule.
func (d *DB) RemoveSessionDocument(ctx context.Context, sessionID, documentID string) error {
	result, err := d.db.ExecContext(ctx,
		`DELETE FROM session_documents WHERE session_id = ? AND document_id = ?`,
		strings.TrimSpace(sessionID), strings.TrimSpace(documentID),
	)
	if err != nil {
		return fmt.Errorf("removing session document: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking removed session document: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

// ListSessionDocuments returns live session documents, pinned first and
// excluded last, with stable added-time ordering within each state.
func (d *DB) ListSessionDocuments(ctx context.Context, sessionID string) ([]*SessionDocument, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT d.id, d.source, d.path, d.title, d.content, d.preview, d.metadata,
		       d.content_hash, d.indexed_at, d.modified_at, sd.state, sd.added_at
		FROM session_documents sd
		INNER JOIN documents d ON d.id = sd.document_id
		WHERE sd.session_id = ?
		ORDER BY CASE sd.state WHEN 'pinned' THEN 0 WHEN 'included' THEN 1 ELSE 2 END,
		         sd.added_at, d.title COLLATE NOCASE`, strings.TrimSpace(sessionID),
	)
	if err != nil {
		return nil, fmt.Errorf("listing session documents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var documents []*SessionDocument
	for rows.Next() {
		doc := &Document{}
		var metadata string
		var state string
		var addedAt time.Time
		if err := rows.Scan(
			&doc.ID, &doc.Source, &doc.Path, &doc.Title, &doc.Content, &doc.Preview,
			&metadata, &doc.ContentHash, &doc.IndexedAt, &doc.ModifiedAt, &state, &addedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning session document: %w", err)
		}
		if err := doc.SetMetadataFromJSON(metadata); err != nil {
			return nil, fmt.Errorf("decoding session document metadata: %w", err)
		}
		documents = append(documents, &SessionDocument{
			Document: doc, State: SessionDocumentState(state), AddedAt: addedAt,
		})
	}
	return documents, rows.Err()
}
