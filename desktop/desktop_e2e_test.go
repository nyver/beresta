package main

import (
	"encoding/base64"
	"testing"
)

// TestDesktopLocalOnlyLifecycle drives the coarse methods exported through
// Wails as one continuous offline scenario. Core/account has a deeper
// headless test, while this test protects the desktop DTO, validation, lock,
// and error-mapping boundary that the React application actually calls.
func TestDesktopLocalOnlyLifecycle(t *testing.T) {
	a := newTestApp(t)
	databasePath := testDatabasePath(t, a)
	backupRoot := t.TempDir()
	passphrase := "correct horse battery staple"

	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: databasePath, Passphrase: passphrase}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	notebook, err := a.CreateNotebook("", "Projects")
	if err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	tag, err := a.CreateTag("important")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	note, err := a.CreateNote(notebook.ID, "Launch checklist")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := a.SetNoteTag(note.ID, tag.ID, true); err != nil {
		t.Fatalf("SetNoteTag: %v", err)
	}
	update, format := encodedNoteUpdate(t, "ship the release notes")
	if err := a.CommitNoteBody(CommitNoteBodyRequest{NoteID: note.ID, UpdateBase64: update, UpdateFormat: format}); err != nil {
		t.Fatalf("CommitNoteBody: %v", err)
	}
	attachmentPlaintext := []byte("offline attachment")
	attachment, err := a.AddAttachmentFromBytes(
		note.ID,
		"plan.txt",
		"text/plain",
		base64.StdEncoding.EncodeToString(attachmentPlaintext),
	)
	if err != nil {
		t.Fatalf("AddAttachmentFromBytes: %v", err)
	}

	results, err := a.Search("release tag:important")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != note.ID {
		t.Fatalf("Search = %+v, want note %s", results, note.ID)
	}

	backup, err := a.CreateManualBackup(backupRoot)
	if err != nil {
		t.Fatalf("CreateManualBackup: %v", err)
	}
	preview, err := a.PreviewBackup(backup.ID)
	if err != nil {
		t.Fatalf("PreviewBackup: %v", err)
	}
	if len(preview.NoteTitles) != 1 || preview.NoteTitles[0] != note.Title {
		t.Fatalf("PreviewBackup = %+v", preview)
	}

	stray, err := a.CreateNote("", "Must disappear after restore")
	if err != nil {
		t.Fatalf("CreateNote(stray): %v", err)
	}
	plan, err := a.PlanRestore(backup.ID, nil)
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	if len(plan.Entries) == 0 {
		t.Fatal("PlanRestore returned no entries")
	}
	if _, err := a.GetNote(stray.ID); err != nil {
		t.Fatalf("PlanRestore mutated the live database: %v", err)
	}
	result, err := a.RestoreWhole(backup.ID, backupRoot)
	if err != nil {
		t.Fatalf("RestoreWhole: %v", err)
	}
	if result.SafetyBackup.ID == "" || result.SafetyBackup.Kind != "pre_restore" {
		t.Fatalf("RestoreWhole safety backup = %+v", result.SafetyBackup)
	}
	if _, err := a.GetNote(stray.ID); err == nil {
		t.Fatal("GetNote(stray) after restore succeeded, want the post-backup note removed")
	}
	restoredAttachment, err := a.ReadAttachmentPreview(attachment.BlobID)
	if err != nil {
		t.Fatalf("ReadAttachmentPreview after restore: %v", err)
	}
	restoredPlaintext, err := base64.StdEncoding.DecodeString(restoredAttachment.DataBase64)
	if err != nil {
		t.Fatalf("decode restored attachment: %v", err)
	}
	if string(restoredPlaintext) != string(attachmentPlaintext) {
		t.Fatalf("restored attachment = %q, want %q", restoredPlaintext, attachmentPlaintext)
	}

	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}
	if _, err := a.GetNote(note.ID); !isAppErrorCode(err, ErrCodeLocked) {
		t.Fatalf("GetNote while locked error = %v, want %s", err, ErrCodeLocked)
	}
	if _, err := a.UnlockAccount(UnlockAccountRequest{DatabasePath: databasePath, Passphrase: passphrase}); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}
	if _, err := a.GetNote(note.ID); err != nil {
		t.Fatalf("GetNote after unlock: %v", err)
	}
}
