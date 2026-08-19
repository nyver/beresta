package main

import (
	"encoding/base64"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// NoteDTO is the JS-facing shape of a model.Note: identifiers are dashed
// UUID strings, LWW registers are flattened to their current value, and
// clocks are Unix milliseconds.
type NoteDTO struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	NotebookID  string `json:"notebook_id"`
	Title       string `json:"title"`
	Pinned      bool   `json:"pinned"`
	Archived    bool   `json:"archived"`
	Deleted     bool   `json:"deleted"`
	CreatedMS   int64  `json:"created_unix_ms"`
}

func noteDTO(n model.Note) NoteDTO {
	return NoteDTO{
		ID:          idString(n.ID),
		WorkspaceID: idString(n.WorkspaceID),
		NotebookID:  idString(n.NotebookID.Value),
		Title:       n.Title.Value,
		Pinned:      n.Flags.Value&model.NoteFlagPinned != 0,
		Archived:    n.Flags.Value&model.NoteFlagArchived != 0,
		Deleted:     n.Deleted.Value,
		CreatedMS:   int64(n.CreatedAt.PhysicalMS),
	}
}

func noteDTOs(notes []model.Note) []NoteDTO {
	out := make([]NoteDTO, len(notes))
	for i, n := range notes {
		out[i] = noteDTO(n)
	}
	return out
}

// CreateNote creates a note in the account's workspace, filed under
// notebookID (empty string files it at the workspace root).
func (a *App) CreateNote(notebookID, title string) (NoteDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return NoteDTO{}, mapError(err)
	}
	nb, err := parseID(notebookID)
	if err != nil {
		return NoteDTO{}, mapError(err)
	}
	note, err := acc.CreateNote(a.requestContext(), workspaceID, nb, title)
	if err != nil {
		return NoteDTO{}, mapError(err)
	}
	return noteDTO(note), nil
}

// GetNote returns one note's metadata.
func (a *App) GetNote(noteID string) (NoteDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return NoteDTO{}, mapError(err)
	}
	id, err := parseID(noteID)
	if err != nil {
		return NoteDTO{}, mapError(err)
	}
	note, err := acc.GetNote(a.requestContext(), id)
	if err != nil {
		return NoteDTO{}, mapError(err)
	}
	return noteDTO(note), nil
}

// ListNotes returns every note in the account's workspace, including
// deleted ones (the frontend distinguishes by the Deleted field).
func (a *App) ListNotes() ([]NoteDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	notes, err := acc.ListNotes(a.requestContext(), workspaceID)
	if err != nil {
		return nil, mapError(err)
	}
	return noteDTOs(notes), nil
}

// SetNoteNotebook reassigns a note's notebook (empty notebookID files it
// at the workspace root).
func (a *App) SetNoteNotebook(noteID, notebookID string) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return mapError(err)
	}
	nb, err := parseID(notebookID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.SetNoteNotebook(a.requestContext(), workspaceID, note, nb))
}

// SetNoteFlags replaces a note's pinned/archived state.
func (a *App) SetNoteFlags(noteID string, pinned, archived bool) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return mapError(err)
	}
	var flags model.NoteFlags
	if pinned {
		flags |= model.NoteFlagPinned
	}
	if archived {
		flags |= model.NoteFlagArchived
	}
	return mapError(acc.SetNoteFlags(a.requestContext(), workspaceID, note, flags))
}

// DeleteNote tombstones a note; its history is preserved and recoverable
// via RestoreNote.
func (a *App) DeleteNote(noteID string) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.DeleteNote(a.requestContext(), workspaceID, note))
}

// RestoreNote clears a note's tombstone.
func (a *App) RestoreNote(noteID string) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.RestoreNote(a.requestContext(), workspaceID, note))
}

// SetNoteTag adds or removes a note's membership in one tag.
func (a *App) SetNoteTag(noteID, tagID string, present bool) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return mapError(err)
	}
	tag, err := parseID(tagID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.SetNoteTag(a.requestContext(), workspaceID, note, tag, present))
}

// CommitNoteBodyRequest carries one atomic editor save.
type CommitNoteBodyRequest struct {
	NoteID string `json:"note_id"`
	// UpdateBase64 is a base64-encoded Yjs update in UpdateFormat.
	UpdateBase64 string `json:"update_base64"`
	// UpdateFormat is "v1" or "v2" (see docs/crypto-spec.md's Yjs update
	// encoding note); the editor integration (task 5.4) determines which
	// one it emits.
	UpdateFormat string `json:"update_format"`
	// Title, when non-nil, also renames the note in the same commit.
	Title *string `json:"title,omitempty"`
}

