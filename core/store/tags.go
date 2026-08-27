package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

const maxTagNameBytes = 128

// Tag is one workspace tag definition.
type Tag struct {
	ID           model.ID
	WorkspaceID  model.ID
	Name         string
	CreatedAt    model.HLC
	Deleted      bool
	DeletedClock model.HLC
}

// CreateTag inserts a new tag. Tag names are unique per workspace.
func CreateTag(ctx context.Context, exec Executor, workspaceID model.ID, name string, clock model.HLC) (Tag, error) {
	if err := validateName(name, maxTagNameBytes); err != nil {
		return Tag{}, err
	}
	id, err := model.NewID()
	if err != nil {
		return Tag{}, err
	}
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO tags (id, workspace_id, name, created_physical_ms, created_logical, created_device_id) VALUES (?, ?, ?, ?, ?, ?)`,
		id.Bytes(), workspaceID.Bytes(), name, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		return Tag{}, fmt.Errorf("store: insert tag: %w", err)
	}
	return Tag{ID: id, WorkspaceID: workspaceID, Name: name, CreatedAt: clock}, nil
}

// UpsertSnapshotTag materializes an authenticated tag catalog row from a
// workspace snapshot, replacing a sync-created placeholder with its real
// name and deletion state when necessary.
func UpsertSnapshotTag(ctx context.Context, exec Executor, tag Tag) error {
	if err := validateName(tag.Name, maxTagNameBytes); err != nil {
		return err
	}
	if tag.ID.IsZero() || tag.WorkspaceID.IsZero() {
		return errors.New("store: invalid snapshot tag")
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO tags (
			id, workspace_id, name, created_physical_ms, created_logical, created_device_id,
			deleted, deleted_physical_ms, deleted_logical, deleted_device_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			name = excluded.name,
			created_physical_ms = excluded.created_physical_ms,
			created_logical = excluded.created_logical,
			created_device_id = excluded.created_device_id,
			deleted = excluded.deleted,
			deleted_physical_ms = excluded.deleted_physical_ms,
			deleted_logical = excluded.deleted_logical,
			deleted_device_id = excluded.deleted_device_id`,
		tag.ID.Bytes(), tag.WorkspaceID.Bytes(), tag.Name, tag.CreatedAt.PhysicalMS, tag.CreatedAt.Logical, tag.CreatedAt.DeviceID.Bytes(),
		tag.Deleted, nullableClockPhysical(tag.Deleted, tag.DeletedClock), nullableClockLogical(tag.Deleted, tag.DeletedClock), nullableClockDevice(tag.Deleted, tag.DeletedClock),
	); err != nil {
		return fmt.Errorf("store: upsert snapshot tag: %w", err)
	}
	return nil
}

// EnsureTagPlaceholder creates a hidden tombstoned tag when a synchronized
// note-tag operation arrives before the matching tag catalog row. This keeps
// the membership register durable without exposing a guessed tag name in the
// user interface.
func EnsureTagPlaceholder(ctx context.Context, exec Executor, workspaceID, tagID model.ID, clock model.HLC) error {
	if tagID.IsZero() {
		return errors.New("store: tag placeholder ID is zero")
	}
	name := syncPlaceholderNamePrefix + hex.EncodeToString(tagID.Bytes())
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO tags (
			id, workspace_id, name, created_physical_ms, created_logical, created_device_id,
			deleted, deleted_physical_ms, deleted_logical, deleted_device_id
		) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		tagID.Bytes(), workspaceID.Bytes(), name, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
		clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		return fmt.Errorf("store: insert tag placeholder: %w", err)
	}
	var storedWorkspaceID []byte
	if err := exec.QueryRowContext(ctx, `SELECT workspace_id FROM tags WHERE id = ?`, tagID.Bytes()).Scan(&storedWorkspaceID); err != nil {
		return fmt.Errorf("store: get tag placeholder workspace: %w", err)
	}
	storedWorkspace, err := model.ParseID(storedWorkspaceID)
	if err != nil {
		return fmt.Errorf("store: stored tag placeholder workspace ID: %w", err)
	}
	if storedWorkspace != workspaceID {
		return ErrWrongWorkspace
	}
	return nil
}

