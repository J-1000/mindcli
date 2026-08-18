package storage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CountCollectionDocumentsAddedAfter counts manual memberships created after
// the supplied view boundary. A zero boundary counts every current member.
func (d *DB) CountCollectionDocumentsAddedAfter(ctx context.Context, collectionID string, after time.Time) (int, error) {
	query := `SELECT COUNT(*) FROM collection_documents WHERE collection_id = ?`
	args := []any{strings.TrimSpace(collectionID)}
	if !after.IsZero() {
		query += ` AND added_at > ?`
		args = append(args, after.UTC())
	}
	var count int
	if err := d.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting new collection documents: %w", err)
	}
	return count, nil
}

// ListCollectionDocumentIDsAddedAfter returns manual memberships created in a
// time range, newest first. A zero boundary returns every current member.
func (d *DB) ListCollectionDocumentIDsAddedAfter(ctx context.Context, collectionID string, after time.Time) ([]string, error) {
	query := `SELECT document_id FROM collection_documents WHERE collection_id = ?`
	args := []any{strings.TrimSpace(collectionID)}
	if !after.IsZero() {
		query += ` AND added_at >= ?`
		args = append(args, after.UTC())
	}
	query += ` ORDER BY added_at DESC`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing new collection documents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning new collection document: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating new collection documents: %w", err)
	}
	return ids, nil
}

// FilterUnseenCollectionDocumentIDs returns current smart-query members that
// have not been recorded by a previous collection view.
func (d *DB) FilterUnseenCollectionDocumentIDs(ctx context.Context, collectionID string, currentIDs []string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT document_id FROM collection_seen_documents WHERE collection_id = ?`, strings.TrimSpace(collectionID),
	)
	if err != nil {
		return nil, fmt.Errorf("listing seen collection documents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	seen := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning seen collection document: %w", err)
		}
		seen[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	unseen := make([]string, 0, len(currentIDs))
	deduplicated := make(map[string]bool)
	for _, id := range currentIDs {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] && !deduplicated[id] {
			deduplicated[id] = true
			unseen = append(unseen, id)
		}
	}
	return unseen, nil
}

// MarkCollectionViewed atomically advances the view boundary and snapshots the
// current smart-query member IDs. Manual collections use their added_at values
// for unseen tracking but may safely pass their current IDs too.
func (d *DB) MarkCollectionViewed(ctx context.Context, collectionID string, currentIDs []string, viewedAt time.Time) error {
	collectionID = strings.TrimSpace(collectionID)
	if viewedAt.IsZero() {
		viewedAt = time.Now().UTC()
	} else {
		viewedAt = viewedAt.UTC()
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting collection view update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE collections SET last_viewed_at = ? WHERE id = ?`, viewedAt, collectionID)
	if err != nil {
		return fmt.Errorf("updating collection view: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking collection view update: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	seen := make(map[string]bool)
	for _, id := range currentIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO collection_seen_documents (collection_id, document_id, first_seen_at)
			VALUES (?, ?, ?)`, collectionID, id, viewedAt); err != nil {
			return fmt.Errorf("recording seen collection document: %w", err)
		}
	}
	return tx.Commit()
}
