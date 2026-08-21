package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

// OpLogGCReport is the result of RunOperationLogGarbageCollection: how many
// rows were (or, in dry-run mode, would be) permanently removed from each
// local synchronization bookkeeping table.
type OpLogGCReport struct {
	InboxRowsCollected    int
	OutboxRowsCollected   int
	AppliedRowsCollected  int
	SnapshotRowsCollected int
	DryRun                bool
}

func (r OpLogGCReport) total() int {
	return r.InboxRowsCollected + r.OutboxRowsCollected + r.AppliedRowsCollected + r.SnapshotRowsCollected
}

// RunOperationLogGarbageCollection permanently removes local synchronization
// history this device no longer needs: applied inbox entries, pushed outbox
// entries, their applied_operations dedup records, and superseded local
// snapshot catalog rows (see saveSnapshotCatalogRow). It never removes a
// quarantined inbox entry (status 3) - those stay until a user resolves
// them through the quarantine journal - or an outbox entry still pending or
// rejected, since those remain actionable local state, not settled history.
//
// A workspace's history is eligible for collection only up through the
// base sequence of a snapshot this device has itself authenticated and
// acknowledged (see saveSnapshotCatalogRow's acknowledged flag - "active
// device acknowledgement" in the sync-engine spec's compaction-safety
// sense, applied locally rather than waited on from the server) and that
// is itself at least the sync-engine spec's 30-day floor old, the same
// floor RunGarbageCollection (see gc.go) applies to tombstones and
// unreferenced blobs. Operations more recent than that boundary, and every
// workspace with no acknowledged snapshot yet, are left untouched.
//
// This is purely local housekeeping: it never touches an already-published
// backup (see core/account/backup.go), whose own self-contained copy of
// these tables is unaffected by anything the live database prunes
// afterward, and it produces no synchronized operation of its own.
func (a *Account) RunOperationLogGarbageCollection(ctx context.Context, now time.Time, dryRun bool) (OpLogGCReport, error) {
	db, _, err := a.accountSession()
	if err != nil {
		return OpLogGCReport{}, err
	}
	workspaces, err := a.Workspaces()
	if err != nil {
		return OpLogGCReport{}, err
	}
	cutoff := now.Add(-gcMinimumRetention).UnixMilli()

	report := OpLogGCReport{DryRun: dryRun}
	for _, workspaceID := range workspaces {
		boundarySnapshotID, boundarySeq, ok, err := latestEligibleSnapshotBoundary(ctx, db, workspaceID, cutoff)
		if err != nil {
			return report, err
		}
		if !ok {
			continue
		}
		counts, err := collectWorkspaceOperationLog(ctx, db, workspaceID, boundarySnapshotID, boundarySeq, dryRun)
		if err != nil {
			return report, fmt.Errorf("account: collect operation log for workspace %s: %w", workspaceID, err)
		}
		report.InboxRowsCollected += counts.inbox
		report.OutboxRowsCollected += counts.outbox
		report.AppliedRowsCollected += counts.applied
		report.SnapshotRowsCollected += counts.snapshots
	}
	return report, nil
}

// latestEligibleSnapshotBoundary returns the most recent local snapshot
// catalog row for workspaceID that this device has acknowledged and that
// is at least cutoff old, if any.
func latestEligibleSnapshotBoundary(ctx context.Context, db *sql.DB, workspaceID model.ID, cutoff int64) (id model.ID, baseSeq uint64, ok bool, err error) {
	var idBytes []byte
	err = db.QueryRowContext(ctx, `
		SELECT id, base_seq FROM snapshots
		WHERE workspace_id = ? AND acknowledged_unix_ms IS NOT NULL AND created_unix_ms <= ?
		ORDER BY base_seq DESC LIMIT 1`, workspaceID.Bytes(), cutoff,
	).Scan(&idBytes, &baseSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Nil, 0, false, nil
	}
	if err != nil {
		return model.Nil, 0, false, err
	}
	id, err = model.ParseID(idBytes)
	if err != nil {
		return model.Nil, 0, false, err
	}
	return id, baseSeq, true, nil
}

type opLogCounts struct {
	inbox, outbox, applied, snapshots int
}

func collectWorkspaceOperationLog(ctx context.Context, db *sql.DB, workspaceID model.ID, boundarySnapshotID model.ID, boundarySeq uint64, dryRun bool) (opLogCounts, error) {
	var counts opLogCounts
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM inbox WHERE workspace_id = ? AND status = 2 AND server_seq <= ?`,
		workspaceID.Bytes(), boundarySeq).Scan(&counts.inbox); err != nil {
		return counts, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbox WHERE workspace_id = ? AND pushed_unix_ms IS NOT NULL AND server_seq <= ?`,
		workspaceID.Bytes(), boundarySeq).Scan(&counts.outbox); err != nil {
		return counts, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM applied_operations WHERE workspace_id = ? AND server_seq <= ?`,
		workspaceID.Bytes(), boundarySeq).Scan(&counts.applied); err != nil {
		return counts, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM snapshots WHERE workspace_id = ? AND base_seq < ? AND id != ?`,
		workspaceID.Bytes(), boundarySeq, boundarySnapshotID.Bytes()).Scan(&counts.snapshots); err != nil {
		return counts, err
	}
	if dryRun || counts.total() == 0 {
		return counts, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return counts, fmt.Errorf("account: begin operation-log GC transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM inbox WHERE workspace_id = ? AND status = 2 AND server_seq <= ?`, workspaceID.Bytes(), boundarySeq); err != nil {
		return counts, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM outbox WHERE workspace_id = ? AND pushed_unix_ms IS NOT NULL AND server_seq <= ?`, workspaceID.Bytes(), boundarySeq); err != nil {
		return counts, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM applied_operations WHERE workspace_id = ? AND server_seq <= ?`, workspaceID.Bytes(), boundarySeq); err != nil {
		return counts, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM snapshots WHERE workspace_id = ? AND base_seq < ? AND id != ?`, workspaceID.Bytes(), boundarySeq, boundarySnapshotID.Bytes()); err != nil {
		return counts, err
	}
	if err := tx.Commit(); err != nil {
		return counts, fmt.Errorf("account: commit operation-log GC transaction: %w", err)
	}
	return counts, nil
}

func (c opLogCounts) total() int { return c.inbox + c.outbox + c.applied + c.snapshots }
