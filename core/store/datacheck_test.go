package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

// TestMissingNoteFTSRowsFindsALiveNoteWithNoIndexRow covers the Advanced
// "Check my data" action's search-index safety net (task 7.10): a live
// note with no notes_fts row at all - which ordinary operation never
// produces, since every note write pairs its notes_fts write in the same
// transaction (see fts.go) - must be found so it can be repaired.
func TestMissingNoteFTSRowsFindsALiveNoteWithNoIndexRow(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	indexed, err := CreateNote(ctx, db, workspaceID, model.Nil, "Indexed", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, db, indexed.ID, "Indexed", ""); err != nil {
		t.Fatal(err)
	}

	// CreateNote alone (unlike account.CreateNote) never touches
	// notes_fts, so this note is missing its index row by construction -
	// simulating the drift MissingNoteFTSRows exists to detect.
	missingIndex, err := CreateNote(ctx, db, workspaceID, model.Nil, "Not indexed", repoClock(t, 11, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	deletedID := mustCreateDeletedNote(t, db, workspaceID)

	got, err := MissingNoteFTSRows(ctx, db, workspaceID)
	if err != nil {
		t.Fatalf("MissingNoteFTSRows() error = %v", err)
	}
	if len(got) != 1 || got[0] != missingIndex.ID {
		t.Fatalf("MissingNoteFTSRows() = %v, want [%v]", got, missingIndex.ID)
	}
	for _, id := range got {
		if id == indexed.ID {
			t.Fatal("MissingNoteFTSRows() reported an already-indexed note")
		}
		if id == deletedID {
			t.Fatal("MissingNoteFTSRows() reported a soft-deleted note, which never needs a search index row")
		}
	}
}

// TestOrphanedNoteFTSRowsFindsAnIndexRowWithNoNote covers the other half of
// the safety net: a notes_fts row whose note was fully removed (unlike a
// soft-deleted note, which keeps its row on purpose - see SearchNotes'
// n.deleted filter) is the only case OrphanedNoteFTSRows should report.
func TestOrphanedNoteFTSRowsFindsAnIndexRowWithNoNote(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	kept := mustCreateDeletedNote(t, db, workspaceID)
	if err := ReplaceNoteFTS(ctx, db, kept, "Deleted but kept", ""); err != nil {
		t.Fatal(err)
	}

	orphanID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, db, orphanID, "Orphaned", "no matching note row"); err != nil {
		t.Fatal(err)
	}

	got, err := OrphanedNoteFTSRows(ctx, db)
	if err != nil {
		t.Fatalf("OrphanedNoteFTSRows() error = %v", err)
	}
	if len(got) != 1 || got[0] != orphanID {
		t.Fatalf("OrphanedNoteFTSRows() = %v, want [%v]", got, orphanID)
	}
}

// TestDeleteNoteFTSRowRemovesOnlyTheNamedRow covers the repair half of
// OrphanedNoteFTSRows: it must remove exactly the orphaned row and leave
// every other row - including a soft-deleted note's deliberately kept row -
// untouched.
func TestDeleteNoteFTSRowRemovesOnlyTheNamedRow(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	kept := mustCreateDeletedNote(t, db, workspaceID)
	if err := ReplaceNoteFTS(ctx, db, kept, "Deleted but kept", ""); err != nil {
		t.Fatal(err)
	}
	orphanID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, db, orphanID, "Orphaned", ""); err != nil {
		t.Fatal(err)
	}

	if err := DeleteNoteFTSRow(ctx, db, orphanID); err != nil {
		t.Fatalf("DeleteNoteFTSRow() error = %v", err)
	}

	var remaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes_fts WHERE note_id = ?`, orphanID.Bytes()).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("notes_fts still has %d row(s) for the deleted orphan", remaining)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes_fts WHERE note_id = ?`, kept.Bytes()).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatal("DeleteNoteFTSRow() removed an unrelated row")
	}
}

// mustCreateDeletedNote creates and soft-deletes a note in workspaceID,
// returning its ID. Its notes_fts row (if any) is expected to survive
// until garbage collection, never flagged as missing or orphaned on its
// own account.
func mustCreateDeletedNote(t testing.TB, db *sql.DB, workspaceID model.ID) model.ID {
	t.Helper()
	ctx := context.Background()
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "Deleted", repoClock(t, 12, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetNoteDeleted(ctx, db, note.ID, true, repoClock(t, 13, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	return note.ID
}
