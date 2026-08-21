package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

// SyncRepository persists the complete state-machine boundary in the same
// SQLCipher database as materialized notes.
type SyncRepository struct {
	db        *sql.DB
	transport string
}

func NewSyncRepository(db *sql.DB, transportName string) (*SyncRepository, error) {
	if db == nil || transportName == "" || len(transportName) > 64 {
		return nil, errors.New("store: invalid sync repository configuration")
	}
	return &SyncRepository{db: db, transport: transportName}, nil
}

func (r *SyncRepository) Cursor(ctx context.Context, workspaceID model.ID) (coresync.Cursor, error) {
	result := coresync.Cursor{WorkspaceID: workspaceID, Epoch: 1}
	err := r.db.QueryRowContext(ctx, `
		SELECT last_seq, cursor_epoch FROM sync_cursors
		WHERE workspace_id = ? AND transport = ?`, workspaceID.Bytes(), r.transport,
	).Scan(&result.LastSequence, &result.Epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return coresync.Cursor{}, fmt.Errorf("store: load sync cursor: %w", err)
	}
	return result, nil
}

func (r *SyncRepository) QuarantineBlocked(ctx context.Context, workspaceID model.ID) (bool, error) {
	var exists int
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM inbox WHERE workspace_id = ? AND status = 3)`, workspaceID.Bytes(),
	).Scan(&exists)
	return exists != 0, err
}

func (r *SyncRepository) Quarantine(ctx context.Context, op coresync.WireOperation, reason string, now time.Time) error {
	if reason == "" || len(reason) > 128 {
		reason = "invalid_operation"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO inbox(op_id, workspace_id, device_id, physical_ms, logical, key_id, nonce, ciphertext, signature, server_seq, received_unix_ms, status, quarantine_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 3, ?)
		ON CONFLICT(workspace_id, op_id) DO UPDATE SET status = 3, quarantine_reason = excluded.quarantine_reason`,
		op.OpID.Bytes(), op.WorkspaceID.Bytes(), op.DeviceID.Bytes(), op.Clock.PhysicalMS, op.Clock.Logical,
		op.KeyID, op.Nonce, op.Ciphertext, op.Signature, op.Sequence, now.UnixMilli(), reason,
	)
	if err != nil {
		return fmt.Errorf("store: quarantine operation: %w", err)
	}
	return nil
}

