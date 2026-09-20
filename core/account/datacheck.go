package account

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// DataCheckResult is the raw, presentation-independent outcome of
// RunDataCheck. core/account cannot import core/presentation directly (see
// core/backupsummary's doc comment for why this package sits above both
// core/store and core/presentation instead); core/datacheck.Summarize turns
// a DataCheckResult, plus the account's current backup catalog, into the
// presentation.DataCheckReport the Advanced "Check my data" action
// displays.
type DataCheckResult struct {
	// IntegrityOK is false when SQLCipher's authenticated page integrity
	// check found a problem.
	IntegrityOK bool
	// SearchIndexRepaired is true when the local search index was found
	// out of step with saved notes and was safely rebuilt.
	SearchIndexRepaired bool
}

// RunDataCheck performs the Advanced "Check my data" action's single,
// consolidated, safe verification pass over local database integrity and
// search index consistency, per the "Routine maintenance and user data
// check" requirement in specs/product-experience. It never itemizes
// internal maintenance jobs to the caller; it stops at integrity failure
// alone, since a corrupt database makes every other check moot.
func (a *Account) RunDataCheck(ctx context.Context, now time.Time) (DataCheckResult, error) {
	db, _, err := a.accountSession()
	if err != nil {
		return DataCheckResult{}, err
	}

	if err := store.CheckIntegrity(ctx, db); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return DataCheckResult{}, ctxErr
		}
		return DataCheckResult{}, nil
	}

	repaired, err := a.repairSearchIndex(ctx, db)
	if err != nil {
		return DataCheckResult{}, err
	}
	return DataCheckResult{IntegrityOK: true, SearchIndexRepaired: repaired}, nil
}

// repairSearchIndex finds and fixes any drift between workspaces' live
// notes and the standalone notes_fts index (see store/fts.go's
// MissingNoteFTSRows/OrphanedNoteFTSRows doc comments): a missing row is
// rebuilt from the note's current CRDT document, and an orphaned row (no
// note left at all) is removed. Every code path that creates or edits a
// note's body already pairs its notes_fts write with the same transaction,
// so this is expected to find and repair nothing; it exists as a safety
// net for interrupted maintenance, not a routine repair path.
func (a *Account) repairSearchIndex(ctx context.Context, db *sql.DB) (bool, error) {
	orphaned, err := store.OrphanedNoteFTSRows(ctx, db)
	if err != nil {
		return false, err
	}
	repaired := false
	for _, noteID := range orphaned {
		if err := inTx(ctx, db, func(tx *sql.Tx) error {
			return store.DeleteNoteFTSRow(ctx, tx, noteID)
		}); err != nil {
			return false, err
		}
		repaired = true
	}

	workspaces, err := a.Workspaces()
	if err != nil {
		return false, err
	}
	for _, workspaceID := range workspaces {
		missing, err := store.MissingNoteFTSRows(ctx, db, workspaceID)
		if err != nil {
			return false, err
		}
		for _, noteID := range missing {
			if err := inTx(ctx, db, func(tx *sql.Tx) error {
				return a.rebuildNoteFTS(ctx, tx, workspaceID, noteID)
			}); err != nil {
				return false, err
			}
			repaired = true
		}
	}
	return repaired, nil
}

// rebuildNoteFTS re-derives noteID's notes_fts row from its current
// canonical title and CRDT body, the same content ReplaceNoteFTS's regular
// callers (CreateNote, CommitNoteBody) already index.
func (a *Account) rebuildNoteFTS(ctx context.Context, exec store.Executor, workspaceID, noteID model.ID) error {
	note, err := store.GetNote(ctx, exec, noteID)
	if err != nil {
		return err
	}
	doc, err := loadNoteDocument(ctx, exec, a, workspaceID, noteID)
	if err != nil {
		return err
	}
	defer doc.Close()
	markdown, err := doc.Markdown(noteBodyRoot)
	if err != nil {
		return fmt.Errorf("account: project note markdown for search index repair: %w", err)
	}
	return store.ReplaceNoteFTS(ctx, exec, noteID, note.Title.Value, markdown)
}

// inTx runs fn inside its own transaction on db, committing on success and
// rolling back otherwise.
func inTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin data check repair transaction: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit data check repair transaction: %w", err)
	}
	return nil
}
