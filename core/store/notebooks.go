package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

const maxNotebookNameBytes = 256

// syncPlaceholderNamePrefix marks a hidden, tombstoned notebook or tag row
// created by EnsureNotebookPlaceholder or EnsureTagPlaceholder to satisfy a
// foreign key before the real catalog entry has synced in from a peer.
const syncPlaceholderNamePrefix = "sync-pending-"

var (
	// ErrNotFound reports that a referenced record does not exist.
	ErrNotFound = errors.New("store: record not found")
	// ErrWrongWorkspace reports that a referenced record belongs to a
	// different workspace than the operation requires.
	ErrWrongWorkspace = errors.New("store: record belongs to a different workspace")
	// ErrNotebookCycle reports that a requested parent assignment would make
	// a notebook its own ancestor.
	ErrNotebookCycle = errors.New("store: notebook parent would create a cycle")
	// ErrInvalidName reports an empty or oversized display name.
	ErrInvalidName = errors.New("store: invalid name")
)

// Notebook is one row of a workspace's notebook tree. Name, ParentID, and
// Deleted are independent LWW registers ordered by their *Clock field.
type Notebook struct {
	ID           model.ID
	WorkspaceID  model.ID
	ParentID     model.ID // model.Nil means filed at the workspace root
	ParentClock  model.HLC
	Name         string
	NameClock    model.HLC
	Deleted      bool
	DeletedClock model.HLC
	CreatedAt    model.HLC
}

func nullableClockPhysical(set bool, clock model.HLC) any {
	if !set {
		return nil
	}
	return clock.PhysicalMS
}

func nullableClockLogical(set bool, clock model.HLC) any {
	if !set {
		return nil
	}
	return clock.Logical
}

func nullableClockDevice(set bool, clock model.HLC) any {
	if !set {
		return nil
	}
	return clock.DeviceID.Bytes()
}

// CreateNotebook inserts a new notebook. A zero parentID files it at the
// workspace root; a non-zero parentID must reference an existing,
// non-deleted notebook in the same workspace. clock is used for the
// created, name, and parent registers, all written by this one event.
func CreateNotebook(ctx context.Context, exec Executor, workspaceID, parentID model.ID, name string, clock model.HLC) (Notebook, error) {
	if err := validateName(name, maxNotebookNameBytes); err != nil {
		return Notebook{}, err
	}
	if !parentID.IsZero() {
		parent, err := getNotebook(ctx, exec, parentID)
		if err != nil {
			return Notebook{}, err
		}
		if parent.WorkspaceID != workspaceID {
			return Notebook{}, ErrWrongWorkspace
		}
		if parent.Deleted {
			return Notebook{}, ErrNotFound
		}
	}

	id, err := model.NewID()
	if err != nil {
		return Notebook{}, err
	}
	parentColumn := idColumn(parentID)
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO notebooks (id, workspace_id, parent_id, parent_physical_ms, parent_logical, parent_device_id, name, name_physical_ms, name_logical, name_device_id, created_physical_ms, created_logical, created_device_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.Bytes(), workspaceID.Bytes(), parentColumn, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
		name, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
		clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		return Notebook{}, fmt.Errorf("store: insert notebook: %w", err)
	}

	return Notebook{
		ID: id, WorkspaceID: workspaceID, ParentID: parentID, ParentClock: clock,
		Name: name, NameClock: clock, CreatedAt: clock,
	}, nil
}

