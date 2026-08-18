package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// CreateNote inserts a new note's metadata row. clock is used for the
// created, title, and (when notebookID is non-zero) notebook registers, all
// written by this one event. The note's rich-text CRDT body is a separate
// concern handled by the sync layer.
func CreateNote(ctx context.Context, exec Executor, workspaceID, notebookID model.ID, title string, clock model.HLC) (model.Note, error) {
	note := model.Note{
		WorkspaceID: workspaceID,
		NotebookID:  model.LWW[model.ID]{Value: notebookID, Clock: clock},
		Title:       model.LWW[string]{Value: title, Clock: clock},
		CreatedAt:   clock,
	}
	id, err := model.NewID()
	if err != nil {
		return model.Note{}, err
	}
	note.ID = id
	if err := note.Validate(); err != nil {
		return model.Note{}, err
	}

	notebookColumn := idColumn(notebookID)
	var notebookPhysical, notebookLogical any
	var notebookDevice any
	if !notebookID.IsZero() {
		notebookPhysical, notebookLogical, notebookDevice = clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes()
	}
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO notes (id, workspace_id, notebook_id, notebook_physical_ms, notebook_logical, notebook_device_id,
		                     title, title_physical_ms, title_logical, title_device_id,
		                     created_physical_ms, created_logical, created_device_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.Bytes(), workspaceID.Bytes(), notebookColumn, notebookPhysical, notebookLogical, notebookDevice,
		title, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
		clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		return model.Note{}, fmt.Errorf("store: insert note: %w", err)
	}
	return note, nil
}

// SetNoteTitle applies an LWW update to a note's title. A stale clock is
// silently superseded, not an error; only a missing note is.
func SetNoteTitle(ctx context.Context, exec Executor, noteID model.ID, title string, clock model.HLC) error {
	if len(title) > model.MaxNoteTitleBytes {
		return fmt.Errorf("%w: title", model.ErrInvalidNote)
	}
	query := `UPDATE notes SET title = ?, title_physical_ms = ?, title_logical = ?, title_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("title_physical_ms", "title_logical", "title_device_id")
	args := append([]any{title, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), noteID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: set note title: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "notes", noteID)
}

// SetNoteNotebook applies an LWW update to a note's notebook assignment. A
// zero notebookID files the note at the workspace root.
func SetNoteNotebook(ctx context.Context, exec Executor, noteID, notebookID model.ID, clock model.HLC) error {
	query := `UPDATE notes SET notebook_id = ?, notebook_physical_ms = ?, notebook_logical = ?, notebook_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("notebook_physical_ms", "notebook_logical", "notebook_device_id")
	args := append([]any{idColumn(notebookID), clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), noteID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: set note notebook: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "notes", noteID)
}

// SetNoteFlags applies an LWW update to a note's flag bitmask.
func SetNoteFlags(ctx context.Context, exec Executor, noteID model.ID, flags model.NoteFlags, clock model.HLC) error {
	query := `UPDATE notes SET flags = ?, flags_physical_ms = ?, flags_logical = ?, flags_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("flags_physical_ms", "flags_logical", "flags_device_id")
	args := append([]any{flags, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), noteID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: set note flags: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "notes", noteID)
}

// SetNoteDeleted applies an LWW update to a note's tombstone flag. Restoring
// a note is the same call with deleted=false and a later clock; the note's
// revision history is never erased by this call.
func SetNoteDeleted(ctx context.Context, exec Executor, noteID model.ID, deleted bool, clock model.HLC) error {
	query := `UPDATE notes SET deleted = ?, deleted_physical_ms = ?, deleted_logical = ?, deleted_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("deleted_physical_ms", "deleted_logical", "deleted_device_id")
	args := append([]any{deleted, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), noteID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: set note deleted: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "notes", noteID)
}

// GetNote returns one note's metadata row.
func GetNote(ctx context.Context, exec Executor, noteID model.ID) (model.Note, error) {
	row := exec.QueryRowContext(ctx, noteSelectColumns+` FROM notes WHERE id = ?`, noteID.Bytes())
	note, err := scanNote(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Note{}, ErrNotFound
	}
	return note, err
}