// CommitNoteBody atomically applies one CRDT update (and optional title
// change) to a note's body.
func (a *App) CommitNoteBody(req CommitNoteBodyRequest) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	noteID, err := parseID(req.NoteID)
	if err != nil {
		return mapError(err)
	}
	update, err := decodeBase64(req.UpdateBase64)
	if err != nil {
		return mapError(err)
	}
	format, err := parseYjsFormat(req.UpdateFormat)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.CommitNoteBody(a.requestContext(), account.NoteBodyCommand{
		WorkspaceID:  workspaceID,
		NoteID:       noteID,
		Update:       update,
		UpdateFormat: format,
		Title:        req.Title,
	}))
}

// NoteDocumentDTO is a note's complete current CRDT body state, for a
// client-side editor to hydrate a fresh Y.Doc from when it opens the note.
type NoteDocumentDTO struct {
	// UpdateBase64 is a Yjs update (from an empty document) in Format.
	UpdateBase64 string `json:"update_base64"`
	Format       string `json:"format"`
}

// GetNoteDocument returns a note's current body as a Yjs update an editor
// can apply to a fresh Y.Doc. It returns an empty document (not an error)
// for a note that has never had a body command applied.
func (a *App) GetNoteDocument(noteID string) (NoteDocumentDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return NoteDocumentDTO{}, mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return NoteDocumentDTO{}, mapError(err)
	}
	state, format, err := acc.NoteDocumentState(a.requestContext(), workspaceID, note)
	if err != nil {
		return NoteDocumentDTO{}, mapError(err)
	}
	return NoteDocumentDTO{UpdateBase64: base64.StdEncoding.EncodeToString(state), Format: yjsFormatString(format)}, nil
}

// --- Notebooks ---

// NotebookDTO is the JS-facing shape of a store.Notebook.
type NotebookDTO struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	ParentID    string `json:"parent_id"`
	Name        string `json:"name"`
	Deleted     bool   `json:"deleted"`
}

func notebookDTO(nb store.Notebook) NotebookDTO {
	return NotebookDTO{
		ID:          idString(nb.ID),
		WorkspaceID: idString(nb.WorkspaceID),
		ParentID:    idString(nb.ParentID),
		Name:        nb.Name,
		Deleted:     nb.Deleted,
	}
}

// CreateNotebook creates a notebook (empty parentID files it at the
// workspace root).
func (a *App) CreateNotebook(parentID, name string) (NotebookDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return NotebookDTO{}, mapError(err)
	}
	parent, err := parseID(parentID)
	if err != nil {
		return NotebookDTO{}, mapError(err)
	}
	nb, err := acc.CreateNotebook(a.requestContext(), workspaceID, parent, name)
	if err != nil {
		return NotebookDTO{}, mapError(err)
	}
	return notebookDTO(nb), nil
}

// RenameNotebook applies an LWW rename to a notebook.
func (a *App) RenameNotebook(notebookID, name string) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(notebookID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.RenameNotebook(a.requestContext(), workspaceID, id, name))
}

// MoveNotebook reassigns a notebook's parent (empty newParentID moves it
// to the workspace root).
func (a *App) MoveNotebook(notebookID, newParentID string) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(notebookID)
	if err != nil {
		return mapError(err)
	}
	parent, err := parseID(newParentID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.MoveNotebook(a.requestContext(), workspaceID, id, parent))
}

// SetNotebookDeleted sets or clears a notebook's tombstone.
func (a *App) SetNotebookDeleted(notebookID string, deleted bool) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(notebookID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.SetNotebookDeleted(a.requestContext(), workspaceID, id, deleted))
}

// ListNotebooks returns every notebook in the account's workspace,
// including deleted ones.
func (a *App) ListNotebooks() ([]NotebookDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	notebooks, err := acc.ListNotebooks(a.requestContext(), workspaceID)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]NotebookDTO, len(notebooks))
	for i, nb := range notebooks {
		out[i] = notebookDTO(nb)
	}
	return out, nil
}

// --- Tags ---

// TagDTO is the JS-facing shape of a store.Tag.
type TagDTO struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Deleted     bool   `json:"deleted"`
}

func tagDTO(t store.Tag) TagDTO {
	return TagDTO{ID: idString(t.ID), WorkspaceID: idString(t.WorkspaceID), Name: t.Name, Deleted: t.Deleted}
}

// CreateTag creates a new workspace tag.
func (a *App) CreateTag(name string) (TagDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return TagDTO{}, mapError(err)
	}
	tag, err := acc.CreateTag(a.requestContext(), workspaceID, name)
	if err != nil {
		return TagDTO{}, mapError(err)
	}
	return tagDTO(tag), nil
}

// SetTagDeleted sets or clears a tag's tombstone.
func (a *App) SetTagDeleted(tagID string, deleted bool) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(tagID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.SetTagDeleted(a.requestContext(), workspaceID, id, deleted))
}

// ListTags returns every tag in the account's workspace, including
// deleted ones.
func (a *App) ListTags() ([]TagDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	tags, err := acc.ListTags(a.requestContext(), workspaceID)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]TagDTO, len(tags))
	for i, t := range tags {
		out[i] = tagDTO(t)
	}
	return out, nil
}
