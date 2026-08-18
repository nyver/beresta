package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// Attachment is one attachment's metadata row: the encrypted manifest plus
// the bookkeeping needed to publish and eventually garbage-collect its
// content-addressed blob file. The blob's ciphertext bytes themselves live
// in a BlobStore, not this row.
type Attachment struct {
	BlobID        BlobID
	WorkspaceID   model.ID
	KeyID         []byte
	Manifest      []byte
	SizeBytes     uint64
	ChunkCount    uint32
	CreatedUnixMS int64
	// OrphanedUnixMS is nil while at least one note references BlobID, and
	// set to the moment the last reference was removed otherwise. A future
	// garbage-collection sweep (task 4.9) deletes an orphaned attachment's
	// blob file and row only once this has been non-nil for at least the
	// documented minimum retention window.
	OrphanedUnixMS *int64
}

// CreateAttachment durably records one attachment's metadata row. It is
// dedup-safe: attachment identity is content-addressed by BlobID (see
// core/crypto.ComputeBlobID), so if the same plaintext was ever attached
// before anywhere in the workspace's history, the existing row is returned
// unchanged instead of erroring — matching BlobStore.Publish's own
// dedup-on-publish behavior for the blob file itself. Callers publish the
// blob file before calling CreateAttachment in the same local mutation, and
// commit the transaction only after both succeed.
func CreateAttachment(ctx context.Context, exec Executor, workspaceID model.ID, blobID BlobID, keyID, manifest []byte, sizeBytes uint64, chunkCount uint32, nowUnixMS int64) (Attachment, error) {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO attachments (blob_id, workspace_id, key_id, manifest, size_bytes, chunk_count, created_unix_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(blob_id) DO NOTHING`,
		blobID.Bytes(), workspaceID.Bytes(), keyID, manifest, int64(sizeBytes), chunkCount, nowUnixMS,
	); err != nil {
		return Attachment{}, fmt.Errorf("store: insert attachment: %w", err)
	}
	return GetAttachment(ctx, exec, blobID)
}

// GetAttachment returns one attachment's metadata row.
func GetAttachment(ctx context.Context, exec Executor, blobID BlobID) (Attachment, error) {
	row := exec.QueryRowContext(ctx,
		`SELECT blob_id, workspace_id, key_id, manifest, size_bytes, chunk_count, created_unix_ms, orphaned_unix_ms
		 FROM attachments WHERE blob_id = ?`,
		blobID.Bytes(),
	)
	return scanAttachment(row)
}

// ListOrphanedAttachments returns every attachment in a workspace that lost
// its last note reference at or before orphanedAtOrBeforeUnixMS, i.e. every
// attachment eligible for garbage collection once a caller-enforced
// retention window has elapsed. This function only reports candidates; it
// never deletes anything.
func ListOrphanedAttachments(ctx context.Context, exec Executor, workspaceID model.ID, orphanedAtOrBeforeUnixMS int64) ([]Attachment, error) {
	rows, err := exec.QueryContext(ctx,
		`SELECT blob_id, workspace_id, key_id, manifest, size_bytes, chunk_count, created_unix_ms, orphaned_unix_ms
		 FROM attachments
		 WHERE workspace_id = ? AND orphaned_unix_ms IS NOT NULL AND orphaned_unix_ms <= ?`,
		workspaceID.Bytes(), orphanedAtOrBeforeUnixMS,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list orphaned attachments: %w", err)
	}
	defer rows.Close()

	var attachments []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list orphaned attachments: %w", err)
	}
	return attachments, nil
}

// DeleteAttachment permanently removes an attachment's catalog row and
// every note_attachments row that references it (present=1 or the
// present=0 tombstones SetNoteAttachment leaves behind when a note removes
// its reference — orphaning requires none present=1, but old present=0
// rows remain and would otherwise violate note_attachments' foreign key
// into attachments). The caller is responsible for removing the published
// blob file separately (see store.BlobStore.Path) and must only call this
// once its orphan grace period has elapsed (see ListOrphanedAttachments).
func DeleteAttachment(ctx context.Context, exec Executor, blobID BlobID) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM note_attachments WHERE blob_id = ?`, blobID.Bytes()); err != nil {
		return fmt.Errorf("store: delete attachment references: %w", err)
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM attachments WHERE blob_id = ?`, blobID.Bytes()); err != nil {
		return fmt.Errorf("store: delete attachment: %w", err)
	}
	return nil
}

func scanAttachment(scanner rowScanner) (Attachment, error) {
	var a Attachment
	var blobIDBytes, workspaceIDBytes []byte
	var sizeBytes int64
	var orphaned sql.NullInt64
	if err := scanner.Scan(&blobIDBytes, &workspaceIDBytes, &a.KeyID, &a.Manifest, &sizeBytes, &a.ChunkCount, &a.CreatedUnixMS, &orphaned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Attachment{}, ErrNotFound
		}
		return Attachment{}, fmt.Errorf("store: scan attachment: %w", err)
	}
	blobID, err := ParseBlobID(blobIDBytes)
	if err != nil {
		return Attachment{}, fmt.Errorf("store: stored attachment blob ID: %w", err)
	}
	workspaceID, err := model.ParseID(workspaceIDBytes)
	if err != nil {
		return Attachment{}, fmt.Errorf("store: stored attachment workspace ID: %w", err)
	}
	a.BlobID, a.WorkspaceID, a.SizeBytes = blobID, workspaceID, uint64(sizeBytes)
	if orphaned.Valid {
		v := orphaned.Int64
		a.OrphanedUnixMS = &v
	}
	return a, nil
}

// SetNoteAttachment adds or removes a note's reference to an attachment as
// an independent per-pair LWW register, exactly like SetNoteTag, and then
// reconciles the attachment's orphan mark in the same transaction: cleared
// if any note still references it, set to nowUnixMS the moment none does.
// Reconciling here rather than in a periodic sweep means the grace period
// in ListOrphanedAttachments always starts from the operation that actually
// removed the last reference, not from whenever a sweep next happens to
// run.
func SetNoteAttachment(ctx context.Context, exec Executor, noteID model.ID, blobID BlobID, present bool, clock model.HLC, nowUnixMS int64) error {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO note_attachments (note_id, blob_id, present, physical_ms, logical, device_id) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(note_id, blob_id) DO UPDATE SET present = excluded.present, physical_ms = excluded.physical_ms, logical = excluded.logical, device_id = excluded.device_id
		 WHERE excluded.physical_ms > note_attachments.physical_ms
		    OR (excluded.physical_ms = note_attachments.physical_ms AND excluded.logical > note_attachments.logical)
		    OR (excluded.physical_ms = note_attachments.physical_ms AND excluded.logical = note_attachments.logical AND excluded.device_id > note_attachments.device_id)`,
		noteID.Bytes(), blobID.Bytes(), present, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		return fmt.Errorf("store: set note attachment: %w", err)
	}
	return reconcileAttachmentOrphan(ctx, exec, blobID, nowUnixMS)
}

