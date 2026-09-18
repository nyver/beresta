package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

// TestReplaceNoteFTSInterruptedTransactionLeavesPriorIndexIntact covers
// task 5.8's restart-safe full-text-index maintenance: ReplaceNoteFTS is
// deliberately delete-then-insert, run inside the same transaction as the
// note's own commit, so that a transaction which never reaches commit
// (an interrupted process, exactly like every other write in this package)
// leaves notes_fts exactly as it was before the attempt - never missing the
// deleted row without its replacement, and never holding stale content
// alongside it.
func TestReplaceNoteFTSInterruptedTransactionLeavesPriorIndexIntact(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	noteID := mustNewID(t)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, tx, noteID, "Original title", "original body"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertNoteFTSMatchesNote(t, db, "original", noteID)

	// Simulate a process terminated mid-commit: the same delete-then-insert
	// runs again with new content, but the transaction is rolled back
	// instead of committed.
	interrupted, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, interrupted, noteID, "Never saved title", "never saved body"); err != nil {
		t.Fatal(err)
	}
	if err := interrupted.Rollback(); err != nil {
		t.Fatal(err)
	}

	// The original row must still be there, completely unchanged: an
	// interrupted delete-then-insert must never resolve to "deleted only",
	// and the never-committed content must not be findable.
	assertNoteFTSMatchesNote(t, db, "original", noteID)
	assertNoFTSMatch(t, db, "never")

	var title string
	if err := db.QueryRowContext(ctx, `SELECT title FROM notes_fts WHERE note_id = ?`, noteID.Bytes()).Scan(&title); err != nil {
		t.Fatalf("read notes_fts row after rollback: %v", err)
	}
	if title != "Original title" {
		t.Fatalf("notes_fts title after an interrupted replace = %q, want unchanged %q", title, "Original title")
	}
}

func assertNoteFTSMatchesNote(t *testing.T, db *sql.DB, term string, wantNoteID model.ID) {
	t.Helper()
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, `SELECT note_id FROM notes_fts WHERE notes_fts MATCH ?`, term)
	if err != nil {
		t.Fatalf("query notes_fts for %q: %v", term, err)
	}
	defer rows.Close()

	var matched [][]byte
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		matched = append(matched, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("notes_fts MATCH %q returned %d rows, want 1", term, len(matched))
	}
	got, err := model.ParseID(matched[0])
	if err != nil {
		t.Fatal(err)
	}
	if got != wantNoteID {
		t.Fatalf("notes_fts MATCH %q returned note %v, want %v", term, got, wantNoteID)
	}
}

func assertNoFTSMatch(t *testing.T, db *sql.DB, term string) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM notes_fts WHERE notes_fts MATCH ?`, term,
	).Scan(&count); err != nil {
		t.Fatalf("query notes_fts for %q: %v", term, err)
	}
	if count != 0 {
		t.Fatalf("notes_fts MATCH %q returned %d rows, want 0 (never-committed content)", term, count)
	}
}
