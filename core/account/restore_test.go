package account

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
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

	// A successful whole restore must survive a restart too: reopening
	// from disk must expose the restored collection, not silently revert
	// to the pre-restore state or some mix of the two
	// (specs/backup-and-recovery.md, "Successful whole restore").
	databasePath := created.databasePath
	wrapper := created.wrapper
	if err := created.Lock(); err != nil {
		t.Fatalf("Lock before simulated restart: %v", err)
	}
	reopened, err := Unlock(ctx, UnlockOptions{
		DatabasePath: databasePath,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
	})
	if err != nil {
		t.Fatalf("Unlock (simulated restart): %v", err)
	}
	t.Cleanup(func() { reopened.Lock() })

	if _, err := reopened.GetNote(ctx, original.ID); err != nil {
		t.Fatalf("original note missing after restart: %v", err)
	}
	if _, err := reopened.GetNote(ctx, afterBackup.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("post-backup note error after restart = %v, want ErrNotFound", err)
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

// restoreFaultFS is a restoreSwapFS that fails the first rename or writeFile
// call its match predicate accepts, then falls through to the real
// filesystem for every call after (including rollback's own undo calls),
// so a test can inject exactly one fault at a chosen swap/key-envelope
// boundary without also breaking the rollback path that must recover from
// it.
type restoreFaultFS struct {
	renameMatch func(oldpath, newpath string) bool
	renameErr   error
	renameFired bool

	writeFileMatch func(path string) bool
	writeFileErr   error
	writeFileFired bool
}

func (f *restoreFaultFS) rename(oldpath, newpath string) error {
	if !f.renameFired && f.renameMatch != nil && f.renameMatch(oldpath, newpath) {
		f.renameFired = true
		return f.renameErr
	}
	return os.Rename(oldpath, newpath)
}

func (f *restoreFaultFS) writeFile(path string, data []byte, perm os.FileMode) error {
	if !f.writeFileFired && f.writeFileMatch != nil && f.writeFileMatch(path) {
		f.writeFileFired = true
		return f.writeFileErr
	}
	return os.WriteFile(path, data, perm)
}

// assertRestoreRolledBackToOriginal proves a failed RestoreWhole leaves
// created immediately usable with its pre-restore data, and that a
// simulated restart (Lock then Unlock from the same on-disk path and
// wrapper, exactly like TestNotesNotebooksTagsAndAttachmentsSurviveRestart)
// exposes that same original data - never a mix of old and restored
// content, and never an account stuck without a usable database connection
// (specs/backup-and-recovery.md, "Restore is interrupted").
func assertRestoreRolledBackToOriginal(t *testing.T, created *Account, originalNoteID model.ID, divergedTitle string) {
	t.Helper()
	ctx := context.Background()

	if _, err := created.GetNote(ctx, originalNoteID); err != nil {
		t.Fatalf("original note missing immediately after a rolled-back restore: %v", err)
	}
	if _, err := created.CreateNote(ctx, defaultWorkspaceID(t, created), model.Nil, "Still works"); err != nil {
		t.Fatalf("CreateNote immediately after a rolled-back restore: %v", err)
	}

	databasePath := created.databasePath
	wrapper := created.wrapper
	if err := created.Lock(); err != nil {
		t.Fatalf("Lock before simulated restart: %v", err)
	}
	reopened, err := Unlock(ctx, UnlockOptions{
		DatabasePath: databasePath,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
	})
	if err != nil {
		t.Fatalf("Unlock (simulated restart): %v", err)
	}
	t.Cleanup(func() { reopened.Lock() })

	notes, err := reopened.ListNotes(ctx, defaultWorkspaceID(t, reopened))
	if err != nil {
		t.Fatalf("ListNotes after simulated restart: %v", err)
	}
	var titles []string
	for _, n := range notes {
		titles = append(titles, n.Title.Value)
	}
	for _, want := range []string{divergedTitle, "Still works"} {
		found := false
		for _, title := range titles {
			if title == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected %q among notes after simulated restart, got %v", want, titles)
		}
	}
}

// TestRestoreWholeMoveAsideFaultLeavesAccountUsableAndSurvivesRestart covers
// task 5.6: a failure while moving the current database aside - the very
// first swap boundary, before any prepared content has touched
// databasePath - must not delete the still-untouched original database nor
// leave the live Account without a database connection (regression test for
// a bug where this boundary's failure path returned (nil, err) directly
// instead of reopening the original database like every other failure path
// here does).
func TestRestoreWholeMoveAsideFaultLeavesAccountUsableAndSurvivesRestart(t *testing.T) {
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
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Diverged after backup"); err != nil {
		t.Fatal(err)
	}

	databasePath := created.databasePath
	fault := &restoreFaultFS{
		renameMatch: func(oldpath, _ string) bool { return oldpath == databasePath },
		renameErr:   &os.LinkError{Op: "rename", Err: syscall.EACCES},
	}
	currentRestoreFS = fault
	defer func() { currentRestoreFS = osRestoreFS{} }()

	if _, err := created.RestoreWhole(ctx, backup.ID, backupsRoot, time.Now()); err == nil {
		t.Fatal("expected RestoreWhole to fail when moving the current database aside fails")
	}
	if !fault.renameFired {
		t.Fatal("test did not actually exercise the move-aside fault")
	}

	assertRestoreRolledBackToOriginal(t, created, original.ID, "Diverged after backup")
}

