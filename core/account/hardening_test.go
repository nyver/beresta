package account

import (
	"context"
	"testing"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

func TestHardenWorkspaceKeyReencryptsUntouchedNotesUnderCurrentKey(t *testing.T) {
	alice := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)
	ctx := context.Background()

	note, err := alice.CreateNote(ctx, workspaceID, model.Nil, "Hardening target")
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID,
		Update: encodedInsertUpdate(t, "hello"), UpdateFormat: yjsadapter.FormatV2,
	}); err != nil {
		t.Fatal(err)
	}

	rotation, err := alice.BeginWorkspaceKeyRotation(workspaceID, map[model.ID][]byte{alice.ID: alice.IdentityPublicKey})
	if err != nil {
		t.Fatal(err)
	}
	var envelope []byte
	for _, r := range rotation.Recipients {
		if r.UserID == alice.ID {
			envelope = r.Envelope
		}
	}
	if err := alice.AcceptWorkspaceKeyRotation(ctx, workspaceID, rotation.KeyID, envelope, alice.AuthorityPublicKey, rotation.Signature, []model.ID{alice.ID}); err != nil {
		t.Fatal(err)
	}

	// Before hardening, the untouched note's snapshot is still under the old key.
	_, entry, _, _, err := alice.workspaceSession(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(entry.KeyID, rotation.KeyID) {
		t.Fatal("expected entry to be the new current key")
	}

	// Reading the note must still succeed via historical-key fallback.
	if _, _, err := alice.NoteDocumentState(ctx, workspaceID, note.ID); err != nil {
		t.Fatalf("reading a note encrypted under a historical key should succeed: %v", err)
	}

	report, err := alice.HardenWorkspaceKey(ctx, workspaceID)
	if err != nil {
		t.Fatalf("HardenWorkspaceKey: %v", err)
	}
	if report.NotesRehardened != 1 || report.Remaining {
		t.Fatalf("unexpected report: %+v", report)
	}

	// Idempotent: nothing left to harden.
	report2, err := alice.HardenWorkspaceKey(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if report2.NotesRehardened != 0 {
		t.Fatalf("expected no further work, got %+v", report2)
	}

	// The document content must be unchanged.
	if _, _, err := alice.NoteDocumentState(ctx, workspaceID, note.ID); err != nil {
		t.Fatalf("reading the hardened note should still succeed: %v", err)
	}
}
