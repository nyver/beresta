package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const opaqueHistoryRetention = 30 * 24 * time.Hour

type CompactionResult struct {
	WorkspaceID        string `json:"workspace_id"`
	SnapshotID         string `json:"snapshot_id,omitempty"`
	BaseSequence       int64  `json:"base_seq"`
	EligibleOperations int64  `json:"eligible_operations"`
	RemovedOperations  int64  `json:"removed_operations"`
	DryRun             bool   `json:"dry_run"`
}

// CompactWorkspace removes only acknowledged opaque history that is both at
// or below an eligible snapshot and older than the 30-day safety boundary.
// Sequence values are never renumbered.
func (s *Storage) CompactWorkspace(ctx context.Context, workspaceID string, now time.Time, dryRun bool) (CompactionResult, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return CompactionResult{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CompactionResult{}, err
	}
	defer tx.Rollback()
	var snapshotID string
	var baseSequence int64
	err = tx.QueryRowContext(ctx, `
		SELECT snapshot_id, base_seq FROM snapshots
		WHERE workspace_id = ? AND eligible_at IS NOT NULL
		ORDER BY base_seq DESC, created_at DESC LIMIT 1`, workspaceID).Scan(&snapshotID, &baseSequence)
	if errors.Is(err, sql.ErrNoRows) {
		return CompactionResult{WorkspaceID: workspaceID, DryRun: dryRun}, nil
	}
	if err != nil {
		return CompactionResult{}, err
	}
	boundary := unixNow(now.Add(-opaqueHistoryRetention))
	var count int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE workspace_id = ? AND seq <= ? AND created_at <= ?`, workspaceID, baseSequence, boundary).Scan(&count); err != nil {
		return CompactionResult{}, err
	}
	result := CompactionResult{WorkspaceID: workspaceID, SnapshotID: snapshotID, BaseSequence: baseSequence, EligibleOperations: count, DryRun: dryRun}
	if dryRun {
		return result, nil
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM operations WHERE workspace_id = ? AND seq <= ? AND created_at <= ?`, workspaceID, baseSequence, boundary)
	if err != nil {
		return CompactionResult{}, err
	}
	result.RemovedOperations, _ = deleted.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET compaction_snapshot_id = ?, compacted_through_seq = MAX(compacted_through_seq, ?) WHERE workspace_id = ?`, snapshotID, baseSequence, workspaceID); err != nil {
		return CompactionResult{}, fmt.Errorf("server: record compaction boundary: %w", err)
	}
	return result, tx.Commit()
}
