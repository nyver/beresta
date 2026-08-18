package store

import (
	"context"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// Revision kinds for the revisions.kind column.
const (
	RevisionKindCheckpoint = 1
	RevisionKindDelta      = 2
)

// InsertRevision appends one encrypted revision record (see
// corecrypto.EncryptAndPackObject) for a note.
func InsertRevision(ctx context.Context, exec Executor, revisionID, noteID model.ID, kind int, data []byte, createdUnixMS uint64) error {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO revisions (id, note_id, kind, data, created_unix_ms) VALUES (?, ?, ?, ?, ?)`,
		revisionID.Bytes(), noteID.Bytes(), kind, data, createdUnixMS,
	); err != nil {
		return fmt.Errorf("store: insert revision: %w", err)
	}
	return nil
}
