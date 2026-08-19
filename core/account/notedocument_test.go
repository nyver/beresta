package account

import (
	"context"
	"errors"
	"testing"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

func TestNoteDocumentStateReturnsEmptyDocumentForUnwrittenNote(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	state, format, err := created.NoteDocumentState(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatalf("NoteDocumentState: %v", err)
	}
	if format != noteSnapshotFormat {
		t.Fatalf("format = %v, want %v", format, noteSnapshotFormat)
	}
	doc, err := yjsadapter.Restore(format, state)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	defer doc.Close()
	text, err := doc.Text(noteBodyRoot)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if text != "" {
		t.Fatalf("text = %q, want empty", text)
	}
}

func TestNoteDocumentStateReflectsCommittedBody(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	update := encodedInsertUpdate(t, "hello world")
	if err := created.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID, Update: update, UpdateFormat: yjsadapter.FormatV2,
	}); err != nil {
		t.Fatalf("CommitNoteBody: %v", err)
	}

	state, format, err := created.NoteDocumentState(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatalf("NoteDocumentState: %v", err)
	}
	doc, err := yjsadapter.Restore(format, state)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	defer doc.Close()
	text, err := doc.Text(noteBodyRoot)
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
}

func TestNoteDocumentStateRejectsNoteFromAnotherWorkspace(t *testing.T) {
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
	if _, _, err := created.NoteDocumentState(ctx, otherWorkspace, note.ID); !errors.Is(err, ErrUnknownWorkspace) {
		t.Fatalf("NoteDocumentState error = %v, want ErrUnknownWorkspace", err)
	}
}

func TestNoteDocumentStateRejectsUnknownNote(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	unknownNote, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := created.NoteDocumentState(ctx, workspaceID, unknownNote); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("NoteDocumentState error = %v, want ErrNotFound", err)
	}
}