func (r *SyncRepository) ApplyPage(ctx context.Context, cursor coresync.Cursor, operations []coresync.WireOperation, processor coresync.OperationProcessor, now time.Time) error {
	verified := make([]coresync.VerifiedOperation, len(operations))
	for index, operation := range operations {
		value, err := processor.Verify(ctx, operation)
		if err != nil {
			return coresync.Reject(operation, verificationClass(err), err)
		}
		if value == nil {
			return coresync.Reject(operation, "empty_verified_operation", errors.New("processor returned no apply action"))
		}
		verified[index] = value
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current := coresync.Cursor{WorkspaceID: cursor.WorkspaceID, Epoch: 1}
	err = tx.QueryRowContext(ctx, `SELECT last_seq, cursor_epoch FROM sync_cursors WHERE workspace_id = ? AND transport = ?`, cursor.WorkspaceID.Bytes(), r.transport).
		Scan(&current.LastSequence, &current.Epoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if cursor.Epoch != current.Epoch || (len(operations) != 0 && operations[0].Sequence != current.LastSequence+1) {
		return coresync.ErrInvalidCursor
	}

	for index, operation := range operations {
		encoded, err := coresync.EncodeOperation(operation)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(encoded)
		var priorHash []byte
		err = tx.QueryRowContext(ctx, `SELECT envelope_hash FROM applied_operations WHERE workspace_id = ? AND op_id = ?`, operation.WorkspaceID.Bytes(), operation.OpID.Bytes()).Scan(&priorHash)
		if err == nil {
			if !bytes.Equal(priorHash, digest[:]) {
				return coresync.Reject(operation, "op_id_reuse", errors.New("operation identifier reused with different content"))
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO inbox(op_id, workspace_id, device_id, physical_ms, logical, key_id, nonce, ciphertext, signature, server_seq, received_unix_ms, status)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT(workspace_id, op_id) DO NOTHING`, operation.OpID.Bytes(), operation.WorkspaceID.Bytes(),
			operation.DeviceID.Bytes(), operation.Clock.PhysicalMS, operation.Clock.Logical, operation.KeyID,
			operation.Nonce, operation.Ciphertext, operation.Signature, operation.Sequence, now.UnixMilli()); err != nil {
			return err
		}
		if err := verified[index].Apply(ctx, tx); err != nil {
			return coresync.Reject(operation, "apply_failed", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO applied_operations(workspace_id, op_id, server_seq, envelope_hash, applied_unix_ms)
			VALUES (?, ?, ?, ?, ?)`, operation.WorkspaceID.Bytes(), operation.OpID.Bytes(), operation.Sequence, digest[:], now.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE inbox SET status = 2, quarantine_reason = NULL WHERE workspace_id = ? AND op_id = ?`, operation.WorkspaceID.Bytes(), operation.OpID.Bytes()); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sync_cursors(workspace_id, transport, cursor, updated_unix_ms, last_seq, cursor_epoch)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id) DO UPDATE SET transport = excluded.transport, cursor = excluded.cursor,
		updated_unix_ms = excluded.updated_unix_ms, last_seq = excluded.last_seq, cursor_epoch = excluded.cursor_epoch`,
		cursor.WorkspaceID.Bytes(), r.transport, []byte{}, now.UnixMilli(), cursor.LastSequence, cursor.Epoch); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SyncRepository) Pending(ctx context.Context, workspaceID model.ID, limit int) ([]coresync.WireOperation, error) {
	if limit <= 0 || limit > 256 {
		return nil, errors.New("store: invalid outbox limit")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT op_id, workspace_id, device_id, physical_ms, logical, key_id, nonce, ciphertext, signature
		FROM outbox WHERE workspace_id = ? AND pushed_unix_ms IS NULL AND rejection_reason IS NULL
		ORDER BY id LIMIT ?`, workspaceID.Bytes(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []coresync.WireOperation
	for rows.Next() {
		var operation coresync.WireOperation
		var opID, workspace, device []byte
		if err := rows.Scan(&opID, &workspace, &device, &operation.Clock.PhysicalMS, &operation.Clock.Logical,
			&operation.KeyID, &operation.Nonce, &operation.Ciphertext, &operation.Signature); err != nil {
			return nil, err
		}
		operation.OpID, err = model.ParseID(opID)
		if err == nil {
			operation.WorkspaceID, err = model.ParseID(workspace)
		}
		if err == nil {
			operation.DeviceID, err = model.ParseID(device)
		}
		if err != nil {
			return nil, fmt.Errorf("store: malformed outbox identifier: %w", err)
		}
		operation.Clock.DeviceID = operation.DeviceID
		result = append(result, operation)
	}
	return result, rows.Err()
}

func (r *SyncRepository) MarkPushed(ctx context.Context, workspaceID model.ID, results []coresync.PushResult, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, result := range results {
		if result.PermanentError != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE outbox SET rejection_reason = ? WHERE workspace_id = ? AND op_id = ?`, result.PermanentError, workspaceID.Bytes(), result.OpID.Bytes()); err != nil {
				return err
			}
			continue
		}
		updated, err := tx.ExecContext(ctx, `UPDATE outbox SET pushed_unix_ms = ?, server_seq = ?, rejection_reason = NULL WHERE workspace_id = ? AND op_id = ? AND pushed_unix_ms IS NULL`, now.UnixMilli(), result.Sequence, workspaceID.Bytes(), result.OpID.Bytes())
		if err != nil {
			return err
		}
		if count, _ := updated.RowsAffected(); count != 1 {
			return errors.New("store: push result does not match a pending operation")
		}
	}
	return tx.Commit()
}

type QuarantineEntry struct {
	OperationID model.ID  `json:"operation_id"`
	Sequence    uint64    `json:"sequence"`
	Reason      string    `json:"reason"`
	ReceivedAt  time.Time `json:"received_at"`
}

func (r *SyncRepository) ListQuarantine(ctx context.Context, workspaceID model.ID) ([]QuarantineEntry, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT op_id, server_seq, quarantine_reason, received_unix_ms FROM inbox WHERE workspace_id = ? AND status = 3 ORDER BY server_seq`, workspaceID.Bytes())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []QuarantineEntry
	for rows.Next() {
		var raw []byte
		var unixMS int64
		var item QuarantineEntry
		if err := rows.Scan(&raw, &item.Sequence, &item.Reason, &unixMS); err != nil {
			return nil, err
		}
		item.OperationID, err = model.ParseID(raw)
		if err != nil {
			return nil, err
		}
		item.ReceivedAt = time.UnixMilli(unixMS)
		entries = append(entries, item)
	}
	return entries, rows.Err()
}

// RetryQuarantined removes only the quarantine record. The cursor remains at
// the prior contiguous boundary, so the server must redeliver and reverify it.
func (r *SyncRepository) RetryQuarantined(ctx context.Context, workspaceID, operationID model.ID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM inbox WHERE workspace_id = ? AND op_id = ? AND status = 3`, workspaceID.Bytes(), operationID.Bytes())
	return err
}

const (
	BlobTransferUpload   = 1
	BlobTransferDownload = 2
)

// RecordBlobChunk persists a verified encrypted chunk before the transfer
// advances. A restart can therefore skip only bytes whose digest was already
// checked, without trusting a partial file or volatile progress callback.
func (r *SyncRepository) RecordBlobChunk(ctx context.Context, workspaceID model.ID, blobID []byte, direction, chunkIndex int, digest []byte, now time.Time) error {
	if len(blobID) != sha256.Size || len(digest) != sha256.Size || (direction != BlobTransferUpload && direction != BlobTransferDownload) || chunkIndex < 0 {
		return errors.New("store: invalid blob transfer checkpoint")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO blob_transfers(workspace_id, blob_id, direction, chunk_index, chunk_hash, verified_unix_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_id, blob_id, direction, chunk_index) DO UPDATE SET
		chunk_hash = excluded.chunk_hash, verified_unix_ms = excluded.verified_unix_ms`,
		workspaceID.Bytes(), blobID, direction, chunkIndex, digest, now.UnixMilli())
	return err
}

// HasBlobChunk returns true only when the durable checkpoint contains the
// caller's expected digest. A changed manifest cannot reuse stale progress.
func (r *SyncRepository) HasBlobChunk(ctx context.Context, workspaceID model.ID, blobID []byte, direction, chunkIndex int, digest []byte) (bool, error) {
	if len(blobID) != sha256.Size || len(digest) != sha256.Size || (direction != BlobTransferUpload && direction != BlobTransferDownload) || chunkIndex < 0 {
		return false, errors.New("store: invalid blob transfer checkpoint")
	}
	var stored []byte
	err := r.db.QueryRowContext(ctx, `SELECT chunk_hash FROM blob_transfers WHERE workspace_id = ? AND blob_id = ? AND direction = ? AND chunk_index = ?`,
		workspaceID.Bytes(), blobID, direction, chunkIndex).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return bytes.Equal(stored, digest), err
}

func (r *SyncRepository) CompleteBlobTransfer(ctx context.Context, workspaceID model.ID, blobID []byte, direction int) error {
	if len(blobID) != sha256.Size || (direction != BlobTransferUpload && direction != BlobTransferDownload) {
		return errors.New("store: invalid blob transfer checkpoint")
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM blob_transfers WHERE workspace_id = ? AND blob_id = ? AND direction = ?`, workspaceID.Bytes(), blobID, direction)
	return err
}

func verificationClass(err error) string {
	if errors.Is(err, coresync.ErrUnsupportedVersion) {
		return "unsupported_version"
	}
	return "verification_failed"
}

var _ coresync.WorkspaceRepository = (*SyncRepository)(nil)