// SetTagDeleted applies an LWW update to a tag's tombstone flag. Existing
// note_tags rows referencing it are left as-is. A stale clock is silently
// superseded, not an error; only a missing tag is.
func SetTagDeleted(ctx context.Context, exec Executor, tagID model.ID, deleted bool, clock model.HLC) error {
	query := `UPDATE tags SET deleted = ?, deleted_physical_ms = ?, deleted_logical = ?, deleted_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("deleted_physical_ms", "deleted_logical", "deleted_device_id")
	args := append([]any{deleted, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), tagID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: set tag deleted: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "tags", tagID)
}

// ListTags returns every tag in a workspace, including deleted ones.
func ListTags(ctx context.Context, exec Executor, workspaceID model.ID) ([]Tag, error) {
	rows, err := exec.QueryContext(ctx,
		`SELECT id, workspace_id, name, created_physical_ms, created_logical, created_device_id, deleted, deleted_physical_ms, deleted_logical, deleted_device_id
		 FROM tags WHERE workspace_id = ?`,
		workspaceID.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: list tags: %w", err)
	}
	defer rows.Close()

	var tags []Tag
	for rows.Next() {
		var t Tag
		var idBytes, workspaceIDBytes, createdDeviceID, deletedDeviceID []byte
		var deletedPhysicalMS, deletedLogical sql.NullInt64
		if err := rows.Scan(&idBytes, &workspaceIDBytes, &t.Name, &t.CreatedAt.PhysicalMS, &t.CreatedAt.Logical, &createdDeviceID, &t.Deleted, &deletedPhysicalMS, &deletedLogical, &deletedDeviceID); err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		id, err := model.ParseID(idBytes)
		if err != nil {
			return nil, fmt.Errorf("store: stored tag ID: %w", err)
		}
		wsID, err := model.ParseID(workspaceIDBytes)
		if err != nil {
			return nil, fmt.Errorf("store: stored tag workspace ID: %w", err)
		}
		t.ID, t.WorkspaceID = id, wsID
		if deviceID, err := model.ParseID(createdDeviceID); err == nil {
			t.CreatedAt.DeviceID = deviceID
		}
		if t.Deleted {
			t.DeletedClock = model.HLC{PhysicalMS: uint64(deletedPhysicalMS.Int64), Logical: uint32(deletedLogical.Int64)}
			if deviceID, err := model.ParseID(deletedDeviceID); err == nil {
				t.DeletedClock.DeviceID = deviceID
			}
		}
		tags = append(tags, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list tags: %w", err)
	}
	return tags, nil
}

// GetTagByName returns the one non-deleted tag with the given name in a
// workspace. Deleted tags are excluded so a `tag:` search filter (or a
// re-run saved search) never silently matches a name someone has since
// reused for a different tag; it reports ErrNotFound instead.
func GetTagByName(ctx context.Context, exec Executor, workspaceID model.ID, name string) (Tag, error) {
	row := exec.QueryRowContext(ctx,
		`SELECT id, workspace_id, name, created_physical_ms, created_logical, created_device_id, deleted, deleted_physical_ms, deleted_logical, deleted_device_id
		 FROM tags WHERE workspace_id = ? AND name = ? AND deleted = 0`,
		workspaceID.Bytes(), name,
	)
	var t Tag
	var idBytes, workspaceIDBytes, createdDeviceID, deletedDeviceID []byte
	var deletedPhysicalMS, deletedLogical sql.NullInt64
	if err := row.Scan(&idBytes, &workspaceIDBytes, &t.Name, &t.CreatedAt.PhysicalMS, &t.CreatedAt.Logical, &createdDeviceID, &t.Deleted, &deletedPhysicalMS, &deletedLogical, &deletedDeviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Tag{}, ErrNotFound
		}
		return Tag{}, fmt.Errorf("store: get tag by name: %w", err)
	}
	id, err := model.ParseID(idBytes)
	if err != nil {
		return Tag{}, fmt.Errorf("store: stored tag ID: %w", err)
	}
	wsID, err := model.ParseID(workspaceIDBytes)
	if err != nil {
		return Tag{}, fmt.Errorf("store: stored tag workspace ID: %w", err)
	}
	t.ID, t.WorkspaceID = id, wsID
	if deviceID, err := model.ParseID(createdDeviceID); err == nil {
		t.CreatedAt.DeviceID = deviceID
	}
	return t, nil
}

// SetNoteTag adds or removes a note's membership in one tag as an
// independent per-pair LWW register, so concurrent edits to unrelated tags
// on the same note never replace the whole tag set.
func SetNoteTag(ctx context.Context, exec Executor, noteID, tagID model.ID, present bool, clock model.HLC) error {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO note_tags (note_id, tag_id, present, physical_ms, logical, device_id) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(note_id, tag_id) DO UPDATE SET present = excluded.present, physical_ms = excluded.physical_ms, logical = excluded.logical, device_id = excluded.device_id
		 WHERE excluded.physical_ms > note_tags.physical_ms
		    OR (excluded.physical_ms = note_tags.physical_ms AND excluded.logical > note_tags.logical)
		    OR (excluded.physical_ms = note_tags.physical_ms AND excluded.logical = note_tags.logical AND excluded.device_id > note_tags.device_id)`,
		noteID.Bytes(), tagID.Bytes(), present, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		return fmt.Errorf("store: set note tag: %w", err)
	}
	return nil
}

