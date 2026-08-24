package account

import (
	"context"
	"errors"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync"
)

func TestCreateNotePersistsRowFTSAndOutboxOperation(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Grocery list")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if note.Title.Value != "Grocery list" {
		t.Fatalf("title = %q", note.Title.Value)
	}

	got, err := created.GetNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.WorkspaceID != workspaceID || got.Title.Value != "Grocery list" {
		t.Fatalf("GetNote = %+v", got)
	}

	var ftsTitle string
	if err := created.db.QueryRowContext(ctx, `SELECT title FROM notes_fts WHERE note_id = ?`, note.ID.Bytes()).Scan(&ftsTitle); err != nil {
		t.Fatalf("query notes_fts: %v", err)
	}
	if ftsTitle != "Grocery list" {
		t.Fatalf("fts title = %q", ftsTitle)
	}

	var outboxCount int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE workspace_id = ?`, workspaceID.Bytes()).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("outbox count = %d, want 1", outboxCount)
	}

	notes, err := created.ListNotes(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != note.ID {
		t.Fatalf("ListNotes = %+v", notes)
	}
}

func TestCreateNoteInNotebookAppendsNotebookAssignmentOperation(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	notebook, err := created.CreateNotebook(ctx, workspaceID, model.Nil, "Projects")
	if err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	note, err := created.CreateNote(ctx, workspaceID, notebook.ID, "Launch plan")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	var outboxCount int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE workspace_id = ?`, workspaceID.Bytes()).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 2 {
		t.Fatalf("outbox count = %d, want 2", outboxCount)
	}

	var opID, ciphertext, nonce, keyID []byte
	if err := created.db.QueryRowContext(ctx,
		`SELECT op_id, ciphertext, nonce, key_id FROM outbox WHERE workspace_id = ? ORDER BY id DESC LIMIT 1`, workspaceID.Bytes(),
	).Scan(&opID, &ciphertext, &nonce, &keyID); err != nil {
		t.Fatal(err)
	}
	entry := created.workspaceKeys[workspaceID]
	opPlaintext, err := corecrypto.OpenObject(entry.Key, corecrypto.EncryptedObject{Metadata: corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1, CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID: workspaceID.Bytes(), ObjectID: opID, ObjectType: corecrypto.ObjectTypeOperationPayload, KeyID: keyID,
	}, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		t.Fatalf("open notebook assignment operation: %v", err)
	}
	defer opPlaintext.Close()
	var decoded sync.NoteMetadataOperation
	if err := opPlaintext.Use(func(data []byte) error {
		var decodeErr error
		decoded, decodeErr = sync.DecodeNoteMetadataOperation(data)
		return decodeErr
	}); err != nil {
		t.Fatalf("decode notebook assignment operation: %v", err)
	}
	if decoded.Kind != sync.NoteMetadataKindNotebook || decoded.NoteID != note.ID || decoded.NotebookID != notebook.ID {
		t.Fatalf("decoded operation = %+v", decoded)
	}
}