// NoteAttachmentBlobIDs returns the IDs of every attachment currently
// present on a note.
func NoteAttachmentBlobIDs(ctx context.Context, exec Executor, noteID model.ID) ([]BlobID, error) {
	rows, err := exec.QueryContext(ctx, `SELECT blob_id FROM note_attachments WHERE note_id = ? AND present = 1`, noteID.Bytes())
	if err != nil {
		return nil, fmt.Errorf("store: list note attachments: %w", err)
	}
	defer rows.Close()

	var blobIDs []BlobID
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("store: scan note attachment: %w", err)
		}
		id, err := ParseBlobID(raw)
		if err != nil {
			return nil, fmt.Errorf("store: stored note attachment blob ID: %w", err)
		}
		blobIDs = append(blobIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list note attachments: %w", err)
	}
	return blobIDs, nil
}

func reconcileAttachmentOrphan(ctx context.Context, exec Executor, blobID BlobID, nowUnixMS int64) error {
	var referenced int
	if err := exec.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM note_attachments WHERE blob_id = ? AND present = 1)`,
		blobID.Bytes(),
	).Scan(&referenced); err != nil {
		return fmt.Errorf("store: check attachment references: %w", err)
	}
	if referenced == 1 {
		if _, err := exec.ExecContext(ctx,
			`UPDATE attachments SET orphaned_unix_ms = NULL WHERE blob_id = ? AND orphaned_unix_ms IS NOT NULL`,
			blobID.Bytes(),
		); err != nil {
			return fmt.Errorf("store: clear attachment orphan mark: %w", err)
		}
		return nil
	}
	if _, err := exec.ExecContext(ctx,
		`UPDATE attachments SET orphaned_unix_ms = ? WHERE blob_id = ? AND orphaned_unix_ms IS NULL`,
		nowUnixMS, blobID.Bytes(),
	); err != nil {
		return fmt.Errorf("store: mark attachment orphaned: %w", err)
	}
	return nil
}
