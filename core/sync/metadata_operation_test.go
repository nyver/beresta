package sync_test

import (
	"testing"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/sync"
)

func mustNewID(t *testing.T) model.ID {
	t.Helper()
	id, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNoteMetadataOperationRoundTrips(t *testing.T) {
	noteID := mustNewID(t)
	notebookID := mustNewID(t)
	tagID := mustNewID(t)
	blobID := make([]byte, sync.AttachmentBlobIDBytes)
	for i := range blobID {
		blobID[i] = byte(i + 1)
	}

	cases := []sync.NoteMetadataOperation{
		{NoteID: noteID, Kind: sync.NoteMetadataKindNotebook, NotebookID: notebookID},
		// Filing a note at the workspace root is the zero notebook ID, and
		// must round-trip too: model.Nil is a valid payload value here, not
		// a missing field.
		{NoteID: noteID, Kind: sync.NoteMetadataKindNotebook, NotebookID: model.Nil},
		{NoteID: noteID, Kind: sync.NoteMetadataKindTag, TagID: tagID, TagPresent: true},
		{NoteID: noteID, Kind: sync.NoteMetadataKindTag, TagID: tagID, TagPresent: false},
		{NoteID: noteID, Kind: sync.NoteMetadataKindFlags, Flags: model.NoteFlagPinned | model.NoteFlagArchived},
		{NoteID: noteID, Kind: sync.NoteMetadataKindDeleted, Deleted: true},
		{NoteID: noteID, Kind: sync.NoteMetadataKindDeleted, Deleted: false},
		{NoteID: noteID, Kind: sync.NoteMetadataKindAttachment, AttachmentBlobID: blobID, AttachmentPresent: true},
		{NoteID: noteID, Kind: sync.NoteMetadataKindAttachment, AttachmentBlobID: blobID, AttachmentPresent: false},
	}

	for _, want := range cases {
		encoded, err := sync.EncodeNoteMetadataOperation(want)
		if err != nil {
			t.Fatalf("Encode(%+v): %v", want, err)
		}
		got, err := sync.DecodeNoteMetadataOperation(encoded)
		if err != nil {
			t.Fatalf("Decode(Encode(%+v)): %v", want, err)
		}
		if got.NoteID != want.NoteID || got.Kind != want.Kind ||
			got.NotebookID != want.NotebookID ||
			got.TagID != want.TagID || got.TagPresent != want.TagPresent ||
			got.Flags != want.Flags || got.Deleted != want.Deleted ||
			string(got.AttachmentBlobID) != string(want.AttachmentBlobID) || got.AttachmentPresent != want.AttachmentPresent {
			t.Fatalf("round trip = %+v, want %+v", got, want)
		}
	}
}

func TestEncodeNoteMetadataOperationRejectsInvalidInput(t *testing.T) {
	noteID := mustNewID(t)

	if _, err := sync.EncodeNoteMetadataOperation(sync.NoteMetadataOperation{Kind: sync.NoteMetadataKindDeleted}); err == nil {
		t.Fatal("expected an error for a missing note ID")
	}
	if _, err := sync.EncodeNoteMetadataOperation(sync.NoteMetadataOperation{NoteID: noteID, Kind: 0}); err == nil {
		t.Fatal("expected an error for an invalid kind")
	}
	if _, err := sync.EncodeNoteMetadataOperation(sync.NoteMetadataOperation{NoteID: noteID, Kind: sync.NoteMetadataKindTag}); err == nil {
		t.Fatal("expected an error for a missing tag ID")
	}
	if _, err := sync.EncodeNoteMetadataOperation(sync.NoteMetadataOperation{
		NoteID: noteID, Kind: sync.NoteMetadataKindAttachment, AttachmentBlobID: []byte{1, 2, 3},
	}); err == nil {
		t.Fatal("expected an error for a wrong-length attachment blob ID")
	}
}

func TestDecodeNoteMetadataOperationRejectsMalformedInput(t *testing.T) {
	noteID := mustNewID(t)
	valid, err := sync.EncodeNoteMetadataOperation(sync.NoteMetadataOperation{NoteID: noteID, Kind: sync.NoteMetadataKindDeleted, Deleted: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sync.DecodeNoteMetadataOperation(valid[:len(valid)-1]); err == nil {
		t.Fatal("expected an error for truncated deleted payload")
	}

	badVersion := append([]byte(nil), valid...)
	badVersion[0] = 0xff
	if _, err := sync.DecodeNoteMetadataOperation(badVersion); err == nil {
		t.Fatal("expected an error for an unknown version")
	}

	badKind := append([]byte(nil), valid...)
	badKind[17] = 0xff
	if _, err := sync.DecodeNoteMetadataOperation(badKind); err == nil {
		t.Fatal("expected an error for an unknown kind")
	}
}
