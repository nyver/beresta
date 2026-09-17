package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
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

func TestListNoteAttachmentsReflectsAddAndRemove(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "With attachments")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	empty, err := a.ListNoteAttachments(note.ID)
	if err != nil {
		t.Fatalf("ListNoteAttachments (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListNoteAttachments (empty) = %v, want none", empty)
	}

	sourcePath := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(sourcePath, []byte("fake png bytes"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	added, err := a.AddAttachmentFromFile(note.ID, sourcePath)
	if err != nil {
		t.Fatalf("AddAttachmentFromFile: %v", err)
	}

	listed, err := a.ListNoteAttachments(note.ID)
	if err != nil {
		t.Fatalf("ListNoteAttachments: %v", err)
	}
	if len(listed) != 1 || listed[0].BlobID != added.BlobID || listed[0].DisplayName != "photo.png" || listed[0].MediaType != "image/png" || listed[0].SizeBytes != added.SizeBytes {
		t.Fatalf("ListNoteAttachments = %+v, want one entry matching %+v", listed, added)
	}

	if err := a.RemoveAttachment(note.ID, added.BlobID); err != nil {
		t.Fatalf("RemoveAttachment: %v", err)
	}
	afterRemove, err := a.ListNoteAttachments(note.ID)
	if err != nil {
		t.Fatalf("ListNoteAttachments (after remove): %v", err)
	}
	if len(afterRemove) != 0 {
		t.Fatalf("ListNoteAttachments (after remove) = %v, want none", afterRemove)
	}
}

func TestAddAttachmentFromBytesRoundTripsThroughPreview(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "Pasted image")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	content := []byte("pasted clipboard image bytes")
	added, err := a.AddAttachmentFromBytes(note.ID, "pasted-image.png", "image/png", base64.StdEncoding.EncodeToString(content))
	if err != nil {
		t.Fatalf("AddAttachmentFromBytes: %v", err)
	}
	if added.DisplayName != "pasted-image.png" || added.MediaType != "image/png" || added.SizeBytes != uint64(len(content)) {
		t.Fatalf("AddAttachmentFromBytes = %+v", added)
	}

	preview, err := a.ReadAttachmentPreview(added.BlobID)
	if err != nil {
		t.Fatalf("ReadAttachmentPreview: %v", err)
	}
	if preview.DisplayName != "pasted-image.png" || preview.MediaType != "image/png" {
		t.Fatalf("ReadAttachmentPreview = %+v", preview)
	}
	got, err := base64.StdEncoding.DecodeString(preview.DataBase64)
	if err != nil {
		t.Fatalf("decode preview data: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("preview content = %q, want %q", got, content)
	}
}

func TestAddAttachmentFromBytesRejectsInvalidBase64(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "Bad paste")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	if _, err := a.AddAttachmentFromBytes(note.ID, "x.png", "image/png", "not-base64!!"); err == nil {
		t.Fatal("AddAttachmentFromBytes(invalid base64) error = nil, want error")
	}
}

func TestReadAttachmentPreviewRejectsOversizedContent(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "Big attachment")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "big.bin")
	big := make([]byte, maxAttachmentPreviewBytes+1)
	if err := os.WriteFile(sourcePath, big, 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	added, err := a.AddAttachmentFromFile(note.ID, sourcePath)
	if err != nil {
		t.Fatalf("AddAttachmentFromFile: %v", err)
	}

	if _, err := a.ReadAttachmentPreview(added.BlobID); err == nil {
		t.Fatal("ReadAttachmentPreview(oversized) error = nil, want error")
	}
}

// TestAddAttachmentFromFileRejectsOversizedSourceBeforeReadingIt covers
// task 5.1's preflight size check: AddAttachmentFromFile stats the source
// before opening it, so an oversized file is rejected immediately with
// the same user-facing message as the mid-stream limit
// (corecrypto.ErrAttachmentResourceLimit), never opening or copying it.
func TestAddAttachmentFromFileRejectsOversizedSourceBeforeReadingIt(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "With oversized attachment")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file reports its logical size via Stat without actually
	// writing (and needing) that many real bytes to disk.
	if err := f.Truncate(int64(corecrypto.MaxAttachmentPlaintextBytes) + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := a.AddAttachmentFromFile(note.ID, sourcePath); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("AddAttachmentFromFile(oversized) error = %v, want ErrCodeInvalidInput", err)
	}
}

// TestAddAttachmentFromBytesAcceptsAnOrdinarilySizedPayload proves the new
// preflight checks (task 5.1) do not interfere with a normal-sized
// paste/drop attachment - a regression guard alongside
// TestMapErrorResultAlwaysEncodesAsJSON's direct check that the new
// sentinel errors map to their own distinct, localized codes.
func TestAddAttachmentFromBytesAcceptsAnOrdinarilySizedPayload(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "With attachment")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	data := base64.StdEncoding.EncodeToString([]byte("small content"))
	if _, err := a.AddAttachmentFromBytes(note.ID, "a.txt", "text/plain", data); err != nil {
		t.Fatalf("AddAttachmentFromBytes with an ordinary size should still succeed: %v", err)
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
