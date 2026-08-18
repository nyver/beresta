package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

// TestTransactionAtomicityProperty randomly composes a batch of repository
// writes spanning every kind of row this package manages (notebook, tag,
// note, note-tag membership, attachment, note-attachment reference, saved
// search, outbox operation) inside one transaction, then randomly commits
// or rolls it back, and asserts the workspace ends up with either the
// whole batch or none of it — never a partial subset. It repeats this
// across many random trials so the atomicity property is exercised well
// beyond the two fixed operations
// TestRepositoryFunctionsComposeIntoOneTransaction covers.
func TestTransactionAtomicityProperty(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()

	for trial := 0; trial < 50; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		workspaceID := seedWorkspace(t, db)
		commit := rng.Intn(2) == 0
		clockBase := uint64(1000 + trial*100)

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}

		notebook, err := CreateNotebook(ctx, tx, workspaceID, model.Nil, fmt.Sprintf("Notebook %d", trial), repoClock(t, clockBase, 0, 0x02))
		if err != nil {
			t.Fatal(err)
		}
		tag, err := CreateTag(ctx, tx, workspaceID, fmt.Sprintf("tag-%d", trial), repoClock(t, clockBase, 1, 0x02))
		if err != nil {
			t.Fatal(err)
		}
		note, err := CreateNote(ctx, tx, workspaceID, notebook.ID, fmt.Sprintf("Note %d", trial), repoClock(t, clockBase, 2, 0x02))
		if err != nil {
			t.Fatal(err)
		}
		if err := SetNoteTag(ctx, tx, note.ID, tag.ID, true, repoClock(t, clockBase, 3, 0x02)); err != nil {
			t.Fatal(err)
		}
		blobID := testBlobID(t, byte(trial+1))
		if _, err := CreateAttachment(ctx, tx, workspaceID, blobID, []byte("key"), []byte("manifest"), 10, 1, int64(clockBase)); err != nil {
			t.Fatal(err)
		}
		if err := SetNoteAttachment(ctx, tx, note.ID, blobID, true, repoClock(t, clockBase, 4, 0x02), int64(clockBase)); err != nil {
			t.Fatal(err)
		}
		if _, err := CreateSavedSearch(ctx, tx, workspaceID, fmt.Sprintf("search-%d", trial), fmt.Sprintf("tag:tag-%d", trial), int64(clockBase)); err != nil {
			t.Fatal(err)
		}
		opID, err := model.NewID()
		if err != nil {
			t.Fatal(err)
		}
		op := OutboxOperation{
			OpID: opID, WorkspaceID: workspaceID, DeviceID: repoTestDeviceID(t, 0x02),
			Clock: repoClock(t, clockBase, 5, 0x02),
			KeyID: []byte("key"), Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), Signature: []byte("sig"),
		}
		if err := InsertOutboxOperation(ctx, tx, op, clockBase); err != nil {
			t.Fatal(err)
		}

		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}

		notebooks, err := ListNotebooks(ctx, db, workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		notes, err := ListNotes(ctx, db, workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		tags, err := ListTags(ctx, db, workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		searches, err := ListSavedSearches(ctx, db, workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		_, attachmentErr := GetAttachment(ctx, db, blobID)

		if commit {
			if len(notebooks) != 1 || len(notes) != 1 || len(tags) != 1 || len(searches) != 1 || errors.Is(attachmentErr, ErrNotFound) {
				t.Fatalf("trial %d: commit left a partial batch: notebooks=%d notes=%d tags=%d searches=%d attachmentErr=%v",
					trial, len(notebooks), len(notes), len(tags), len(searches), attachmentErr)
			}
		} else {
			if len(notebooks) != 0 || len(notes) != 0 || len(tags) != 0 || len(searches) != 0 || !errors.Is(attachmentErr, ErrNotFound) {
				t.Fatalf("trial %d: rollback left a partial batch: notebooks=%d notes=%d tags=%d searches=%d attachmentErr=%v",
					trial, len(notebooks), len(notes), len(tags), len(searches), attachmentErr)
			}
		}
	}
}
