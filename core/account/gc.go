package account

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// gcMinimumRetention is the sync-engine spec's floor before a tombstone's
// history or an unreferenced blob may be collected ("clients and the
// server SHALL not collect associated history or unreferenced blobs until
// the tombstone is at least 30 days old and compaction safety conditions
// are met").
const gcMinimumRetention = 30 * 24 * time.Hour

// GCBlobCandidate is one attachment blob eligible for collection.
type GCBlobCandidate struct {
	BlobID         store.BlobID
	SizeBytes      uint64
	OrphanedUnixMS int64
	// InAnyBackup reports whether this blob's content is still present in
	// at least one of the account's current backup sets, for user
	// reassurance before confirming collection. It does not gate
	// collection: a backup set is a self-contained copy, so live blob
	// collection never affects an already-published backup's own ability
	// to restore.
	InAnyBackup bool
}

// GCNoteCandidate is one tombstoned note eligible for permanent collection.
type GCNoteCandidate struct {
	NoteID        model.ID
	Title         string
	DeletedUnixMS int64
}

// GCReport is the result of RunGarbageCollection: what was (or, in dry-run
// mode, would be) collected.
type GCReport struct {
	Blobs              []GCBlobCandidate
	Notes              []GCNoteCandidate
	BlobBytesReclaimed uint64
	DryRun             bool
}

// RunGarbageCollection identifies every orphaned attachment blob and
// tombstoned note past the 30-day minimum retention window and, unless
// dryRun is true, permanently collects them: the blob's file and catalog
// row, or the note and every row that belongs to it alone (CRDT state,
// revisions, tag/attachment membership, FTS entry). Dry-run mode performs
// no mutation and is safe to call at any time to preview what a real run
// would do.
func (a *Account) RunGarbageCollection(ctx context.Context, now time.Time, dryRun bool) (GCReport, error) {
	db, _, err := a.accountSession()
	if err != nil {
		return GCReport{}, err
	}
	workspaceID := backupSourceWorkspaceID(ctx, db)
	cutoff := now.Add(-gcMinimumRetention).UnixMilli()

	orphaned, err := store.ListOrphanedAttachments(ctx, db, workspaceID, cutoff)
	if err != nil {
		return GCReport{}, err
	}
	backupLocations, err := allBackupLocations(ctx, db)
	if err != nil {
		return GCReport{}, err
	}

	report := GCReport{DryRun: dryRun}
	for _, att := range orphaned {
		report.Blobs = append(report.Blobs, GCBlobCandidate{
			BlobID:         att.BlobID,
			SizeBytes:      att.SizeBytes,
			OrphanedUnixMS: *att.OrphanedUnixMS,
			InAnyBackup:    blobExistsInAnyBackup(att.BlobID, backupLocations),
		})
		report.BlobBytesReclaimed += att.SizeBytes
	}

	notes, err := store.ListNotes(ctx, db, workspaceID)
	if err != nil {
		return GCReport{}, err
	}
	for _, note := range notes {
		if !note.Deleted.Value {
			continue
		}
		if int64(note.Deleted.Clock.PhysicalMS) > cutoff {
			continue
		}
		report.Notes = append(report.Notes, GCNoteCandidate{
			NoteID:        note.ID,
			Title:         note.Title.Value,
			DeletedUnixMS: int64(note.Deleted.Clock.PhysicalMS),
		})
	}

	if dryRun {
		return report, nil
	}

	nowUnixMS := now.UnixMilli()
	for _, candidate := range report.Blobs {
		if err := a.collectBlob(ctx, db, candidate.BlobID); err != nil {
			return report, fmt.Errorf("account: collect blob: %w", err)
		}
	}
	for _, candidate := range report.Notes {
		if err := a.collectNote(ctx, db, candidate.NoteID, nowUnixMS); err != nil {
			return report, fmt.Errorf("account: collect note: %w", err)
		}
	}
	return report, nil
}

// collectBlob removes an attachment's catalog row (in its own transaction)
// before removing its published blob file, matching the architecture's
// publish-before-reference-commit ordering in reverse: a crash between the
// two here leaves an unreferenced file (harmless, eligible for collection
// again later), never a dangling database reference to a missing file.
func (a *Account) collectBlob(ctx context.Context, db *sql.DB, blobID store.BlobID) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin blob collection transaction: %w", err)
	}
	defer tx.Rollback()
	if err := store.DeleteAttachment(ctx, tx, blobID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit blob collection transaction: %w", err)
	}
	if err := os.Remove(a.blobs.Path(blobID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("account: remove collected blob file: %w", err)
	}
	return nil
}

func (a *Account) collectNote(ctx context.Context, db *sql.DB, noteID model.ID, nowUnixMS int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin note collection transaction: %w", err)
	}
	defer tx.Rollback()
	if err := store.DeleteNoteCompletely(ctx, tx, noteID, nowUnixMS); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit note collection transaction: %w", err)
	}
	return nil
}

// allBackupLocations returns the on-disk location of every backup in the
// catalog, across every kind.
func allBackupLocations(ctx context.Context, db store.Executor) ([]string, error) {
	var locations []string
	for _, kind := range []int{store.BackupKindDaily, store.BackupKindPreMigration, store.BackupKindPreRestore, store.BackupKindManual} {
		backups, err := store.ListBackups(ctx, db, kind)
		if err != nil {
			return nil, err
		}
		for _, b := range backups {
			locations = append(locations, b.Location)
		}
	}
	return locations, nil
}

// blobExistsInAnyBackup checks, by cheap file existence alone (no
// decryption), whether id's published blob file is present in any backup
// set's own blob directory.
func blobExistsInAnyBackup(id store.BlobID, backupLocations []string) bool {
	hexID := fmt.Sprintf("%x", id.Bytes())
	for _, location := range backupLocations {
		path := filepath.Join(location, "blobs", hexID[0:2], hexID[2:4], hexID)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}
