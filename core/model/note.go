package model

import (
	"errors"
	"fmt"
)

// MaxNoteTitleBytes bounds one note title as a resource-limit guard, not a
// UX affordance; the UI may impose a tighter practical limit.
const MaxNoteTitleBytes = 1024

// ErrInvalidNote reports a structurally invalid note record.
var ErrInvalidNote = errors.New("model: invalid note")

// NoteFlags is a bitmask of user-visible note flags.
type NoteFlags uint32

const (
	// NoteFlagPinned keeps a note pinned to the top of its list.
	NoteFlagPinned NoteFlags = 1 << iota
	// NoteFlagArchived hides a note from default views without deleting it.
	NoteFlagArchived
)

const validNoteFlags = NoteFlagPinned | NoteFlagArchived

// Note is the metadata register set for one note. The rich-text body is a
// CRDT document tracked separately by the synchronization layer. Notebook
// assignment, title, flags, and the delete tombstone use LWW registers
// ordered by Hybrid Logical Clock so concurrent metadata edits converge
// deterministically; per-tag registers are added when notebook/tag
// persistence lands. A zero NotebookID.Value means the note is not filed
// under any notebook.
type Note struct {
	ID          ID
	WorkspaceID ID
	NotebookID  LWW[ID]
	Title       LWW[string]
	Flags       LWW[NoteFlags]
	Deleted     LWW[bool]
	CreatedAt   HLC
}

// Validate rejects a structurally invalid note record. It does not enforce
// cross-replica convergence rules, which belong to the repository and sync
// layers.
func (n Note) Validate() error {
	if err := n.ID.Validate(); err != nil {
		return fmt.Errorf("%w: ID", ErrInvalidNote)
	}
	if err := n.WorkspaceID.Validate(); err != nil {
		return fmt.Errorf("%w: workspace ID", ErrInvalidNote)
	}
	if !n.NotebookID.Value.IsZero() {
		if err := n.NotebookID.Value.Validate(); err != nil {
			return fmt.Errorf("%w: notebook ID", ErrInvalidNote)
		}
	}
	if err := n.NotebookID.Clock.Validate(); err != nil {
		return fmt.Errorf("%w: notebook clock", ErrInvalidNote)
	}
	if len(n.Title.Value) > MaxNoteTitleBytes {
		return fmt.Errorf("%w: title", ErrInvalidNote)
	}
	if err := n.Title.Clock.Validate(); err != nil {
		return fmt.Errorf("%w: title clock", ErrInvalidNote)
	}
	if n.Flags.Value&^validNoteFlags != 0 {
		return fmt.Errorf("%w: flags", ErrInvalidNote)
	}
	if err := n.Flags.Clock.Validate(); err != nil {
		return fmt.Errorf("%w: flags clock", ErrInvalidNote)
	}
	if err := n.Deleted.Clock.Validate(); err != nil {
		return fmt.Errorf("%w: deleted clock", ErrInvalidNote)
	}
	if err := n.CreatedAt.Validate(); err != nil || n.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created clock", ErrInvalidNote)
	}
	return nil
}
