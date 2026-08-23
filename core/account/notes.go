package account

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync"
)

// CreateNote creates a new note filed under notebookID (model.Nil files it
// at the workspace root) with the given title and an empty body, and
// records the creation as a signed encrypted outbox operation. The note's
// rich-text body is populated separately through CommitNoteBody.
func (a *Account) CreateNote(ctx context.Context, workspaceID, notebookID model.ID, title string) (model.Note, error) {
	db, entry, deviceID, devicePrivate, err := a.workspaceSession(workspaceID)
	if err != nil {
		return model.Note{}, err
	}
	clock, err := a.tick()
	if err != nil {
		return model.Note{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Note{}, fmt.Errorf("account: begin create note transaction: %w", err)
	}
	defer tx.Rollback()

	note, err := store.CreateNote(ctx, tx, workspaceID, notebookID, title, clock)
	if err != nil {
		return model.Note{}, err
	}
	if err := store.ReplaceNoteFTS(ctx, tx, note.ID, title, ""); err != nil {
		return model.Note{}, err
	}

	payload, err := sync.EncodeNoteBodyOperation(sync.NoteBodyOperation{NoteID: note.ID, Title: &title})
	if err != nil {
		return model.Note{}, fmt.Errorf("account: encode note creation operation: %w", err)
	}
	if err := writeOutboxOperation(ctx, tx, entry, devicePrivate, workspaceID, deviceID, clock, payload); err != nil {
		return model.Note{}, err
	}
	if err := store.AdvanceDeviceClock(ctx, tx, deviceID, clock); err != nil {
		return model.Note{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.Note{}, fmt.Errorf("account: commit create note transaction: %w", err)
	}
	return note, nil
}

// GetNote returns one note's metadata row.
func (a *Account) GetNote(ctx context.Context, noteID model.ID) (model.Note, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return model.Note{}, ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()
	return store.GetNote(ctx, db, noteID)
}

// ListNotes returns every note in a workspace, including deleted ones.
func (a *Account) ListNotes(ctx context.Context, workspaceID model.ID) ([]model.Note, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return nil, ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()
	return store.ListNotes(ctx, db, workspaceID)
}

// SetNoteNotebook reassigns a note's notebook (model.Nil files it at the
// workspace root) and records the change as a signed encrypted outbox
// operation.
func (a *Account) SetNoteNotebook(ctx context.Context, workspaceID, noteID, notebookID model.ID) error {
	return a.commitNoteMetadata(ctx, workspaceID, sync.NoteMetadataOperation{
		NoteID: noteID, Kind: sync.NoteMetadataKindNotebook, NotebookID: notebookID,
	}, func(ctx context.Context, tx store.Executor, clock model.HLC) error {
		return store.SetNoteNotebook(ctx, tx, noteID, notebookID, clock)
	})
}

// SetNoteFlags replaces a note's flag bitmask and records the change as a
// signed encrypted outbox operation.
func (a *Account) SetNoteFlags(ctx context.Context, workspaceID, noteID model.ID, flags model.NoteFlags) error {
	return a.commitNoteMetadata(ctx, workspaceID, sync.NoteMetadataOperation{
		NoteID: noteID, Kind: sync.NoteMetadataKindFlags, Flags: flags,
	}, func(ctx context.Context, tx store.Executor, clock model.HLC) error {
		return store.SetNoteFlags(ctx, tx, noteID, flags, clock)
	})
}

// DeleteNote marks a note deleted. Its revision history and CRDT state are
// preserved as recoverable history, matching the notes-management
// requirement that deletion is a tombstone, not erasure.
func (a *Account) DeleteNote(ctx context.Context, workspaceID, noteID model.ID) error {
	return a.setNoteDeleted(ctx, workspaceID, noteID, true)
}

// RestoreNote clears a previously deleted note's tombstone.
func (a *Account) RestoreNote(ctx context.Context, workspaceID, noteID model.ID) error {
	return a.setNoteDeleted(ctx, workspaceID, noteID, false)
}

func (a *Account) setNoteDeleted(ctx context.Context, workspaceID, noteID model.ID, deleted bool) error {
	return a.commitNoteMetadata(ctx, workspaceID, sync.NoteMetadataOperation{
		NoteID: noteID, Kind: sync.NoteMetadataKindDeleted, Deleted: deleted,
	}, func(ctx context.Context, tx store.Executor, clock model.HLC) error {
		return store.SetNoteDeleted(ctx, tx, noteID, deleted, clock)
	})
}

// SetNoteTag adds or removes a note's membership in one tag and records the
// change as a signed encrypted outbox operation.
func (a *Account) SetNoteTag(ctx context.Context, workspaceID, noteID, tagID model.ID, present bool) error {
	return a.commitNoteMetadata(ctx, workspaceID, sync.NoteMetadataOperation{
		NoteID: noteID, Kind: sync.NoteMetadataKindTag, TagID: tagID, TagPresent: present,
	}, func(ctx context.Context, tx store.Executor, clock model.HLC) error {
		return store.SetNoteTag(ctx, tx, noteID, tagID, present, clock)
	})
}

// commitNoteMetadata runs apply against a note in workspaceID inside one
// transaction, then appends the corresponding signed encrypted
// NoteMetadataOperation to the outbox before committing. Every note-level
// metadata mutation (notebook assignment, tag membership, flags,
// delete/restore, attachment references) shares this shape so the local
// materialized state and its outbox record always commit atomically
// together.
func (a *Account) commitNoteMetadata(ctx context.Context, workspaceID model.ID, op sync.NoteMetadataOperation, apply func(ctx context.Context, tx store.Executor, clock model.HLC) error) error {
	db, entry, deviceID, devicePrivate, err := a.workspaceSession(workspaceID)
	if err != nil {
		return err
	}
	clock, err := a.tick()
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin note metadata transaction: %w", err)
	}
	defer tx.Rollback()

	note, err := store.GetNote(ctx, tx, op.NoteID)
	if err != nil {
		return err
	}
	if note.WorkspaceID != workspaceID {
		return store.ErrWrongWorkspace
	}

	if err := apply(ctx, tx, clock); err != nil {
		return err
	}

	payload, err := sync.EncodeNoteMetadataOperation(op)
	if err != nil {
		return fmt.Errorf("account: encode note metadata operation: %w", err)
	}
	if err := writeOutboxOperation(ctx, tx, entry, devicePrivate, workspaceID, deviceID, clock, payload); err != nil {
		return err
	}
	if err := store.AdvanceDeviceClock(ctx, tx, deviceID, clock); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit note metadata transaction: %w", err)
	}
	return nil
}

