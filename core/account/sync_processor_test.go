package account

import (
	"context"
	"testing"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
)

func TestAttachmentMetadataCreatesPlaceholderBeforeReference(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "note")
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}

	blobID, err := store.ParseBlobID(make([]byte, store.BlobIDBytes))
	if err != nil {
		t.Fatalf("ParseBlobID() error = %v", err)
	}
	clock := model.HLC{PhysicalMS: 1_000, DeviceID: created.DeviceID}
	transaction, err := created.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	operation := verifiedNoteOperation{
		account: created,
		wire: coresync.WireOperation{
			WorkspaceID: workspaceID,
			Clock:       clock,
		},
		metadata: &coresync.NoteMetadataOperation{
			NoteID:            note.ID,
			Kind:              coresync.NoteMetadataKindAttachment,
			AttachmentBlobID:  blobID.Bytes(),
			AttachmentPresent: true,
		},
	}
	if err := operation.applyMetadata(ctx, transaction); err != nil {
		transaction.Rollback()
		t.Fatalf("applyMetadata() error = %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	attachment, err := store.GetAttachment(ctx, created.db, blobID)
	if err != nil {
		t.Fatalf("GetAttachment() error = %v", err)
	}
	if !attachment.IsPlaceholder() {
		t.Fatal("attachment catalog row is not a placeholder")
	}
	blobIDs, err := store.NoteAttachmentBlobIDs(ctx, created.db, note.ID)
	if err != nil {
		t.Fatalf("NoteAttachmentBlobIDs() error = %v", err)
	}
	if len(blobIDs) != 1 || blobIDs[0] != blobID {
		t.Fatalf("NoteAttachmentBlobIDs() = %v, want [%v]", blobIDs, blobID)
	}
}

func TestTagMetadataCreatesPlaceholderBeforeReference(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "note")
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	tagID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	clock := model.HLC{PhysicalMS: 1_000, DeviceID: created.DeviceID}
	transaction, err := created.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	operation := verifiedNoteOperation{
		account: created,
		wire: coresync.WireOperation{
			WorkspaceID: workspaceID,
			Clock:       clock,
		},
		metadata: &coresync.NoteMetadataOperation{
			NoteID:     note.ID,
			Kind:       coresync.NoteMetadataKindTag,
			TagID:      tagID,
			TagPresent: true,
		},
	}
	if err := operation.applyMetadata(ctx, transaction); err != nil {
		transaction.Rollback()
		t.Fatalf("applyMetadata() error = %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	var deleted bool
	if err := created.db.QueryRowContext(ctx, `SELECT deleted FROM tags WHERE id = ?`, tagID.Bytes()).Scan(&deleted); err != nil {
		t.Fatalf("query placeholder tag: %v", err)
	}
	if !deleted {
		t.Fatal("tag placeholder must be hidden as deleted")
	}
	tagIDs, err := store.NoteTagIDs(ctx, created.db, note.ID)
	if err != nil {
		t.Fatalf("NoteTagIDs() error = %v", err)
	}
	if len(tagIDs) != 1 || tagIDs[0] != tagID {
		t.Fatalf("NoteTagIDs() = %v, want [%v]", tagIDs, tagID)
	}
}

func TestNotebookMetadataCreatesPlaceholderBeforeReference(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "note")
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	notebookID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	clock := model.HLC{PhysicalMS: 1_000, DeviceID: created.DeviceID}
	transaction, err := created.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	operation := verifiedNoteOperation{
		account: created,
		wire: coresync.WireOperation{
			WorkspaceID: workspaceID,
			Clock:       clock,
		},
		metadata: &coresync.NoteMetadataOperation{
			NoteID:     note.ID,
			Kind:       coresync.NoteMetadataKindNotebook,
			NotebookID: notebookID,
		},
	}
	// Before the fix, this failed with "FOREIGN KEY constraint failed"
	// because notebookID had never been materialized locally.
	if err := operation.applyMetadata(ctx, transaction); err != nil {
		transaction.Rollback()
		t.Fatalf("applyMetadata() error = %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	var deleted bool
	if err := created.db.QueryRowContext(ctx, `SELECT deleted FROM notebooks WHERE id = ?`, notebookID.Bytes()).Scan(&deleted); err != nil {
		t.Fatalf("query placeholder notebook: %v", err)
	}
	if !deleted {
		t.Fatal("notebook placeholder must be hidden as deleted")
	}
	got, err := store.GetNote(ctx, created.db, note.ID)
	if err != nil {
		t.Fatalf("GetNote() error = %v", err)
	}
	if got.NotebookID.Value != notebookID {
		t.Fatalf("NotebookID = %v, want %v", got.NotebookID.Value, notebookID)
	}
}
