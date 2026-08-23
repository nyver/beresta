package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

// InsertReplicatedNote materializes the first body operation for a note whose
// UUID was generated on another device. It never invents a replacement ID;
// later operations therefore address the same object on every replica.
func InsertReplicatedNote(ctx context.Context, exec Executor, noteID, workspaceID model.ID, title string, clock model.HLC) (model.Note, error) {
	if err := noteID.Validate(); err != nil || len(title) > model.MaxNoteTitleBytes || clock.IsZero() {
		return model.Note{}, fmt.Errorf("%w: replicated note", model.ErrInvalidNote)
	}
	result, err := exec.ExecContext(ctx,
		`INSERT INTO notes (id, workspace_id, title, title_physical_ms, title_logical, title_device_id,
		                     created_physical_ms, created_logical, created_device_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		noteID.Bytes(), workspaceID.Bytes(), title, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
		clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes())
	if err != nil {
		return model.Note{}, fmt.Errorf("store: insert replicated note: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		note, err := GetNote(ctx, exec, noteID)
		if err != nil {
			return model.Note{}, err
		}
		if note.WorkspaceID != workspaceID {
			return model.Note{}, ErrWrongWorkspace
		}
		return note, nil
	}
	return model.Note{ID: noteID, WorkspaceID: workspaceID, Title: model.LWW[string]{Value: title, Clock: clock}, CreatedAt: clock}, nil
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

// DeleteNoteCompletely permanently erases a note and every row that
// belongs to it alone: CRDT state/update log, revisions, tag/attachment
// membership, and its FTS entry. It reconciles the orphan status of every
// attachment the note referenced (see SetNoteAttachment), since removing
// its note_attachments rows can newly orphan them. Callers must only use
// this on a note whose tombstone (SetNoteDeleted) has already passed the
// minimum retention window (see specs/sync-engine.md, "Tombstones and
// garbage collection"); it is not itself a synchronized operation.
func DeleteNoteCompletely(ctx context.Context, exec Executor, noteID model.ID, nowUnixMS int64) error {
	blobIDs, err := NoteAttachmentBlobIDs(ctx, exec, noteID)
	if err != nil {
		return err
	}
	if _, err := exec.ExecContext(ctx, `DELETE FROM note_attachments WHERE note_id = ?`, noteID.Bytes()); err != nil {
		return fmt.Errorf("store: delete note attachment references: %w", err)
	}
	for _, blobID := range blobIDs {
		if err := reconcileAttachmentOrphan(ctx, exec, blobID, nowUnixMS); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		`DELETE FROM note_tags WHERE note_id = ?`,
		`DELETE FROM revisions WHERE note_id = ?`,
		`DELETE FROM crdt_updates WHERE note_id = ?`,
		`DELETE FROM crdt_states WHERE note_id = ?`,
		`DELETE FROM notes_fts WHERE note_id = ?`,
		`DELETE FROM notes WHERE id = ?`,
	} {
		if _, err := exec.ExecContext(ctx, stmt, noteID.Bytes()); err != nil {
			return fmt.Errorf("store: delete note completely: %w", err)
		}
	}
	return nil
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

// notePreviewRunes caps how much of a note's plaintext body is kept as its
// list-row preview: the workspace ceiling is 20,000 notes (design.md), and
// NoteListMetaByWorkspace returns every one of them at once, so this must
// stay small enough that the JS bridge payload cannot balloon on a
// body-heavy workspace.
const notePreviewRunes = 160

// NoteListMeta is one note's list-row display metadata: a plaintext preview
// snippet and a "last modified" timestamp that is not tracked anywhere on
// model.Note itself (LWW registers only cover title/notebook/flags/deleted;
// body edits only ever touch crdt_states).
type NoteListMeta struct {
	UpdatedUnixMS int64
	Preview       string
}

// NoteListMetaByWorkspace returns every note's list-row metadata across an
// entire workspace in one query, keyed by note ID, mirroring
// NoteTagIDsByWorkspace's shape and reasoning (tags.go): a note list or
// search result page needs this for many notes at once, so one indexed join
// beats issuing a query per note. UpdatedUnixMS is the latest of the note's
// own LWW clocks (title/flags/created) and its CRDT body's last commit
// (crdt_states.updated_unix_ms, NULL for a note whose body was never
// edited), so a title-only rename still bumps "last modified" even without
// a body commit. Preview comes from notes_fts.body - the note's plaintext
// canonical Markdown body, already held there unencrypted for FTS5 matching
// (see ReplaceNoteFTS's doc comment); the whole database file is itself
// SQLCipher-encrypted at rest, which is the trust boundary Search and
// SearchByTag already rely on for the same table.
func NoteListMetaByWorkspace(ctx context.Context, exec Executor, workspaceID model.ID) (map[model.ID]NoteListMeta, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT n.id,
		       MAX(n.created_physical_ms, COALESCE(n.title_physical_ms, 0), COALESCE(n.flags_physical_ms, 0), COALESCE(cs.updated_unix_ms, 0)),
		       f.body
		FROM notes n
		LEFT JOIN crdt_states cs ON cs.note_id = n.id
		LEFT JOIN notes_fts f ON f.note_id = n.id
		WHERE n.workspace_id = ?`, workspaceID.Bytes())
	if err != nil {
		return nil, fmt.Errorf("store: list note metadata: %w", err)
	}
	defer rows.Close()

	result := make(map[model.ID]NoteListMeta)
	for rows.Next() {
		var rawID []byte
		var updatedMS int64
		var body sql.NullString
		if err := rows.Scan(&rawID, &updatedMS, &body); err != nil {
			return nil, fmt.Errorf("store: scan note metadata: %w", err)
		}
		id, err := model.ParseID(rawID)
		if err != nil {
			return nil, fmt.Errorf("store: stored note metadata ID: %w", err)
		}
		result[id] = NoteListMeta{UpdatedUnixMS: updatedMS, Preview: notePreview(body.String)}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list note metadata: %w", err)
	}
	return result, nil
}

// notePreview collapses a note body's whitespace/newlines into single spaces
// and truncates it to notePreviewRunes, so a note list row gets a short
// plain-text snippet instead of raw Markdown line breaks.
func notePreview(body string) string {
	collapsed := strings.Join(strings.Fields(body), " ")
	runes := []rune(collapsed)
	if len(runes) <= notePreviewRunes {
		return collapsed
	}
	return string(runes[:notePreviewRunes])
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
