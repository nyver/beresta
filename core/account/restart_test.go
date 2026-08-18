package account

import (
	"bytes"
	"context"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

// TestNotesNotebooksTagsAndAttachmentsSurviveRestart proves the full
// offline note application surface (notes-management spec, "Organize and
// edit a note": create, format, file under a notebook, tag, and attach a
// file) persists and renders correctly after the account is locked and
// unlocked again, simulating an application restart.
func TestNotesNotebooksTagsAndAttachmentsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	path := tempDBPath(t)
	wrapper := newFakeWrapper()

	created, err := Create(ctx, CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	workspaceID := defaultWorkspaceID(t, created)

	notebook, err := created.CreateNotebook(ctx, workspaceID, model.Nil, "Trip planning")
	if err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	tag, err := created.CreateTag(ctx, workspaceID, "travel")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	note, err := created.CreateNote(ctx, workspaceID, notebook.ID, "Packing list")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := created.SetNoteTag(ctx, workspaceID, note.ID, tag.ID, true); err != nil {
		t.Fatalf("SetNoteTag: %v", err)
	}
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "itinerary.txt", "text/plain", bytes.NewReader([]byte("fly out Monday")))
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	if err := created.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	reopened, err := Unlock(ctx, UnlockOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
	})
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	t.Cleanup(func() { reopened.Lock() })

	gotNote, err := reopened.GetNote(ctx, note.ID)
	if err != nil {
		t.Fatalf("GetNote after restart: %v", err)
	}
	if gotNote.NotebookID.Value != notebook.ID {
		t.Fatalf("notebook after restart = %v, want %v", gotNote.NotebookID.Value, notebook.ID)
	}

	notebooks, err := reopened.ListNotebooks(ctx, workspaceID)
	if err != nil || len(notebooks) != 1 || notebooks[0].Name != "Trip planning" {
		t.Fatalf("ListNotebooks after restart = %v, err = %v", notebooks, err)
	}
	tags, err := reopened.ListTags(ctx, workspaceID)
	if err != nil || len(tags) != 1 || tags[0].Name != "travel" {
		t.Fatalf("ListTags after restart = %v, err = %v", tags, err)
	}

	var out bytes.Buffer
	name, mediaType, err := reopened.ReadAttachment(ctx, workspaceID, attachment.BlobID, &out)
	if err != nil {
		t.Fatalf("ReadAttachment after restart: %v", err)
	}
	if name != "itinerary.txt" || mediaType != "text/plain" || out.String() != "fly out Monday" {
		t.Fatalf("attachment after restart = (%q, %q, %q)", name, mediaType, out.String())
	}
}
