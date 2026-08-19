package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAttachmentAddSaveRemoveRoundTrip(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "With attachment")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(sourcePath, []byte("fake png bytes"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}

	attachment, err := a.AddAttachmentFromFile(note.ID, sourcePath)
	if err != nil {
		t.Fatalf("AddAttachmentFromFile: %v", err)
	}
	if attachment.BlobID == "" || attachment.SizeBytes == 0 {
		t.Fatalf("AddAttachmentFromFile = %+v", attachment)
	}

	destPath := filepath.Join(t.TempDir(), "restored.png")
	saved, err := a.SaveAttachmentToFile(attachment.BlobID, destPath)
	if err != nil {
		t.Fatalf("SaveAttachmentToFile: %v", err)
	}
	if saved.DisplayName != "photo.png" {
		t.Fatalf("SaveAttachmentToFile = %+v", saved)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(got) != "fake png bytes" {
		t.Fatalf("restored content = %q", got)
	}

	if err := a.RemoveAttachment(note.ID, attachment.BlobID); err != nil {
		t.Fatalf("RemoveAttachment: %v", err)
	}
}

func TestAddAttachmentFromFileMissingSourceReportsError(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "No attachment yet")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	if _, err := a.AddAttachmentFromFile(note.ID, filepath.Join(t.TempDir(), "missing.bin")); err == nil {
		t.Fatal("AddAttachmentFromFile(missing source) error = nil, want error")
	}
}
