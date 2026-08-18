package store

import (
	"context"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// OutboxOperation is one signed, encrypted local operation queued for a
// future synchronization transport (schema/v1/operation.md's client-signed
// form; seq is assigned later by a server and is not part of this record).
type OutboxOperation struct {
	OpID        model.ID
	WorkspaceID model.ID
	DeviceID    model.ID
	Clock       model.HLC
	KeyID       []byte
	Nonce       []byte
	Ciphertext  []byte
	Signature   []byte
}

// InsertOutboxOperation appends one operation to the outbox for a future
// synchronization transport to push.
func InsertOutboxOperation(ctx context.Context, exec Executor, op OutboxOperation, createdUnixMS uint64) error {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO outbox (op_id, workspace_id, device_id, physical_ms, logical, key_id, nonce, ciphertext, signature, created_unix_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.OpID.Bytes(), op.WorkspaceID.Bytes(), op.DeviceID.Bytes(),
		op.Clock.PhysicalMS, op.Clock.Logical,
		op.KeyID, op.Nonce, op.Ciphertext, op.Signature, createdUnixMS,
	); err != nil {
		return fmt.Errorf("store: insert outbox operation: %w", err)
	}
	return nil
}