// UpsertSnapshotNotebook materializes an authenticated notebook catalog row
// from a workspace snapshot. Callers must insert every row with a root parent
// first, then call it again with the real parents after the tree exists.
func UpsertSnapshotNotebook(ctx context.Context, exec Executor, notebook Notebook) error {
	if err := validateName(notebook.Name, maxNotebookNameBytes); err != nil {
		return err
	}
	if notebook.ID.IsZero() || notebook.WorkspaceID.IsZero() {
		return errors.New("store: invalid snapshot notebook")
	}
	if _, err := exec.ExecContext(ctx, `
		INSERT INTO notebooks (
			id, workspace_id, parent_id, parent_physical_ms, parent_logical, parent_device_id,
			name, name_physical_ms, name_logical, name_device_id,
			deleted, deleted_physical_ms, deleted_logical, deleted_device_id,
			created_physical_ms, created_logical, created_device_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			parent_id = excluded.parent_id,
			parent_physical_ms = excluded.parent_physical_ms,
			parent_logical = excluded.parent_logical,
			parent_device_id = excluded.parent_device_id,
			name = excluded.name,
			name_physical_ms = excluded.name_physical_ms,
			name_logical = excluded.name_logical,
			name_device_id = excluded.name_device_id,
			deleted = excluded.deleted,
			deleted_physical_ms = excluded.deleted_physical_ms,
			deleted_logical = excluded.deleted_logical,
			deleted_device_id = excluded.deleted_device_id,
			created_physical_ms = excluded.created_physical_ms,
			created_logical = excluded.created_logical,
			created_device_id = excluded.created_device_id`,
		notebook.ID.Bytes(), notebook.WorkspaceID.Bytes(), idColumn(notebook.ParentID), notebook.ParentClock.PhysicalMS, notebook.ParentClock.Logical, notebook.ParentClock.DeviceID.Bytes(),
		notebook.Name, notebook.NameClock.PhysicalMS, notebook.NameClock.Logical, notebook.NameClock.DeviceID.Bytes(),
		notebook.Deleted, nullableClockPhysical(notebook.Deleted, notebook.DeletedClock), nullableClockLogical(notebook.Deleted, notebook.DeletedClock), nullableClockDevice(notebook.Deleted, notebook.DeletedClock),
		notebook.CreatedAt.PhysicalMS, notebook.CreatedAt.Logical, notebook.CreatedAt.DeviceID.Bytes(),
	); err != nil {
		return fmt.Errorf("store: upsert snapshot notebook: %w", err)
	}
	return nil
}

// EnsureNotebookPlaceholder creates a hidden tombstoned notebook when a
// synchronized note-notebook assignment arrives before the matching notebook
// catalog row (e.g. a newly connected device receiving the "move note"
// operation before the "create notebook" operation). This satisfies notes'
// notebook_id foreign key without exposing a guessed notebook name in the
// user interface, mirroring EnsureTagPlaceholder. It always files the
// placeholder at the workspace root; a later CreateNotebook or snapshot
// replay never touches this row's id, so any real parent arrives as its own
// separate, ordinary update.
func EnsureNotebookPlaceholder(ctx context.Context, exec Executor, workspaceID, notebookID model.ID, clock model.HLC) error {
	if notebookID.IsZero() {
		return errors.New("store: notebook placeholder ID is zero")
	}
	name := syncPlaceholderNamePrefix + hex.EncodeToString(notebookID.Bytes())
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO notebooks (
			id, workspace_id, name, name_physical_ms, name_logical, name_device_id,
			deleted, deleted_physical_ms, deleted_logical, deleted_device_id,
			created_physical_ms, created_logical, created_device_id
		) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		notebookID.Bytes(), workspaceID.Bytes(), name, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
		clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
		clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		return fmt.Errorf("store: insert notebook placeholder: %w", err)
	}
	var storedWorkspaceID []byte
	if err := exec.QueryRowContext(ctx, `SELECT workspace_id FROM notebooks WHERE id = ?`, notebookID.Bytes()).Scan(&storedWorkspaceID); err != nil {
		return fmt.Errorf("store: get notebook placeholder workspace: %w", err)
	}
	storedWorkspace, err := model.ParseID(storedWorkspaceID)
	if err != nil {
		return fmt.Errorf("store: stored notebook placeholder workspace ID: %w", err)
	}
	if storedWorkspace != workspaceID {
		return ErrWrongWorkspace
	}
	return nil
}

