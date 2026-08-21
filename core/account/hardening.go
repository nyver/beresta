package account

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// HardeningReport summarizes one HardenWorkspaceKey batch.
type HardeningReport struct {
	// NotesRehardened is how many note snapshots this call re-encrypted
	// under the workspace's current key.
	NotesRehardened int
	// Remaining is true if at least one more note in the workspace is still
	// encrypted under a non-current key and needs a further call.
	Remaining bool
}

// hardeningBatchSize bounds how much decrypt/re-encrypt work
// HardenWorkspaceKey does per call, so a caller can run it opportunistically
// (for example, once per idle sync cycle) without blocking other work on a
// large collection.
const hardeningBatchSize = 200

// HardenWorkspaceKey re-encrypts, under workspaceID's current key, every
// note snapshot this device still stores under an older (historical or
// retired) key - typically left behind by a workspace key rotation (see
// BeginWorkspaceKeyRotation / AcceptWorkspaceKeyRotation) for any note that
// nobody has edited since. This narrows how much content a retained
// historical key can still decrypt on this device; it does not and cannot
// affect what a formerly authorized party already copied elsewhere (see
// docs/threat-model.md).
//
// It is a purely local operation: no new synchronized operation is
// produced (the document content is unchanged, only its local at-rest
// encryption), and it is safe to call repeatedly - each call processes up
// to a bounded batch and reports whether more work remains, so a caller can
// resume it across restarts or idle ticks without tracking its own cursor.
func (a *Account) HardenWorkspaceKey(ctx context.Context, workspaceID model.ID) (HardeningReport, error) {
	db, current, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return HardeningReport{}, err
	}

	notes, err := store.ListNotes(ctx, db, workspaceID)
	if err != nil {
		return HardeningReport{}, err
	}

	var report HardeningReport
	for _, note := range notes {
		state, ok, err := store.LoadCRDTState(ctx, db, note.ID)
		if err != nil {
			return report, err
		}
		if !ok || len(state.Snapshot) < corecrypto.KeyIDBytes {
			continue
		}
		if bytesEqual(state.Snapshot[:corecrypto.KeyIDBytes], current.KeyID) {
			continue // already under the current key
		}
		if report.NotesRehardened >= hardeningBatchSize {
			report.Remaining = true
			continue
		}
		if err := a.rehardenNoteSnapshot(ctx, db, current, workspaceID, note.ID); err != nil {
			return report, fmt.Errorf("account: harden note %s: %w", note.ID, err)
		}
		report.NotesRehardened++
	}
	return report, nil
}

// rehardenNoteSnapshot decrypts one note's snapshot (potentially under a
// historical key, via loadNoteDocument's key resolution) and re-persists
// the identical CRDT state under current - the workspace's current key -
// in one transaction. It does not touch the note's stored revision history,
// which remains individually re-encryptable by the same mechanism if ever
// needed, and does not change the document content or state vector.
func (a *Account) rehardenNoteSnapshot(ctx context.Context, db *sql.DB, current workspaceKeyEntry, workspaceID, noteID model.ID) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin hardening transaction: %w", err)
	}
	defer tx.Rollback()

	doc, err := loadNoteDocument(ctx, tx, a, workspaceID, noteID)
	if err != nil {
		return err
	}
	defer doc.Close()
	snapshot, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
	if err != nil {
		return fmt.Errorf("account: re-encode note snapshot: %w", err)
	}
	stateVector, err := doc.EncodeStateVector()
	if err != nil {
		return fmt.Errorf("account: re-encode note state vector: %w", err)
	}
	if err := saveNoteSnapshot(ctx, tx, current, workspaceID, noteID, snapshot, stateVector, uint64(time.Now().UnixMilli())); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit hardening transaction: %w", err)
	}
	return nil
}
