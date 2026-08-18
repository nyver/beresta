package store

import (
	"context"
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
