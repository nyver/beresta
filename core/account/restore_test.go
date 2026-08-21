package account

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

func TestPreviewBackupListsNoteTitles(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Beta"); err != nil {
		t.Fatal(err)
	}

	backup, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	preview, err := created.PreviewBackup(ctx, backup.ID)
	if err != nil {
		t.Fatalf("PreviewBackup: %v", err)
	}
	if len(preview.NoteTitles) != 2 {
		t.Fatalf("NoteTitles = %v, want 2 entries", preview.NoteTitles)
	}
}

func TestPlanRestoreClassifiesAdditionUpdateUnchanged(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	unchanged, err := created.CreateNote(ctx, workspaceID, model.Nil, "Stays the same")
	if err != nil {
		t.Fatal(err)
	}
	toUpdate, err := created.CreateNote(ctx, workspaceID, model.Nil, "Original title")
	if err != nil {
		t.Fatal(err)
	}

	backup, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	// A note that will be an "addition" when restored, since it is created
	// only after the backup.
	added, err := created.CreateNote(ctx, workspaceID, model.Nil, "Only exists after backup")
	if err != nil {
		t.Fatal(err)
	}
	_ = added
	if err := created.SetNoteFlags(ctx, workspaceID, toUpdate.ID, model.NoteFlagPinned); err != nil {
		t.Fatalf("SetNoteFlags: %v", err)
	}

	plan, err := created.PlanRestore(ctx, backup.ID, nil)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	kinds := make(map[model.ID]RestoreChangeKind, len(plan.Entries))
	for _, e := range plan.Entries {
		kinds[e.NoteID] = e.Kind
	}
	if kinds[unchanged.ID] != RestoreChangeUnchanged {
		t.Fatalf("unchanged note kind = %v, want RestoreChangeUnchanged", kinds[unchanged.ID])
	}
	if kinds[toUpdate.ID] != RestoreChangeUpdate {
		t.Fatalf("updated note kind = %v, want RestoreChangeUpdate", kinds[toUpdate.ID])
	}
	if _, present := kinds[added.ID]; present {
		t.Fatal("a note created after the backup should not appear in its plan")
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("plan entries = %d, want 2 (the backup's own two notes)", len(plan.Entries))
	}
}

func TestRestoreSelectiveImportsAsNewNoteWithNotebookTagAndAttachment(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	notebook, err := created.CreateNotebook(ctx, workspaceID, model.Nil, "Journal")
	if err != nil {
		t.Fatal(err)
	}
	tag, err := created.CreateTag(ctx, workspaceID, "important")
	if err != nil {
		t.Fatal(err)
	}
	note, err := created.CreateNote(ctx, workspaceID, notebook.ID, "Entry one")
	if err != nil {
		t.Fatal(err)
	}
	if err := created.SetNoteTag(ctx, workspaceID, note.ID, tag.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "photo.txt", "text/plain", bytes.NewReader([]byte("photo bytes"))); err != nil {
		t.Fatal(err)
	}

	backup, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	// Simulate loss: delete the note locally (tombstoned, but selective
	// restore should still bring back an independent, undeleted copy).
	if err := created.DeleteNote(ctx, workspaceID, note.ID); err != nil {
		t.Fatal(err)
	}

	result, err := created.RestoreSelective(ctx, backup.ID, []model.ID{note.ID}, t.TempDir(), time.Now())
	if err != nil {
		t.Fatalf("RestoreSelective: %v", err)
	}
	if len(result.NewNoteIDs) != 1 {
		t.Fatalf("NewNoteIDs = %v, want one entry", result.NewNoteIDs)
	}
	newNoteID := result.NewNoteIDs[0]
	if newNoteID == note.ID {
		t.Fatal("selective restore must assign a fresh note ID, not reuse the original")
	}

	restoredNote, err := created.GetNote(ctx, newNoteID)
	if err != nil {
		t.Fatalf("GetNote (restored): %v", err)
	}
	if restoredNote.Title.Value != "Entry one" || restoredNote.Deleted.Value {
		t.Fatalf("restored note = %+v", restoredNote)
	}
	if restoredNote.NotebookID.Value.IsZero() {
		t.Fatal("restored note should be filed under a recreated notebook")
	}
	notebooks, err := created.ListNotebooks(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	foundNotebook := false
	for _, nb := range notebooks {
		if nb.ID == restoredNote.NotebookID.Value && nb.Name == "Journal" {
			foundNotebook = true
		}
	}
	if !foundNotebook {
		t.Fatal("restored note's notebook was not resolved to a notebook named Journal")
	}

	tagIDs, err := store.NoteTagIDs(ctx, created.db, newNoteID)
	if err != nil || len(tagIDs) != 1 {
		t.Fatalf("NoteTagIDs = %v, err = %v", tagIDs, err)
	}
	restoredTag, err := store.GetTagByName(ctx, created.db, workspaceID, "important")
	if err != nil || tagIDs[0] != restoredTag.ID {
		t.Fatalf("restored tag mismatch: tagIDs=%v restoredTag=%v err=%v", tagIDs, restoredTag, err)
	}

	blobIDs, err := store.NoteAttachmentBlobIDs(ctx, created.db, newNoteID)
	if err != nil || len(blobIDs) != 1 {
		t.Fatalf("NoteAttachmentBlobIDs = %v, err = %v", blobIDs, err)
	}
	var out bytes.Buffer
	name, _, err := created.ReadAttachment(ctx, workspaceID, blobIDs[0], &out)
	if err != nil || name != "photo.txt" || out.String() != "photo bytes" {
		t.Fatalf("restored attachment name=%q content=%q err=%v", name, out.String(), err)
	}

	doc, err := loadNoteDocument(ctx, created.db, created, workspaceID, newNoteID)
	if err != nil {
		t.Fatal(err)
	}
	text, err := doc.Text(noteBodyRoot)
	doc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Fatalf("restored empty-body note text = %q, want empty (note was never given a body)", text)
	}
}

