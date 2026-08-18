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

// Revision is one encrypted revision record: either a checkpoint (a full
// document snapshot) or a delta (one incremental Yjs update), in the shape
// corecrypto.EncryptAndPackObject produces. Format is the
// core/sync/yjsadapter.Format Data decrypts to, needed because Yjs's
// ApplyUpdate/EncodeStateAsUpdate cannot infer their binary encoding from
// the bytes alone.
type Revision struct {
	ID            model.ID
	NoteID        model.ID
	Kind          int
	Format        uint8
	Data          []byte
	CreatedUnixMS int64
}

// InsertRevision appends one encrypted revision record (see
// corecrypto.EncryptAndPackObject) for a note.
func InsertRevision(ctx context.Context, exec Executor, revisionID, noteID model.ID, kind int, format uint8, data []byte, createdUnixMS uint64) error {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO revisions (id, note_id, kind, format, data, created_unix_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		revisionID.Bytes(), noteID.Bytes(), kind, format, data, createdUnixMS,
	); err != nil {
		return fmt.Errorf("store: insert revision: %w", err)
	}
	return nil
}

// CheckpointDue reports whether a note has accumulated at least interval
// delta revisions since its most recent checkpoint (or since its first
// revision, if it has never had one). It counts by insertion order (SQLite's
// implicit rowid), not by created_unix_ms: a checkpoint is written in the
// same transaction, and so with the same clock.PhysicalMS, as the delta that
// triggered it, and rapid successive edits routinely share a millisecond, so
// a millisecond-resolution timestamp comparison alone cannot reliably order
// or count them.
func CheckpointDue(ctx context.Context, exec Executor, noteID model.ID, interval int) (bool, error) {
	var count int
	if err := exec.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM revisions
		 WHERE note_id = ? AND kind = ?
		   AND rowid > COALESCE(
		       (SELECT MAX(rowid) FROM revisions WHERE note_id = ? AND kind = ?), 0)`,
		noteID.Bytes(), RevisionKindDelta, noteID.Bytes(), RevisionKindCheckpoint,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("store: count revisions since last checkpoint: %w", err)
	}
	return count >= interval, nil
}

// ListRevisions returns every retained revision for a note, in the order it
// was written (SQLite's implicit rowid, not created_unix_ms; see
// CheckpointDue).
func ListRevisions(ctx context.Context, exec Executor, noteID model.ID) ([]Revision, error) {
	rows, err := exec.QueryContext(ctx,
		`SELECT id, kind, format, data, created_unix_ms FROM revisions WHERE note_id = ? ORDER BY rowid ASC`,
		noteID.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: list revisions: %w", err)
	}
	defer rows.Close()

	var revisions []Revision
	for rows.Next() {
		var idBytes []byte
		r := Revision{NoteID: noteID}
		if err := rows.Scan(&idBytes, &r.Kind, &r.Format, &r.Data, &r.CreatedUnixMS); err != nil {
			return nil, fmt.Errorf("store: scan revision: %w", err)
		}
		id, err := model.ParseID(idBytes)
		if err != nil {
			return nil, fmt.Errorf("store: stored revision ID: %w", err)
		}
		r.ID = id
		revisions = append(revisions, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list revisions: %w", err)
	}
	return revisions, nil
}

// PruneRevisions deletes revisions written before the youngest checkpoint at
// or before cutoffUnixMS, for every note, keeping that checkpoint itself and
// everything written after it (see CheckpointDue on why insertion order,
// not created_unix_ms, decides what "before" and "after" mean once a
// checkpoint and a delta can share a millisecond). A note with no checkpoint
// at or before the cutoff is left untouched: without a checkpoint to serve
// as a replay base, none of its revisions can be safely deleted while still
// satisfying the notes-management spec's "at least the preceding seven
// days" retention floor, so pruning conservatively keeps its full history
// instead.
func PruneRevisions(ctx context.Context, exec Executor, cutoffUnixMS int64) (int64, error) {
	result, err := exec.ExecContext(ctx,
		`DELETE FROM revisions
		 WHERE rowid < (
		     SELECT COALESCE(MAX(r2.rowid), -1)
		     FROM revisions r2
		     WHERE r2.note_id = revisions.note_id AND r2.kind = ? AND r2.created_unix_ms <= ?
		 )`,
		RevisionKindCheckpoint, cutoffUnixMS,
	)
	if err != nil {
		return 0, fmt.Errorf("store: prune revisions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune revisions: %w", err)
	}
	return affected, nil
}
