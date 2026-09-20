package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// ReplaceNoteFTS keeps the standalone notes_fts index in step with a note's
// title and canonical Markdown body. It is delete-then-insert rather than an
// SQL trigger so an aborted transaction can never leave the index and the
// canonical notes/crdt_states rows inconsistent (see the migration comment
// on notes_fts).
func ReplaceNoteFTS(ctx context.Context, exec Executor, noteID model.ID, title, body string) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM notes_fts WHERE note_id = ?`, noteID.Bytes()); err != nil {
		return fmt.Errorf("store: delete note FTS row: %w", err)
	}
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO notes_fts (note_id, title, body) VALUES (?, ?, ?)`,
		noteID.Bytes(), title, body,
	); err != nil {
		return fmt.Errorf("store: insert note FTS row: %w", err)
	}
	return nil
}

// MissingNoteFTSRows returns the IDs of every live (non-deleted) note in
// workspaceID that has no notes_fts row. Every code path that creates or
// edits a note's body pairs its notes_fts write with the same transaction
// (ReplaceNoteFTS above, called from CreateNote and CommitNoteBody), so this
// is expected to return nothing; it exists as a safety net the Advanced
// "Check my data" action uses to detect and repair drift left by an
// interrupted maintenance run rather than a routine repair path.
func MissingNoteFTSRows(ctx context.Context, exec Executor, workspaceID model.ID) ([]model.ID, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT n.id FROM notes n
		WHERE n.workspace_id = ? AND n.deleted = 0
		AND NOT EXISTS (SELECT 1 FROM notes_fts f WHERE f.note_id = n.id)`,
		workspaceID.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: find notes missing a search index row: %w", err)
	}
	return scanNoteIDRows(rows)
}

// OrphanedNoteFTSRows returns the IDs referenced by every notes_fts row
// with no matching notes row at all. This is distinct from a soft-deleted
// note, which deliberately keeps its notes_fts row until garbage collection
// (see SearchNotes' n.deleted filter); DeleteNoteCompletely removes both
// rows together in the same transaction, so this is expected to return
// nothing. Like MissingNoteFTSRows, it exists only as a safety net for the
// Advanced "Check my data" action.
func OrphanedNoteFTSRows(ctx context.Context, exec Executor) ([]model.ID, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT f.note_id FROM notes_fts f
		LEFT JOIN notes n ON n.id = f.note_id
		WHERE n.id IS NULL`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: find orphaned search index rows: %w", err)
	}
	return scanNoteIDRows(rows)
}

// DeleteNoteFTSRow removes noteID's notes_fts row, if any. It is the
// orphaned-row half of repairing what OrphanedNoteFTSRows finds; the
// missing-row half (MissingNoteFTSRows) must re-derive title and body from
// the note's current CRDT document, which this package has no access to, so
// callers use ReplaceNoteFTS for that side instead.
func DeleteNoteFTSRow(ctx context.Context, exec Executor, noteID model.ID) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM notes_fts WHERE note_id = ?`, noteID.Bytes()); err != nil {
		return fmt.Errorf("store: delete orphaned note FTS row: %w", err)
	}
	return nil
}

func scanNoteIDRows(rows *sql.Rows) ([]model.ID, error) {
	defer rows.Close()
	var ids []model.ID
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("store: scan note id: %w", err)
		}
		id, err := model.ParseID(raw)
		if err != nil {
			return nil, fmt.Errorf("store: parse note id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: scan note id rows: %w", err)
	}
	return ids, nil
}