// HasPendingSyncPlaceholders reports whether workspaceID still has any hidden
// placeholder notebook, tag, or attachment row (see EnsureNotebookPlaceholder,
// EnsureTagPlaceholder, EnsureAttachmentPlaceholder) that has not yet been
// replaced by its real catalog entry. A device must not author a workspace
// snapshot while this is true: publishing would advertise the placeholder's
// name-less, parent-less stand-in as this workspace's authoritative
// structural state, and since nothing re-triggers a republish once every
// device's local catalog digest stops changing, a newly joined device could
// be left with hidden placeholders indefinitely instead of the real
// notebooks, tags, or attachments.
func HasPendingSyncPlaceholders(ctx context.Context, exec Executor, workspaceID model.ID) (bool, error) {
	var exists int
	if err := exec.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM notebooks WHERE workspace_id = ? AND deleted = 1 AND name LIKE ?
			UNION ALL
			SELECT 1 FROM tags WHERE workspace_id = ? AND deleted = 1 AND name LIKE ?
			UNION ALL
			SELECT 1 FROM attachments WHERE workspace_id = ? AND length(manifest) = 0
		)`,
		workspaceID.Bytes(), syncPlaceholderNamePrefix+"%",
		workspaceID.Bytes(), syncPlaceholderNamePrefix+"%",
		workspaceID.Bytes(),
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("store: check pending sync placeholders: %w", err)
	}
	return exists != 0, nil
}

// RenameNotebook applies an LWW update to a notebook's name. A stale clock
// (older than the currently stored one) is silently superseded, not an
// error; only a missing notebook is.
func RenameNotebook(ctx context.Context, exec Executor, notebookID model.ID, name string, clock model.HLC) error {
	if err := validateName(name, maxNotebookNameBytes); err != nil {
		return err
	}
	query := `UPDATE notebooks SET name = ?, name_physical_ms = ?, name_logical = ?, name_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("name_physical_ms", "name_logical", "name_device_id")
	args := append([]any{name, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), notebookID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: rename notebook: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "notebooks", notebookID)
}