// TestRestoreWholeSwapFaultLeavesAccountUsableAndSurvivesRestart covers the
// second swap boundary: the prepared (restored) database's rename into
// databasePath itself failing, after the original has already been moved
// aside.
func TestRestoreWholeSwapFaultLeavesAccountUsableAndSurvivesRestart(t *testing.T) {
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
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Diverged after backup"); err != nil {
		t.Fatal(err)
	}

	databasePath := created.databasePath
	fault := &restoreFaultFS{
		renameMatch: func(_, newpath string) bool { return newpath == databasePath },
		renameErr:   &os.LinkError{Op: "rename", Err: syscall.ENOSPC},
	}
	currentRestoreFS = fault
	defer func() { currentRestoreFS = osRestoreFS{} }()

	if _, err := created.RestoreWhole(ctx, backup.ID, backupsRoot, time.Now()); err == nil {
		t.Fatal("expected RestoreWhole to fail when swapping the restored database into place fails")
	}
	if !fault.renameFired {
		t.Fatal("test did not actually exercise the swap fault")
	}

	assertRestoreRolledBackToOriginal(t, created, original.ID, "Diverged after backup")
}

// TestRestoreWholeEnvelopeWriteFaultLeavesAccountUsableAndSurvivesRestart
// covers the key-envelope boundary: writing the restored database's new key
// envelope failing after the database file itself has already been
// swapped in.
func TestRestoreWholeEnvelopeWriteFaultLeavesAccountUsableAndSurvivesRestart(t *testing.T) {
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
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Diverged after backup"); err != nil {
		t.Fatal(err)
	}

	envelopeFile := envelopePath(created.databasePath)
	fault := &restoreFaultFS{
		writeFileMatch: func(path string) bool { return path == envelopeFile },
		writeFileErr:   &os.PathError{Op: "write", Path: envelopeFile, Err: syscall.ENOSPC},
	}
	currentRestoreFS = fault
	defer func() { currentRestoreFS = osRestoreFS{} }()

	if _, err := created.RestoreWhole(ctx, backup.ID, backupsRoot, time.Now()); err == nil {
		t.Fatal("expected RestoreWhole to fail when writing the restored key envelope fails")
	}
	if !fault.writeFileFired {
		t.Fatal("test did not actually exercise the key-envelope write fault")
	}

	assertRestoreRolledBackToOriginal(t, created, original.ID, "Diverged after backup")
}

