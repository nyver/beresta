package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

// MarkKeyRotationPending records that workspaceID's key rotation has begun
// but is not yet confirmed complete (see core/keyrotation.TriggerAfterRevocation).
// It is idempotent: calling it again while a marker already exists leaves
// the original requested time untouched.
func MarkKeyRotationPending(ctx context.Context, db *sql.DB, workspaceID model.ID, now time.Time) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO pending_key_rotations(workspace_id, requested_unix_ms) VALUES (?, ?)
		ON CONFLICT(workspace_id) DO NOTHING`, workspaceID.Bytes(), now.UnixMilli())
	return err
}

// ClearKeyRotationPending removes workspaceID's pending-rotation marker
// once the rotation has been published and applied locally.
func ClearKeyRotationPending(ctx context.Context, db *sql.DB, workspaceID model.ID) error {
	_, err := db.ExecContext(ctx, `DELETE FROM pending_key_rotations WHERE workspace_id = ?`, workspaceID.Bytes())
	return err
}

// KeyRotationPending reports whether workspaceID has a rotation that began
// but has not yet been confirmed complete.
func KeyRotationPending(ctx context.Context, db *sql.DB, workspaceID model.ID) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pending_key_rotations WHERE workspace_id = ?)`,
		workspaceID.Bytes()).Scan(&exists)
	return exists != 0, err
}
