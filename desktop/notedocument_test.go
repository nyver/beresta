package main

import (
	"encoding/base64"
	"testing"

	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

func encodedNoteUpdate(t *testing.T, text string) (base64Update, format string) {
	t.Helper()
	doc := yjsadapter.New()
	defer doc.Close()
	if err := doc.Insert("body", 0, text, nil); err != nil {
		t.Fatalf("build test update: %v", err)
	}
	update, err := doc.EncodeStateAsUpdate(yjsadapter.FormatV1)
	if err != nil {
		t.Fatalf("encode test update: %v", err)
	}
	return base64.StdEncoding.EncodeToString(update), "v1"
}

func TestGetNoteDocumentReturnsEmptyStateForUnwrittenNote(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	doc, err := a.GetNoteDocument(note.ID)
	if err != nil {
		t.Fatalf("GetNoteDocument: %v", err)
	}
	if doc.UpdateBase64 == "" || doc.Format != "v2" {
		t.Fatalf("GetNoteDocument = %+v", doc)
	}
}

func TestCommitNoteBodyThenGetNoteDocumentRoundTrips(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: testDatabasePath(t, a), Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	note, err := a.CreateNote("", "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	update, format := encodedNoteUpdate(t, "hello world")
	if err := a.CommitNoteBody(CommitNoteBodyRequest{NoteID: note.ID, UpdateBase64: update, UpdateFormat: format}); err != nil {
		t.Fatalf("CommitNoteBody: %v", err)
	}

	doc, err := a.GetNoteDocument(note.ID)
	if err != nil {
		t.Fatalf("GetNoteDocument: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(doc.UpdateBase64)
	if err != nil {
		t.Fatalf("decode GetNoteDocument response: %v", err)
	}
	restored, err := yjsadapter.Restore(yjsadapter.FormatV2, raw)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	defer restored.Close()
	text, err := restored.Text("body")
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
}

func TestGetNoteDocumentRequiresUnlockedAccount(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.GetNoteDocument("00000000-0000-7000-8000-000000000000"); !isAppErrorCode(err, ErrCodeLocked) {
		t.Fatalf("GetNoteDocument on locked app error = %v, want %s", err, ErrCodeLocked)
	}
}
