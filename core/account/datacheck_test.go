package account

import (
	"context"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

// TestRunDataCheckOnAHealthyAccountReportsNoRepair covers the Advanced
// "Check my data" action's common case (task 7.10): a freshly created,
// untouched account has a healthy database and a consistent search index,
// so RunDataCheck must report both without repairing anything.
func TestRunDataCheckOnAHealthyAccountReportsNoRepair(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untouched"); err != nil {
		t.Fatal(err)
	}

	result, err := created.RunDataCheck(ctx, time.Now())
	if err != nil {
		t.Fatalf("RunDataCheck() error = %v", err)
	}
	if !result.IntegrityOK {
		t.Fatal("IntegrityOK = false, want true")
	}
	if result.SearchIndexRepaired {
		t.Fatal("SearchIndexRepaired = true, want false on an untouched account")
	}
}

// TestRunDataCheckRepairsAMissingSearchIndexRow covers the safety net for a
// live note whose notes_fts row is missing - the drift an interrupted
// maintenance run could leave, per specs/product-experience's "Routine
// maintenance and user data check" requirement. RunDataCheck must rebuild
// the row from the note's current title and CRDT body, not merely notice
// the gap.
func TestRunDataCheckRepairsAMissingSearchIndexRow(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Recipe")
	if err != nil {
		t.Fatal(err)
	}
	update := encodedInsertUpdate(t, "buy oat milk")
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID:  workspaceID,
		NoteID:       note.ID,
		Update:       update,
		UpdateFormat: yjsadapter.FormatV2,
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate the drift an interrupted maintenance run could leave: the
	// note's own write path always pairs its notes_fts row with the same
	// transaction (see fts.go), so this can only be produced directly.
	if err := store.DeleteNoteFTSRow(ctx, created.db, note.ID); err != nil {
		t.Fatal(err)
	}

	result, err := created.RunDataCheck(ctx, time.Now())
	if err != nil {
		t.Fatalf("RunDataCheck() error = %v", err)
	}
	if !result.IntegrityOK {
		t.Fatal("IntegrityOK = false, want true")
	}
	if !result.SearchIndexRepaired {
		t.Fatal("SearchIndexRepaired = false, want true")
	}

	var count int
	if err := created.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notes_fts WHERE notes_fts MATCH 'milk' AND note_id = ?`, note.ID.Bytes(),
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("notes_fts MATCH 'milk' for the repaired note = %d rows, want 1", count)
	}
}

// TestRunDataCheckRemovesAnOrphanedSearchIndexRow covers the other half of
// the safety net: a notes_fts row with no matching note at all (as opposed
// to a soft-deleted note, which keeps its row on purpose) must be removed.
func TestRunDataCheckRemovesAnOrphanedSearchIndexRow(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)

	orphanID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceNoteFTS(ctx, created.db, orphanID, "Orphaned", "no matching note row"); err != nil {
		t.Fatal(err)
	}

	result, err := created.RunDataCheck(ctx, time.Now())
	if err != nil {
		t.Fatalf("RunDataCheck() error = %v", err)
	}
	if !result.SearchIndexRepaired {
		t.Fatal("SearchIndexRepaired = false, want true")
	}

	var count int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes_fts WHERE note_id = ?`, orphanID.Bytes()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("orphaned notes_fts row still present after RunDataCheck")
	}
}

// TestRunDataCheckOnALockedAccountFails covers that RunDataCheck, like
// every other Account method, refuses to run once the account is locked.
func TestRunDataCheckOnALockedAccountFails(t *testing.T) {
	created := createTestAccount(t)
	created.Lock()

	if _, err := created.RunDataCheck(context.Background(), time.Now()); err != ErrAccountLocked {
		t.Fatalf("RunDataCheck() error = %v, want %v", err, ErrAccountLocked)
	}
}