func TestRestoreWholeReplacesLiveDatabaseAndKeepsSafetyBackup(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	original, err := created.CreateNote(ctx, workspaceID, model.Nil, "Before backup")
	if err != nil {
		t.Fatal(err)
	}

	backupsRoot := t.TempDir()
	backup, err := created.CreateBackup(ctx, backupsRoot, store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	// Diverge after the backup: this note must disappear after whole
	// restore.
	afterBackup, err := created.CreateNote(ctx, workspaceID, model.Nil, "After backup")
	if err != nil {
		t.Fatal(err)
	}

	result, err := created.RestoreWhole(ctx, backup.ID, backupsRoot, time.Now())
	if err != nil {
		t.Fatalf("RestoreWhole: %v", err)
	}
	if result.SafetyBackup.Kind != store.BackupKindPreRestore {
		t.Fatalf("SafetyBackup.Kind = %d, want BackupKindPreRestore", result.SafetyBackup.Kind)
	}

	if _, err := created.GetNote(ctx, original.ID); err != nil {
		t.Fatalf("original note missing after restore: %v", err)
	}
	if _, err := created.GetNote(ctx, afterBackup.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("post-backup note error = %v, want ErrNotFound (whole restore should have removed it)", err)
	}

	// The pre-restore safety backup must itself be a usable, previewable
	// backup covering the state just before the restore (i.e. including
	// afterBackup).
	safetyPreview, err := created.PreviewBackup(ctx, result.SafetyBackup.ID)
	if err != nil {
		t.Fatalf("PreviewBackup (safety backup): %v", err)
	}
	found := false
	for _, title := range safetyPreview.NoteTitles {
		if title == "After backup" {
			found = true
		}
	}
	if !found {
		t.Fatal("pre-restore safety backup should contain the state that existed immediately before the restore")
	}
}

func TestRestoreWholeRejectsCorruptBackupAndLeavesDataUnchanged(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untouched")
	if err != nil {
		t.Fatal(err)
	}

	backupsRoot := t.TempDir()
	backup, err := created.CreateBackup(ctx, backupsRoot, store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backup.Location, backupSnapshotFile), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := created.RestoreWhole(ctx, backup.ID, backupsRoot, time.Now()); !errors.Is(err, ErrBackupCorrupt) {
		t.Fatalf("RestoreWhole error = %v, want ErrBackupCorrupt", err)
	}

	// The account must remain fully usable with its original data.
	if _, err := created.GetNote(ctx, note.ID); err != nil {
		t.Fatalf("GetNote after rejected restore: %v", err)
	}
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Still works"); err != nil {
		t.Fatalf("CreateNote after rejected restore: %v", err)
	}
}

