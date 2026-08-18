package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

// TestWALRecoveryAfterUncleanClose proves that a write committed to the
// WAL, but never explicitly checkpointed into the main database file,
// survives an unclean shutdown. It copies the live main file and its -wal
// /-shm sidecars to a fresh location while the original connection is
// still open — standing in for the filesystem state a real crash would
// leave, since nothing here ever cleanly checkpoints or closes first — and
// then opens the copy through this package's own Open, which must recover
// the committed write via SQLite's standard WAL replay before returning.
func TestWALRecoveryAfterUncleanClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beresta.db")
	key := testDatabaseKey(t, 0x72)
	defer key.Close()
	ctx := context.Background()

	db, _, err := Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "Recovered note", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	recoveryDir := filepath.Join(dir, "recovered")
	if err := os.MkdirAll(recoveryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	recoveredPath := filepath.Join(recoveryDir, "beresta.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, readErr := os.ReadFile(path + suffix)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			t.Fatal(readErr)
		}
		if err := os.WriteFile(recoveredPath+suffix, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	recoveredDB, _, err := Open(ctx, recoveredPath, key)
	if err != nil {
		t.Fatalf("Open() on the recovered copy error = %v", err)
	}
	defer recoveredDB.Close()

	got, err := GetNote(ctx, recoveredDB, note.ID)
	if err != nil {
		t.Fatalf("GetNote() on the recovered copy error = %v", err)
	}
	if got.Title.Value != "Recovered note" {
		t.Fatalf("Title = %q, want the note committed before the simulated crash", got.Title.Value)
	}
}
