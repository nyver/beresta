package account

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// TestRandomizedLocalOperationsAndRestoreConverge runs randomized sequences
// of local note operations (task 4.11's required randomized local-
// operation and restore recovery tests), snapshots expected state at a
// backup checkpoint, keeps mutating, then proves RestoreWhole recovers
// exactly the checkpointed state — not just "some" state — regardless of
// which random sequence produced it. Each seed is deterministic and
// reproducible on failure.
func TestRandomizedLocalOperationsAndRestoreConverge(t *testing.T) {
	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			testRandomizedOperationsAndRestoreConverge(t, seed)
		})
	}
}

type randomizedNoteState struct {
	title   string
	deleted bool
	flags   model.NoteFlags
}

func testRandomizedOperationsAndRestoreConverge(t *testing.T, seed int64) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	rng := rand.New(rand.NewSource(seed))

	live := make(map[model.ID]*randomizedNoteState)
	randomLiveID := func() (model.ID, bool) {
		if len(live) == 0 {
			return model.Nil, false
		}
		ids := make([]model.ID, 0, len(live))
		for id := range live {
			ids = append(ids, id)
		}
		return ids[rng.Intn(len(ids))], true
	}

	randomOp := func() {
		switch rng.Intn(4) {
		case 0: // create
			title := fmt.Sprintf("Note %d", rng.Int())
			n, err := created.CreateNote(ctx, workspaceID, model.Nil, title)
			if err != nil {
				t.Fatalf("CreateNote: %v", err)
			}
			live[n.ID] = &randomizedNoteState{title: title}
		case 1: // edit body (does not change the tracked comparison fields)
			id, ok := randomLiveID()
			if !ok {
				return
			}
			commitInsert(t, created, workspaceID, id, "x")
		case 2: // toggle delete/restore
			id, ok := randomLiveID()
			if !ok {
				return
			}
			st := live[id]
			if st.deleted {
				if err := created.RestoreNote(ctx, workspaceID, id); err != nil {
					t.Fatalf("RestoreNote: %v", err)
				}
			} else {
				if err := created.DeleteNote(ctx, workspaceID, id); err != nil {
					t.Fatalf("DeleteNote: %v", err)
				}
			}
			st.deleted = !st.deleted
		case 3: // toggle pinned flag
			id, ok := randomLiveID()
			if !ok {
				return
			}
			st := live[id]
			newFlags := st.flags ^ model.NoteFlagPinned
			if err := created.SetNoteFlags(ctx, workspaceID, id, newFlags); err != nil {
				t.Fatalf("SetNoteFlags: %v", err)
			}
			st.flags = newFlags
		}
	}

	for i := 0; i < 30; i++ {
		randomOp()
	}

	checkpoint := make(map[model.ID]randomizedNoteState, len(live))
	for id, st := range live {
		checkpoint[id] = *st
	}
	backup, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	for i := 0; i < 20; i++ {
		randomOp()
	}

	if _, err := created.RestoreWhole(ctx, backup.ID, t.TempDir(), time.Now()); err != nil {
		t.Fatalf("RestoreWhole: %v", err)
	}

	notes, err := created.ListNotes(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListNotes after restore: %v", err)
	}
	if len(notes) != len(checkpoint) {
		t.Fatalf("note count after restore = %d, want %d (checkpoint)", len(notes), len(checkpoint))
	}
	for _, n := range notes {
		want, ok := checkpoint[n.ID]
		if !ok {
			t.Fatalf("restore produced an unexpected note %v (%q)", n.ID, n.Title.Value)
		}
		if n.Title.Value != want.title || n.Deleted.Value != want.deleted || n.Flags.Value != want.flags {
			t.Fatalf("note %v after restore = (title=%q deleted=%v flags=%v), want (title=%q deleted=%v flags=%v)",
				n.ID, n.Title.Value, n.Deleted.Value, n.Flags.Value, want.title, want.deleted, want.flags)
		}
	}
}