func TestRestoreDatabaseFileRollsBackAndReopensOnFailure(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Must survive a failed swap")
	if err != nil {
		t.Fatal(err)
	}

	databasePath := created.databasePath
	wrapper := created.wrapper
	if err := created.db.Close(); err != nil {
		t.Fatal(err)
	}

	freshKey, envelope, err := store.LoadOrCreateDatabaseKey(ctx, wrapper, localDeviceKeyID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer freshKey.Close()

	// A prepared path that does not exist forces the move-into-place step
	// to fail, exercising the rollback path deterministically.
	missingPreparedPath := filepath.Join(t.TempDir(), "does-not-exist.db")
	reopened, err := restoreDatabaseFile(ctx, databasePath, missingPreparedPath, wrapper, freshKey, envelope)
	if err == nil {
		t.Fatal("expected an error for a missing prepared database")
	}
	if reopened == nil {
		t.Fatal("restoreDatabaseFile should still return a reopened database after rolling back")
	}
	defer reopened.Close()

	var title string
	if err := reopened.QueryRowContext(ctx, `SELECT title FROM notes WHERE id = ?`, note.ID.Bytes()).Scan(&title); err != nil {
		t.Fatalf("query reopened original database: %v", err)
	}
	if title != "Must survive a failed swap" {
		t.Fatalf("title = %q after rollback", title)
	}

	// created.db is now stale (closed); avoid the test's own Lock cleanup
	// double-closing it.
	created.db = reopened
}

func TestPlanRestoreOnlyCountsStorageForAttachmentsNotAlreadyLocal(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	shared, err := created.CreateNote(ctx, workspaceID, model.Nil, "Shared attachment")
	if err != nil {
		t.Fatal(err)
	}
	sharedAttachment, err := created.AddAttachment(ctx, workspaceID, shared.ID, "shared.txt", "text/plain", bytes.NewReader([]byte("shared content")))
	if err != nil {
		t.Fatal(err)
	}

	backup, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	// Delete the note locally so it becomes an "addition" candidate, but
	// the attachment's blob stays published locally via a second note that
	// still references identical content (dedup means it is the same
	// BlobID and so remains in the live blob store).
	other, err := created.CreateNote(ctx, workspaceID, model.Nil, "Keeps the blob alive")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.AddAttachment(ctx, workspaceID, other.ID, "shared.txt", "text/plain", bytes.NewReader([]byte("shared content"))); err != nil {
		t.Fatal(err)
	}
	if err := created.DeleteNote(ctx, workspaceID, shared.ID); err != nil {
		t.Fatal(err)
	}

	plan, err := created.PlanRestore(ctx, backup.ID, []model.ID{shared.ID})
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if len(plan.Entries) != 1 || plan.Entries[0].Kind != RestoreChangeUpdate {
		t.Fatalf("plan entries = %+v, want one Update (the note's deleted state differs)", plan.Entries)
	}
	if plan.RequiredStorageBytes != 0 {
		t.Fatalf("RequiredStorageBytes = %d, want 0: %x is already published locally", plan.RequiredStorageBytes, sharedAttachment.BlobID.Bytes())
	}
}

func TestRestoreWholeRepublishesABlobMissingFromTheLiveStore(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "a.txt", "text/plain", bytes.NewReader([]byte("payload")))
	if err != nil {
		t.Fatal(err)
	}

	backupsRoot := t.TempDir()
	backup, err := created.CreateBackup(ctx, backupsRoot, store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	// Simulate the live blob having been lost (for example, by disk
	// corruption or an aggressive external cleanup) while the backup set
	// still has its own copy.
	if err := os.Remove(created.blobs.Path(attachment.BlobID)); err != nil {
		t.Fatal(err)
	}
	exists, err := created.blobs.Exists(attachment.BlobID)
	if err != nil || exists {
		t.Fatalf("blob should be gone before restore: exists=%v err=%v", exists, err)
	}

	if _, err := created.RestoreWhole(ctx, backup.ID, backupsRoot, time.Now()); err != nil {
		t.Fatalf("RestoreWhole: %v", err)
	}

	exists, err = created.blobs.Exists(attachment.BlobID)
	if err != nil || !exists {
		t.Fatalf("blob should be republished from the backup set after restore: exists=%v err=%v", exists, err)
	}
	var out bytes.Buffer
	if _, _, err := created.ReadAttachment(ctx, workspaceID, attachment.BlobID, &out); err != nil || out.String() != "payload" {
		t.Fatalf("ReadAttachment after republish: content=%q err=%v", out.String(), err)
	}
}