// TestUnlockCleansStaleRestoreStagingDirectoryOnStartup covers task 5.8's
// restart-safe temporary-file maintenance: a whole-restore staging
// directory left behind by a process that terminated before RestoreWhole's
// own deferred cleanup ran must be swept automatically the next time the
// account is unlocked, so it never accumulates indefinitely
// (specs/backup-and-recovery.md, "Restore is interrupted").
func TestUnlockCleansStaleRestoreStagingDirectoryOnStartup(t *testing.T) {
	ctx := context.Background()
	path := tempDBPath(t)
	wrapper := newFakeWrapper()
	created, err := Create(ctx, CreateOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Survives a crashed restore")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a process that terminated mid-RestoreWhole, after
	// os.MkdirTemp created its staging directory but before the deferred
	// os.RemoveAll in RestoreWhole could run.
	stalePath := filepath.Join(filepath.Dir(path), ".whole-restore-crashed")
	if err := os.MkdirAll(stalePath, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(stalePath, "source.db")
	if err := os.WriteFile(sourcePath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Back-date the directory and its child file well past
	// restoreStagingMinAge: CleanupRestoreStaging only sweeps directories
	// whose most recent activity - including any child file's own mtime,
	// not just the directory's own - is old enough that no concurrent
	// RestoreWhole in another process could still own them, and a
	// directory/file this test just created is otherwise indistinguishable
	// from one still being actively written to.
	oldEnough := time.Now().Add(-2 * restoreStagingMinAge)
	if err := os.Chtimes(sourcePath, oldEnough, oldEnough); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stalePath, oldEnough, oldEnough); err != nil {
		t.Fatal(err)
	}

	if err := created.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	reopened, err := Unlock(ctx, UnlockOptions{
		DatabasePath: path,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
	})
	if err != nil {
		t.Fatalf("Unlock (simulated restart): %v", err)
	}
	t.Cleanup(func() { reopened.Lock() })

	// The sweep runs in a background goroutine off Unlock's critical path,
	// so give it a bounded window to finish rather than asserting the
	// instant Unlock returns.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(stalePath); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale restore staging directory still exists after Unlock's background sweep deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := reopened.GetNote(ctx, note.ID); err != nil {
		t.Fatalf("GetNote after restart: %v", err)
	}
}

func TestCleanupRestoreStagingOnMissingDirectoryIsANoOp(t *testing.T) {
	if err := CleanupRestoreStaging(filepath.Join(t.TempDir(), "does-not-exist")); err != nil {
		t.Fatalf("CleanupRestoreStaging on a missing directory: %v", err)
	}
}

// TestCleanupRestoreStagingReportsARemovalFailure proves the sweep's error
// path (restore.go's errs-accumulation branch) is actually reachable and
// reported rather than silently swallowed, by injecting a removal failure
// through the same currentBackupFS seam CleanupBackupStaging's equivalent
// path already exercises fault injection through.
func TestCleanupRestoreStagingReportsARemovalFailure(t *testing.T) {
	databaseDir := t.TempDir()
	stalePath := filepath.Join(databaseDir, ".whole-restore-locked")
	if err := os.MkdirAll(stalePath, 0o700); err != nil {
		t.Fatal(err)
	}
	oldEnough := time.Now().Add(-2 * restoreStagingMinAge)
	if err := os.Chtimes(stalePath, oldEnough, oldEnough); err != nil {
		t.Fatal(err)
	}

	wrapped := &os.PathError{Op: "remove", Path: stalePath, Err: syscall.EACCES}
	fake := &injectingBackupFS{failRemoveAll: wrapped}
	previous := currentBackupFS
	currentBackupFS = fake
	defer func() { currentBackupFS = previous }()

	err := CleanupRestoreStaging(databaseDir)
	if err == nil {
		t.Fatal("CleanupRestoreStaging with an injected removal failure succeeded, want an error")
	}
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("CleanupRestoreStaging error = %v, want it to wrap the injected EACCES", err)
	}
}

// TestCleanupRestoreStagingLeavesARecentDirectoryAlone covers the other
// half of restoreStagingMinAge's safety margin: nothing today stops a
// second process from calling Unlock (and therefore this sweep) against
// the same database directory while a first process is genuinely
// mid-RestoreWhole, so the sweep must never remove a staging directory
// young enough that it could still be in active use, only ones old enough
// to be unambiguously abandoned.
func TestCleanupRestoreStagingLeavesARecentDirectoryAlone(t *testing.T) {
	databaseDir := t.TempDir()
	recentPath := filepath.Join(databaseDir, ".whole-restore-inprogress")
	if err := os.MkdirAll(recentPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := CleanupRestoreStaging(databaseDir); err != nil {
		t.Fatalf("CleanupRestoreStaging: %v", err)
	}

	if _, err := os.Stat(recentPath); err != nil {
		t.Fatalf("a recently created restore staging directory must survive the sweep, stat err = %v", err)
	}
}

// TestCleanupRestoreStagingLeavesADirectoryAloneWhenAChildFileIsStillFresh
// covers a blind spot a directory's own ModTime alone would miss: it only
// advances when an entry is added, renamed, or removed within it, not while
// an existing child file is still being written to - exactly what
// RestoreWhole spends most of its time doing (writing into source.db, then
// restored.db) after the directory itself was created. A staging directory
// old enough on its own ModTime must still survive the sweep if one of its
// files was written to more recently.
func TestCleanupRestoreStagingLeavesADirectoryAloneWhenAChildFileIsStillFresh(t *testing.T) {
	databaseDir := t.TempDir()
	stagingPath := filepath.Join(databaseDir, ".whole-restore-writing")
	if err := os.MkdirAll(stagingPath, 0o700); err != nil {
		t.Fatal(err)
	}
	oldEnough := time.Now().Add(-2 * restoreStagingMinAge)
	if err := os.Chtimes(stagingPath, oldEnough, oldEnough); err != nil {
		t.Fatal(err)
	}

	// The child file itself was created long ago (so the directory's own
	// entry-add didn't just happen) but is still actively being written to.
	childPath := filepath.Join(stagingPath, "restored.db")
	if err := os.WriteFile(childPath, []byte("in progress"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stagingPath, oldEnough, oldEnough); err != nil {
		t.Fatal(err) // writing the child also bumped the directory's own mtime; restore it.
	}
	recentWrite := time.Now()
	if err := os.Chtimes(childPath, recentWrite, recentWrite); err != nil {
		t.Fatal(err)
	}

	if err := CleanupRestoreStaging(databaseDir); err != nil {
		t.Fatalf("CleanupRestoreStaging: %v", err)
	}

	if _, err := os.Stat(stagingPath); err != nil {
		t.Fatalf("a staging directory with a recently written child file must survive the sweep, stat err = %v", err)
	}
}
