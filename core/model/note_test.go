package model

import (
	"errors"
	"strings"
	"testing"
)

func validNote(t *testing.T) Note {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	device := testDeviceID(t, 0x33)
	clock := HLC{PhysicalMS: 1, DeviceID: device}
	return Note{
		ID:          id,
		WorkspaceID: workspaceID,
		NotebookID:  LWW[ID]{Value: Nil, Clock: clock},
		Title:       LWW[string]{Value: "Shopping list", Clock: clock},
		Flags:       LWW[NoteFlags]{Value: NoteFlagPinned, Clock: clock},
		Deleted:     LWW[bool]{Value: false, Clock: HLC{}},
		CreatedAt:   clock,
	}
}

func TestNoteValidate(t *testing.T) {
	if err := validNote(t).Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}

	notebookID, err := NewID()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		modify func(*Note)
	}{
		{"zero ID", func(n *Note) { n.ID = Nil }},
		{"zero workspace ID", func(n *Note) { n.WorkspaceID = Nil }},
		{"non-zero but invalid notebook ID", func(n *Note) { var bad ID; bad[0] = 1; n.NotebookID.Value = bad }},
		{"valid notebook ID accepted", func(n *Note) { n.NotebookID.Value = notebookID }},
		{"oversized title", func(n *Note) { n.Title.Value = strings.Repeat("a", MaxNoteTitleBytes+1) }},
		{"unknown flag bits", func(n *Note) { n.Flags.Value = NoteFlags(1 << 31) }},
		{"zero created clock", func(n *Note) { n.CreatedAt = HLC{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := validNote(t)
			tt.modify(&n)
			err := n.Validate()
			if tt.name == "valid notebook ID accepted" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidNote) {
				t.Fatalf("Validate() error = %v, want ErrInvalidNote", err)
			}
		})
	}
}

func TestNoteValidateAllowsUnwrittenNotebookAndDeletedRegisters(t *testing.T) {
	n := validNote(t)
	n.NotebookID = LWW[ID]{} // never filed under a notebook
	n.Deleted = LWW[bool]{}  // never deleted
	if err := n.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}
