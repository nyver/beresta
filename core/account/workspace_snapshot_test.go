package account

import (
	"bytes"
	"context"
	"testing"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
)

func TestSnapshotOperationArchiveIsStrictAndContiguous(t *testing.T) {
	device := snapshotTestID(t, 2)
	operation := coresync.WireOperation{OpID: snapshotTestID(t, 3), WorkspaceID: snapshotTestID(t, 1), DeviceID: device, Sequence: 1,
		Clock: model.HLC{PhysicalMS: 1000, Logical: 1, DeviceID: device}, KeyID: bytes.Repeat([]byte{4}, 16),
		Nonce: bytes.Repeat([]byte{5}, 24), Ciphertext: bytes.Repeat([]byte{6}, 32), Signature: bytes.Repeat([]byte{7}, 64)}
	encoded, err := encodeSnapshotOperations([]coresync.WireOperation{operation})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSnapshotOperations(encoded)
	if err != nil || len(decoded) != 1 || decoded[0].OpID != operation.OpID {
		t.Fatalf("decoded=%v err=%v", decoded, err)
	}
	if _, err := decodeSnapshotOperations(append(encoded, 0)); err == nil {
		t.Fatal("snapshot archive accepted trailing data")
	}
}

func TestSnapshotCatalogArchiveRoundTrips(t *testing.T) {
	workspaceID := snapshotTestID(t, 1)
	deviceID := snapshotTestID(t, 2)
	notebookID := snapshotTestID(t, 3)
	tagID := snapshotTestID(t, 4)
	noteID := snapshotTestID(t, 5)
	attachmentID, err := store.ParseBlobID(bytes.Repeat([]byte{5}, store.BlobIDBytes))
	if err != nil {
		t.Fatal(err)
	}
	clock := model.HLC{PhysicalMS: 1000, Logical: 1, DeviceID: deviceID}
	operation := coresync.WireOperation{OpID: snapshotTestID(t, 6), WorkspaceID: workspaceID, DeviceID: deviceID, Sequence: 1,
		Clock: clock, KeyID: bytes.Repeat([]byte{7}, 16), Nonce: bytes.Repeat([]byte{8}, 24), Ciphertext: bytes.Repeat([]byte{9}, 32), Signature: bytes.Repeat([]byte{10}, 64)}
	catalog := snapshotCatalog{
		Notebooks: []store.Notebook{{ID: notebookID, WorkspaceID: workspaceID, Name: "Projects", NameClock: clock, ParentClock: clock, CreatedAt: clock}},
		Tags:      []store.Tag{{ID: tagID, WorkspaceID: workspaceID, Name: "important", CreatedAt: clock}},
		Attachments: []store.Attachment{{BlobID: attachmentID, WorkspaceID: workspaceID, KeyID: bytes.Repeat([]byte{7}, 16),
			Manifest: []byte("encrypted manifest"), SizeBytes: 42, ChunkCount: 1, CreatedUnixMS: 1000}},
		NoteAssignments: []snapshotNoteAssignment{{NoteID: noteID, NotebookID: notebookID, NotebookClock: clock}},
	}
	encoded, err := encodeWorkspaceSnapshotArchive([]coresync.WireOperation{operation}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	operations, decoded, err := decodeWorkspaceSnapshotArchive(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].OpID != operation.OpID || len(decoded.Notebooks) != 1 || decoded.Notebooks[0].Name != "Projects" ||
		len(decoded.Tags) != 1 || decoded.Tags[0].Name != "important" || len(decoded.Attachments) != 1 || decoded.Attachments[0].BlobID != attachmentID ||
		len(decoded.NoteAssignments) != 1 || decoded.NoteAssignments[0].NoteID != noteID || decoded.NoteAssignments[0].NotebookID != notebookID {
		t.Fatalf("snapshot catalog round trip = operations=%+v catalog=%+v", operations, decoded)
	}
}

func TestApplySnapshotNoteAssignmentsRepairsLegacyNotebookAssignment(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	notebook, err := created.CreateNotebook(ctx, workspaceID, model.Nil, "Projects")
	if err != nil {
		t.Fatal(err)
	}
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Legacy note")
	if err != nil {
		t.Fatal(err)
	}
	clock := model.HLC{PhysicalMS: note.CreatedAt.PhysicalMS + 1, DeviceID: created.DeviceID}
	if err := applySnapshotNoteAssignments(ctx, created.db, workspaceID, []snapshotNoteAssignment{{
		NoteID: note.ID, NotebookID: notebook.ID, NotebookClock: clock,
	}}); err != nil {
		t.Fatalf("applySnapshotNoteAssignments: %v", err)
	}
	got, err := created.GetNote(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NotebookID.Value != notebook.ID || got.NotebookID.Clock != clock {
		t.Fatalf("notebook assignment = %+v, want notebook %v at %v", got.NotebookID, notebook.ID, clock)
	}
}

func snapshotTestID(t *testing.T, value byte) model.ID {
	t.Helper()
	raw := bytes.Repeat([]byte{value}, 16)
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	id, err := model.ParseID(raw)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
