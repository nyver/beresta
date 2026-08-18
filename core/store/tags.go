package store

import (
	"context"
	"database/sql"
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