// ListNotes returns every note in a workspace, including deleted ones;
// callers filter by Deleted.Value for the views they need to render.
func ListNotes(ctx context.Context, exec Executor, workspaceID model.ID) ([]model.Note, error) {
	rows, err := exec.QueryContext(ctx, noteSelectColumns+` FROM notes WHERE workspace_id = ?`, workspaceID.Bytes())
	if err != nil {
		return nil, fmt.Errorf("store: list notes: %w", err)
	}
	defer rows.Close()

	var notes []model.Note
	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list notes: %w", err)
	}
	return notes, nil
}

const noteSelectColumns = `SELECT id, workspace_id, notebook_id, notebook_physical_ms, notebook_logical, notebook_device_id,
	       title, title_physical_ms, title_logical, title_device_id,
	       flags, flags_physical_ms, flags_logical, flags_device_id,
	       deleted, deleted_physical_ms, deleted_logical, deleted_device_id,
	       created_physical_ms, created_logical, created_device_id`

// scanNote scans one note row. extra, when given, is appended to the Scan
// call so a caller selecting additional trailing columns (e.g. search's bm25
// rank) can read them in the same pass without duplicating the note field
// list.
func scanNote(scanner rowScanner, extra ...any) (model.Note, error) {
	var n model.Note
	var idBytes, workspaceIDBytes, notebookIDBytes, notebookDeviceID []byte
	var titleDeviceID, flagsDeviceID, deletedDeviceID, createdDeviceID []byte
	var notebookPhysical, notebookLogical, flagsPhysical, flagsLogical, deletedPhysical, deletedLogical sql.NullInt64
	dest := []any{
		&idBytes, &workspaceIDBytes, &notebookIDBytes, &notebookPhysical, &notebookLogical, &notebookDeviceID,
		&n.Title.Value, &n.Title.Clock.PhysicalMS, &n.Title.Clock.Logical, &titleDeviceID,
		&n.Flags.Value, &flagsPhysical, &flagsLogical, &flagsDeviceID,
		&n.Deleted.Value, &deletedPhysical, &deletedLogical, &deletedDeviceID,
		&n.CreatedAt.PhysicalMS, &n.CreatedAt.Logical, &createdDeviceID,
	}
	if err := scanner.Scan(append(dest, extra...)...); err != nil {
		return model.Note{}, fmt.Errorf("store: scan note: %w", err)
	}

	id, err := model.ParseID(idBytes)
	if err != nil {
		return model.Note{}, fmt.Errorf("store: stored note ID: %w", err)
	}
	workspaceID, err := model.ParseID(workspaceIDBytes)
	if err != nil {
		return model.Note{}, fmt.Errorf("store: stored note workspace ID: %w", err)
	}
	n.ID, n.WorkspaceID = id, workspaceID

	if len(notebookIDBytes) > 0 {
		notebookID, err := model.ParseID(notebookIDBytes)
		if err != nil {
			return model.Note{}, fmt.Errorf("store: stored note notebook ID: %w", err)
		}
		n.NotebookID.Value = notebookID
	}
	n.NotebookID.Clock = model.HLC{PhysicalMS: uint64(notebookPhysical.Int64), Logical: uint32(notebookLogical.Int64)}
	if deviceID, err := model.ParseID(notebookDeviceID); err == nil {
		n.NotebookID.Clock.DeviceID = deviceID
	}
	if deviceID, err := model.ParseID(titleDeviceID); err == nil {
		n.Title.Clock.DeviceID = deviceID
	}
	n.Flags.Clock = model.HLC{PhysicalMS: uint64(flagsPhysical.Int64), Logical: uint32(flagsLogical.Int64)}
	if deviceID, err := model.ParseID(flagsDeviceID); err == nil {
		n.Flags.Clock.DeviceID = deviceID
	}
	n.Deleted.Clock = model.HLC{PhysicalMS: uint64(deletedPhysical.Int64), Logical: uint32(deletedLogical.Int64)}
	if deviceID, err := model.ParseID(deletedDeviceID); err == nil {
		n.Deleted.Clock.DeviceID = deviceID
	}
	if deviceID, err := model.ParseID(createdDeviceID); err == nil {
		n.CreatedAt.DeviceID = deviceID
	}
	return n, nil
}