// MoveNotebook reassigns a notebook's parent as an LWW update. A zero
// newParentID moves it to the workspace root. It rejects a move that would
// make notebookID its own ancestor.
func MoveNotebook(ctx context.Context, exec Executor, notebookID, newParentID model.ID, clock model.HLC) error {
	if notebookID == newParentID {
		return ErrNotebookCycle
	}
	notebook, err := getNotebook(ctx, exec, notebookID)
	if err != nil {
		return err
	}
	if !newParentID.IsZero() {
		ancestor := newParentID
		for !ancestor.IsZero() {
			if ancestor == notebookID {
				return ErrNotebookCycle
			}
			next, err := getNotebook(ctx, exec, ancestor)
			if err != nil {
				return err
			}
			if next.WorkspaceID != notebook.WorkspaceID {
				return ErrWrongWorkspace
			}
			ancestor = next.ParentID
		}
	}

	query := `UPDATE notebooks SET parent_id = ?, parent_physical_ms = ?, parent_logical = ?, parent_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("parent_physical_ms", "parent_logical", "parent_device_id")
	args := append([]any{idColumn(newParentID), clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), notebookID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: move notebook: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "notebooks", notebookID)
}

// SetNotebookDeleted applies an LWW update to a notebook's tombstone flag.
// Restoring a notebook is the same call with deleted=false and a later
// clock. Child notebooks and notes are never cascade-affected.
func SetNotebookDeleted(ctx context.Context, exec Executor, notebookID model.ID, deleted bool, clock model.HLC) error {
	query := `UPDATE notebooks SET deleted = ?, deleted_physical_ms = ?, deleted_logical = ?, deleted_device_id = ?
	          WHERE id = ? AND ` + lwwWhereClause("deleted_physical_ms", "deleted_logical", "deleted_device_id")
	args := append([]any{deleted, clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(), notebookID.Bytes()}, lwwArgs(clock)...)
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: set notebook deleted: %w", err)
	}
	return ignoreStaleWrite(ctx, exec, result, "notebooks", notebookID)
}

// ListNotebooks returns every notebook in a workspace, including deleted
// ones; callers filter by Deleted for the tree they need to render.
func ListNotebooks(ctx context.Context, exec Executor, workspaceID model.ID) ([]Notebook, error) {
	rows, err := exec.QueryContext(ctx,
		`SELECT id, workspace_id, parent_id, parent_physical_ms, parent_logical, parent_device_id,
		        name, name_physical_ms, name_logical, name_device_id,
		        deleted, deleted_physical_ms, deleted_logical, deleted_device_id,
		        created_physical_ms, created_logical, created_device_id
		 FROM notebooks WHERE workspace_id = ?`,
		workspaceID.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: list notebooks: %w", err)
	}
	defer rows.Close()

	var notebooks []Notebook
	for rows.Next() {
		notebook, err := scanNotebook(rows)
		if err != nil {
			return nil, err
		}
		notebooks = append(notebooks, notebook)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list notebooks: %w", err)
	}
	return notebooks, nil
}

func getNotebook(ctx context.Context, exec Executor, id model.ID) (Notebook, error) {
	row := exec.QueryRowContext(ctx,
		`SELECT id, workspace_id, parent_id, parent_physical_ms, parent_logical, parent_device_id,
		        name, name_physical_ms, name_logical, name_device_id,
		        deleted, deleted_physical_ms, deleted_logical, deleted_device_id,
		        created_physical_ms, created_logical, created_device_id
		 FROM notebooks WHERE id = ?`,
		id.Bytes(),
	)
	notebook, err := scanNotebook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Notebook{}, ErrNotFound
	}
	return notebook, err
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanNotebook(scanner rowScanner) (Notebook, error) {
	var n Notebook
	var idBytes, workspaceIDBytes, parentIDBytes, parentDeviceID, nameDeviceID, deletedDeviceID, createdDeviceID []byte
	var parentPhysicalMS, parentLogical, deletedPhysicalMS, deletedLogical sql.NullInt64
	if err := scanner.Scan(
		&idBytes, &workspaceIDBytes, &parentIDBytes, &parentPhysicalMS, &parentLogical, &parentDeviceID,
		&n.Name, &n.NameClock.PhysicalMS, &n.NameClock.Logical, &nameDeviceID,
		&n.Deleted, &deletedPhysicalMS, &deletedLogical, &deletedDeviceID,
		&n.CreatedAt.PhysicalMS, &n.CreatedAt.Logical, &createdDeviceID,
	); err != nil {
		return Notebook{}, fmt.Errorf("store: scan notebook: %w", err)
	}
	id, err := model.ParseID(idBytes)
	if err != nil {
		return Notebook{}, fmt.Errorf("store: stored notebook ID: %w", err)
	}
	workspaceID, err := model.ParseID(workspaceIDBytes)
	if err != nil {
		return Notebook{}, fmt.Errorf("store: stored notebook workspace ID: %w", err)
	}
	n.ID = id
	n.WorkspaceID = workspaceID
	if len(parentIDBytes) > 0 {
		parentID, err := model.ParseID(parentIDBytes)
		if err != nil {
			return Notebook{}, fmt.Errorf("store: stored notebook parent ID: %w", err)
		}
		n.ParentID = parentID
	}
	n.ParentClock = model.HLC{PhysicalMS: uint64(parentPhysicalMS.Int64), Logical: uint32(parentLogical.Int64)}
	if deviceID, err := model.ParseID(parentDeviceID); err == nil {
		n.ParentClock.DeviceID = deviceID
	}
	if deviceID, err := model.ParseID(nameDeviceID); err == nil {
		n.NameClock.DeviceID = deviceID
	}
	if deviceID, err := model.ParseID(createdDeviceID); err == nil {
		n.CreatedAt.DeviceID = deviceID
	}
	if n.Deleted {
		n.DeletedClock = model.HLC{PhysicalMS: uint64(deletedPhysicalMS.Int64), Logical: uint32(deletedLogical.Int64)}
		if deviceID, err := model.ParseID(deletedDeviceID); err == nil {
			n.DeletedClock.DeviceID = deviceID
		}
	}
	return n, nil
}

func validateName(name string, maxBytes int) error {
	if len(name) == 0 || len(name) > maxBytes {
		return ErrInvalidName
	}
	return nil
}

// idColumn returns the driver value to bind for a nullable ID column: nil
// for the zero ID, or its raw bytes.
func idColumn(id model.ID) any {
	if id.IsZero() {
		return nil
	}
	return id.Bytes()
}

// ignoreStaleWrite treats a zero-row-affected conditional LWW update as
// success when the target row exists (the incoming clock lost to a value
// already applied) and as ErrNotFound only when the row does not exist.
// table must be a fixed internal literal, never caller-supplied input.
func ignoreStaleWrite(ctx context.Context, exec Executor, result sql.Result, table string, id model.ID) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: check rows affected: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var exists int
	err = exec.QueryRowContext(ctx, fmt.Sprintf(`SELECT 1 FROM %s WHERE id = ?`, table), id.Bytes()).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: check row existence: %w", err)
	}
	return nil
}