// CreateNotebook creates a new notebook in workspaceID. A zero parentID
// files it at the workspace root. Notebook structure is local-first
// organization; it does not itself produce an outbox operation (see
// core/sync.NoteMetadataOperation's doc comment).
func (a *Account) CreateNotebook(ctx context.Context, workspaceID, parentID model.ID, name string) (store.Notebook, error) {
	db, _, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return store.Notebook{}, err
	}
	clock, err := a.tick()
	if err != nil {
		return store.Notebook{}, err
	}
	notebook, err := store.CreateNotebook(ctx, db, workspaceID, parentID, name, clock)
	if err != nil {
		return store.Notebook{}, err
	}
	if err := store.AdvanceDeviceClock(ctx, db, a.DeviceID, clock); err != nil {
		return store.Notebook{}, err
	}
	return notebook, nil
}

// RenameNotebook applies an LWW rename to a notebook.
func (a *Account) RenameNotebook(ctx context.Context, workspaceID, notebookID model.ID, name string) error {
	return a.mutateWorkspace(ctx, workspaceID, func(db *sql.DB, clock model.HLC) error {
		return store.RenameNotebook(ctx, db, notebookID, name, clock)
	})
}

// MoveNotebook reassigns a notebook's parent.
func (a *Account) MoveNotebook(ctx context.Context, workspaceID, notebookID, newParentID model.ID) error {
	return a.mutateWorkspace(ctx, workspaceID, func(db *sql.DB, clock model.HLC) error {
		return store.MoveNotebook(ctx, db, notebookID, newParentID, clock)
	})
}

// SetNotebookDeleted sets or clears a notebook's tombstone.
func (a *Account) SetNotebookDeleted(ctx context.Context, workspaceID, notebookID model.ID, deleted bool) error {
	return a.mutateWorkspace(ctx, workspaceID, func(db *sql.DB, clock model.HLC) error {
		return store.SetNotebookDeleted(ctx, db, notebookID, deleted, clock)
	})
}

// ListNotebooks returns every notebook in a workspace, including deleted
// ones.
func (a *Account) ListNotebooks(ctx context.Context, workspaceID model.ID) ([]store.Notebook, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return nil, ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()
	return store.ListNotebooks(ctx, db, workspaceID)
}

// CreateTag creates a new workspace tag.
func (a *Account) CreateTag(ctx context.Context, workspaceID model.ID, name string) (store.Tag, error) {
	db, _, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return store.Tag{}, err
	}
	clock, err := a.tick()
	if err != nil {
		return store.Tag{}, err
	}
	tag, err := store.CreateTag(ctx, db, workspaceID, name, clock)
	if err != nil {
		return store.Tag{}, err
	}
	if err := store.AdvanceDeviceClock(ctx, db, a.DeviceID, clock); err != nil {
		return store.Tag{}, err
	}
	return tag, nil
}

// SetTagDeleted sets or clears a tag's tombstone.
func (a *Account) SetTagDeleted(ctx context.Context, workspaceID, tagID model.ID, deleted bool) error {
	return a.mutateWorkspace(ctx, workspaceID, func(db *sql.DB, clock model.HLC) error {
		return store.SetTagDeleted(ctx, db, tagID, deleted, clock)
	})
}

// ListTags returns every tag in a workspace, including deleted ones.
func (a *Account) ListTags(ctx context.Context, workspaceID model.ID) ([]store.Tag, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return nil, ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()
	return store.ListTags(ctx, db, workspaceID)
}

// NoteTagIDsByWorkspace returns every note's current tag membership in a
// workspace, keyed by note ID.
func (a *Account) NoteTagIDsByWorkspace(ctx context.Context, workspaceID model.ID) (map[model.ID][]model.ID, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return nil, ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()
	return store.NoteTagIDsByWorkspace(ctx, db, workspaceID)
}

// mutateWorkspace ticks the clock and runs apply against the account's
// database (captured under lock, not read fresh from a.db) outside an
// explicit transaction, then advances the persisted device clock. It is
// used for single-statement structural mutations (notebook/tag lifecycle)
// that do not produce an outbox operation.
func (a *Account) mutateWorkspace(ctx context.Context, workspaceID model.ID, apply func(db *sql.DB, clock model.HLC) error) error {
	db, _, deviceID, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return err
	}
	clock, err := a.tick()
	if err != nil {
		return err
	}
	if err := apply(db, clock); err != nil {
		return err
	}
	return store.AdvanceDeviceClock(ctx, db, deviceID, clock)
}
