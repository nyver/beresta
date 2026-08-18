package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func repoTestDeviceID(t testing.TB, seed byte) model.ID {
	t.Helper()
	var raw [16]byte
	for i := range raw {
		raw[i] = seed
	}
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	id, err := model.ParseID(raw[:])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func repoClock(t testing.TB, physicalMS uint64, logical uint32, deviceSeed byte) model.HLC {
	t.Helper()
	return model.HLC{PhysicalMS: physicalMS, Logical: logical, DeviceID: repoTestDeviceID(t, deviceSeed)}
}

func seedWorkspace(t testing.TB, db *sql.DB) model.ID {
	t.Helper()
	id, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	clock := repoClock(t, 1, 0, 0x01)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO workspaces (id, created_physical_ms, created_logical, created_device_id) VALUES (?, ?, ?, ?)`,
		id.Bytes(), clock.PhysicalMS, clock.Logical, clock.DeviceID.Bytes(),
	); err != nil {
		t.Fatal(err)
	}
	return id
}

func repoTestDB(t testing.TB) *sql.DB {
	t.Helper()
	db := openTestDB(t)
	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCreateAndRenameNotebook(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	notebook, err := CreateNotebook(ctx, db, workspaceID, model.Nil, "Personal", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatalf("CreateNotebook() error = %v", err)
	}
	if err := RenameNotebook(ctx, db, notebook.ID, "Work", repoClock(t, 20, 0, 0x02)); err != nil {
		t.Fatalf("RenameNotebook() error = %v", err)
	}
	got, err := getNotebook(ctx, db, notebook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Work" {
		t.Fatalf("Name = %q, want %q", got.Name, "Work")
	}
}

func TestRenameNotebookIgnoresStaleClock(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	notebook, err := CreateNotebook(ctx, db, workspaceID, model.Nil, "Personal", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	// A stale (earlier) clock must lose without erroring.
	if err := RenameNotebook(ctx, db, notebook.ID, "Stale Name", repoClock(t, 5, 0, 0x02)); err != nil {
		t.Fatalf("RenameNotebook() with a stale clock error = %v, want nil", err)
	}
	got, err := getNotebook(ctx, db, notebook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Personal" {
		t.Fatalf("Name = %q, want the original name to survive a stale write", got.Name)
	}
}

func TestRenameNotebookDeviceIDBreaksTie(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	notebook, err := CreateNotebook(ctx, db, workspaceID, model.Nil, "Personal", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	// Equal (physical_ms, logical) from a higher device ID must win.
	if err := RenameNotebook(ctx, db, notebook.ID, "From Low Device", repoClock(t, 10, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	if err := RenameNotebook(ctx, db, notebook.ID, "From High Device", repoClock(t, 10, 0, 0x09)); err != nil {
		t.Fatal(err)
	}
	got, err := getNotebook(ctx, db, notebook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "From High Device" {
		t.Fatalf("Name = %q, want the higher device ID's write to win the tie", got.Name)
	}
}

func TestCreateNotebookRejectsCrossWorkspaceParent(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceA := seedWorkspace(t, db)
	workspaceB := seedWorkspace(t, db)

	parent, err := CreateNotebook(ctx, db, workspaceA, model.Nil, "Parent", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNotebook(ctx, db, workspaceB, parent.ID, "Child", repoClock(t, 11, 0, 0x02)); !errors.Is(err, ErrWrongWorkspace) {
		t.Fatalf("CreateNotebook() error = %v, want ErrWrongWorkspace", err)
	}
}

func TestMoveNotebookRejectsCycle(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	root, err := CreateNotebook(ctx, db, workspaceID, model.Nil, "Root", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	child, err := CreateNotebook(ctx, db, workspaceID, root.ID, "Child", repoClock(t, 11, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := CreateNotebook(ctx, db, workspaceID, child.ID, "Grandchild", repoClock(t, 12, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	if err := MoveNotebook(ctx, db, root.ID, grandchild.ID, repoClock(t, 20, 0, 0x02)); !errors.Is(err, ErrNotebookCycle) {
		t.Fatalf("MoveNotebook() error = %v, want ErrNotebookCycle", err)
	}
	if err := MoveNotebook(ctx, db, root.ID, root.ID, repoClock(t, 20, 0, 0x02)); !errors.Is(err, ErrNotebookCycle) {
		t.Fatalf("MoveNotebook() self-parent error = %v, want ErrNotebookCycle", err)
	}
	// A legitimate move must still succeed.
	if err := MoveNotebook(ctx, db, grandchild.ID, model.Nil, repoClock(t, 21, 0, 0x02)); err != nil {
		t.Fatalf("MoveNotebook() to root error = %v", err)
	}
}

func TestNotebookTombstoneAndRestore(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	notebook, err := CreateNotebook(ctx, db, workspaceID, model.Nil, "Personal", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetNotebookDeleted(ctx, db, notebook.ID, true, repoClock(t, 20, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	got, err := getNotebook(ctx, db, notebook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Deleted {
		t.Fatal("notebook was not marked deleted")
	}
	if err := SetNotebookDeleted(ctx, db, notebook.ID, false, repoClock(t, 30, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	got, err = getNotebook(ctx, db, notebook.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Deleted {
		t.Fatal("notebook was not restored")
	}
}

func TestDeleteMissingNotebookReturnsErrNotFound(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	missing, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := SetNotebookDeleted(ctx, db, missing, true, repoClock(t, 1, 0, 0x02)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetNotebookDeleted() on a missing notebook error = %v, want ErrNotFound", err)
	}
}

func TestCreateTagAndUniqueName(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	if _, err := CreateTag(ctx, db, workspaceID, "urgent", repoClock(t, 10, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateTag(ctx, db, workspaceID, "urgent", repoClock(t, 11, 0, 0x02)); err == nil {
		t.Fatal("expected a unique-constraint error for a duplicate tag name")
	}
}

func TestSetNoteTagIsIndependentPerTag(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "Groceries", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	urgent, err := CreateTag(ctx, db, workspaceID, "urgent", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	home, err := CreateTag(ctx, db, workspaceID, "home", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetNoteTag(ctx, db, note.ID, urgent.ID, true, repoClock(t, 20, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	if err := SetNoteTag(ctx, db, note.ID, home.ID, true, repoClock(t, 21, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	// A concurrent removal of "urgent" from a different device must not
	// affect the unrelated "home" tag membership.
	if err := SetNoteTag(ctx, db, note.ID, urgent.ID, false, repoClock(t, 22, 0, 0x03)); err != nil {
		t.Fatal(err)
	}

	tagIDs, err := NoteTagIDs(ctx, db, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tagIDs) != 1 || tagIDs[0] != home.ID {
		t.Fatalf("NoteTagIDs() = %v, want only %s", tagIDs, home.ID)
	}
}

func TestCreateAndUpdateNoteMetadata(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	notebook, err := CreateNotebook(ctx, db, workspaceID, model.Nil, "Personal", repoClock(t, 5, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "Untitled", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if err := SetNoteTitle(ctx, db, note.ID, "Shopping list", repoClock(t, 20, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	if err := SetNoteNotebook(ctx, db, note.ID, notebook.ID, repoClock(t, 21, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	if err := SetNoteFlags(ctx, db, note.ID, model.NoteFlagPinned, repoClock(t, 22, 0, 0x02)); err != nil {
		t.Fatal(err)
	}

	got, err := GetNote(ctx, db, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title.Value != "Shopping list" {
		t.Fatalf("Title = %q, want %q", got.Title.Value, "Shopping list")
	}
	if got.NotebookID.Value != notebook.ID {
		t.Fatalf("NotebookID = %s, want %s", got.NotebookID.Value, notebook.ID)
	}
	if got.Flags.Value != model.NoteFlagPinned {
		t.Fatalf("Flags = %d, want %d", got.Flags.Value, model.NoteFlagPinned)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("round-tripped note fails model validation: %v", err)
	}
}

func TestNoteTombstoneAndRestore(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "Note", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetNoteDeleted(ctx, db, note.ID, true, repoClock(t, 20, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	got, err := GetNote(ctx, db, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Deleted.Value {
		t.Fatal("note was not marked deleted")
	}
	// An older delete-related write arriving later (e.g. offline device
	// returning) must not resurrect the note.
	if err := SetNoteDeleted(ctx, db, note.ID, false, repoClock(t, 15, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	got, err = GetNote(ctx, db, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Deleted.Value {
		t.Fatal("stale restore must not resurrect a deleted note")
	}
}

// TestSetNoteFlagsLogicalClockBreaksTie complements
// TestRenameNotebookDeviceIDBreaksTie by exercising the other tie-break
// field: equal physical_ms with a higher logical counter must win,
// regardless of write arrival order, independent of device ID.
func TestSetNoteFlagsLogicalClockBreaksTie(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "Note", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	if err := SetNoteFlags(ctx, db, note.ID, model.NoteFlagArchived, repoClock(t, 20, 5, 0x02)); err != nil {
		t.Fatal(err)
	}
	if err := SetNoteFlags(ctx, db, note.ID, model.NoteFlagPinned, repoClock(t, 20, 2, 0x02)); err != nil {
		t.Fatal(err)
	}
	got, err := GetNote(ctx, db, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Flags.Value != model.NoteFlagArchived {
		t.Fatalf("Flags = %v, want the higher logical counter's write (Archived) to win the tie", got.Flags.Value)
	}
}

func TestSavedSearchCRUD(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	search, err := CreateSavedSearch(ctx, db, workspaceID, "Urgent home tasks", `tag:urgent tag:home`, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateSavedSearch(ctx, db, search.ID, "Urgent home tasks v2", `tag:urgent`, 2000); err != nil {
		t.Fatal(err)
	}
	searches, err := ListSavedSearches(ctx, db, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 1 || searches[0].Name != "Urgent home tasks v2" {
		t.Fatalf("ListSavedSearches() = %+v", searches)
	}
	if err := DeleteSavedSearch(ctx, db, search.ID); err != nil {
		t.Fatal(err)
	}
	searches, err = ListSavedSearches(ctx, db, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(searches) != 0 {
		t.Fatalf("ListSavedSearches() after delete = %+v, want empty", searches)
	}
}

// TestRepositoryFunctionsComposeIntoOneTransaction proves the Executor
// design goal: several repository calls sharing one *sql.Tx either all
// commit or all roll back together.
func TestRepositoryFunctionsComposeIntoOneTransaction(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	notebook, err := CreateNotebook(ctx, tx, workspaceID, model.Nil, "Personal", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNote(ctx, tx, workspaceID, notebook.ID, "Note", repoClock(t, 11, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if notebooks, err := ListNotebooks(ctx, db, workspaceID); err != nil || len(notebooks) != 0 {
		t.Fatalf("ListNotebooks() after rollback = %+v, err = %v, want none", notebooks, err)
	}
	if notes, err := ListNotes(ctx, db, workspaceID); err != nil || len(notes) != 0 {
		t.Fatalf("ListNotes() after rollback = %+v, err = %v, want none", notes, err)
	}

	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	notebook, err = CreateNotebook(ctx, tx, workspaceID, model.Nil, "Personal", repoClock(t, 10, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateNote(ctx, tx, workspaceID, notebook.ID, "Note", repoClock(t, 11, 0, 0x02)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if notebooks, err := ListNotebooks(ctx, db, workspaceID); err != nil || len(notebooks) != 1 {
		t.Fatalf("ListNotebooks() after commit = %+v, err = %v, want one", notebooks, err)
	}
	if notes, err := ListNotes(ctx, db, workspaceID); err != nil || len(notes) != 1 {
		t.Fatalf("ListNotes() after commit = %+v, err = %v, want one", notes, err)
	}
}