func TestNoteMetadataMutationsPersistAndAppendOutboxOperations(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	notebook, err := created.CreateNotebook(ctx, workspaceID, model.Nil, "Personal")
	if err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	tag, err := created.CreateTag(ctx, workspaceID, "urgent")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	wantOutbox := 1 // note creation
	if err := created.SetNoteNotebook(ctx, workspaceID, note.ID, notebook.ID); err != nil {
		t.Fatalf("SetNoteNotebook: %v", err)
	}
	wantOutbox++
	if err := created.SetNoteTag(ctx, workspaceID, note.ID, tag.ID, true); err != nil {
		t.Fatalf("SetNoteTag: %v", err)
	}
	wantOutbox++
	if err := created.SetNoteFlags(ctx, workspaceID, note.ID, model.NoteFlagPinned); err != nil {
		t.Fatalf("SetNoteFlags: %v", err)
	}
	wantOutbox++
	if err := created.DeleteNote(ctx, workspaceID, note.ID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	wantOutbox++
	if err := created.RestoreNote(ctx, workspaceID, note.ID); err != nil {
		t.Fatalf("RestoreNote: %v", err)
	}
	wantOutbox++

	got, err := created.GetNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.NotebookID.Value != notebook.ID {
		t.Fatalf("notebook = %v, want %v", got.NotebookID.Value, notebook.ID)
	}
	if got.Flags.Value != model.NoteFlagPinned {
		t.Fatalf("flags = %v", got.Flags.Value)
	}
	if got.Deleted.Value {
		t.Fatal("note should be restored (not deleted)")
	}

	tagIDs, err := store.NoteTagIDs(ctx, created.db, note.ID)
	if err != nil || len(tagIDs) != 1 || tagIDs[0] != tag.ID {
		t.Fatalf("NoteTagIDs = %v, err = %v", tagIDs, err)
	}

	var outboxCount int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE workspace_id = ?`, workspaceID.Bytes()).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != wantOutbox {
		t.Fatalf("outbox count = %d, want %d", outboxCount, wantOutbox)
	}

	// The most recent outbox operation (the restore) must decode back to the
	// exact metadata mutation that was applied.
	var opID, ciphertext, nonce, keyID []byte
	if err := created.db.QueryRowContext(ctx,
		`SELECT op_id, ciphertext, nonce, key_id FROM outbox WHERE workspace_id = ? ORDER BY id DESC LIMIT 1`, workspaceID.Bytes(),
	).Scan(&opID, &ciphertext, &nonce, &keyID); err != nil {
		t.Fatal(err)
	}
	entry := created.workspaceKeys[workspaceID]
	opMetadata := corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		ObjectID:      opID,
		ObjectType:    corecrypto.ObjectTypeOperationPayload,
		KeyID:         keyID,
	}
	opPlaintext, err := corecrypto.OpenObject(entry.Key, corecrypto.EncryptedObject{Metadata: opMetadata, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		t.Fatalf("open last outbox operation: %v", err)
	}
	defer opPlaintext.Close()
	var decoded sync.NoteMetadataOperation
	if err := opPlaintext.Use(func(data []byte) error {
		var decodeErr error
		decoded, decodeErr = sync.DecodeNoteMetadataOperation(data)
		return decodeErr
	}); err != nil {
		t.Fatalf("decode last outbox operation: %v", err)
	}
	if decoded.Kind != sync.NoteMetadataKindDeleted || decoded.NoteID != note.ID || decoded.Deleted {
		t.Fatalf("decoded last operation = %+v", decoded)
	}
}

func TestNoteMetadataMutationRejectsWrongWorkspace(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	otherWorkspace, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := created.SetNoteFlags(ctx, otherWorkspace, note.ID, model.NoteFlagPinned); !errors.Is(err, ErrUnknownWorkspace) {
		t.Fatalf("SetNoteFlags with unknown workspace error = %v, want ErrUnknownWorkspace", err)
	}
}

func TestNotebookAndTagLifecycleIsLocalOnly(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	root, err := created.CreateNotebook(ctx, workspaceID, model.Nil, "Work")
	if err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	child, err := created.CreateNotebook(ctx, workspaceID, root.ID, "Projects")
	if err != nil {
		t.Fatalf("CreateNotebook (child): %v", err)
	}

	if err := created.RenameNotebook(ctx, workspaceID, child.ID, "Archive"); err != nil {
		t.Fatalf("RenameNotebook: %v", err)
	}
	if err := created.MoveNotebook(ctx, workspaceID, child.ID, model.Nil); err != nil {
		t.Fatalf("MoveNotebook: %v", err)
	}
	if err := created.SetNotebookDeleted(ctx, workspaceID, root.ID, true); err != nil {
		t.Fatalf("SetNotebookDeleted: %v", err)
	}

	notebooks, err := created.ListNotebooks(ctx, workspaceID)
	if err != nil || len(notebooks) != 2 {
		t.Fatalf("ListNotebooks = %v, err = %v", notebooks, err)
	}

	tag, err := created.CreateTag(ctx, workspaceID, "reading")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if err := created.SetTagDeleted(ctx, workspaceID, tag.ID, true); err != nil {
		t.Fatalf("SetTagDeleted: %v", err)
	}
	tags, err := created.ListTags(ctx, workspaceID)
	if err != nil || len(tags) != 1 || !tags[0].Deleted {
		t.Fatalf("ListTags = %v, err = %v", tags, err)
	}

	// Notebook/tag structural lifecycle never appends an outbox operation
	// (see core/sync.NoteMetadataOperation's doc comment); only note-level
	// mutations do.
	var outboxCount int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM outbox WHERE workspace_id = ?`, workspaceID.Bytes()).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("outbox count = %d, want 0", outboxCount)
	}
}