// NoteTagIDs returns the IDs of every tag currently present on a note.
func NoteTagIDs(ctx context.Context, exec Executor, noteID model.ID) ([]model.ID, error) {
	rows, err := exec.QueryContext(ctx, `SELECT tag_id FROM note_tags WHERE note_id = ? AND present = 1`, noteID.Bytes())
	if err != nil {
		return nil, fmt.Errorf("store: list note tags: %w", err)
	}
	defer rows.Close()

	var tagIDs []model.ID
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("store: scan note tag: %w", err)
		}
		id, err := model.ParseID(raw)
		if err != nil {
			return nil, fmt.Errorf("store: stored note tag ID: %w", err)
		}
		tagIDs = append(tagIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list note tags: %w", err)
	}
	return tagIDs, nil
}

// NoteTagIDsByWorkspace returns every note's present tag IDs across an
// entire workspace in one query, keyed by note ID. A note with no tags is
// simply absent from the result map. Callers that need tag IDs for many
// notes at once (a note list, a search result page) should use this instead
// of NoteTagIDs in a loop, which would issue one query per note.
func NoteTagIDsByWorkspace(ctx context.Context, exec Executor, workspaceID model.ID) (map[model.ID][]model.ID, error) {
	rows, err := exec.QueryContext(ctx, `SELECT nt.note_id, nt.tag_id FROM note_tags nt
		JOIN notes n ON n.id = nt.note_id
		WHERE nt.present = 1 AND n.workspace_id = ?`, workspaceID.Bytes())
	if err != nil {
		return nil, fmt.Errorf("store: list workspace note tags: %w", err)
	}
	defer rows.Close()

	result := make(map[model.ID][]model.ID)
	for rows.Next() {
		var rawNote, rawTag []byte
		if err := rows.Scan(&rawNote, &rawTag); err != nil {
			return nil, fmt.Errorf("store: scan workspace note tag: %w", err)
		}
		noteID, err := model.ParseID(rawNote)
		if err != nil {
			return nil, fmt.Errorf("store: stored note tag note ID: %w", err)
		}
		tagID, err := model.ParseID(rawTag)
		if err != nil {
			return nil, fmt.Errorf("store: stored note tag ID: %w", err)
		}
		result[noteID] = append(result[noteID], tagID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list workspace note tags: %w", err)
	}
	return result, nil
}
