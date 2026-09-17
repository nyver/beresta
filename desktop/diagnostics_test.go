package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/beresta-app/beresta/core/presentation"
)

func TestDiagnosticSummaryReportsLocalOnlyBeforeSyncIsConfigured(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := a.CreateNote("", "Diagnostics fixture note"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	summary, err := a.DiagnosticSummary()
	if err != nil {
		t.Fatalf("DiagnosticSummary: %v", err)
	}
	if summary.AppVersion == "" || summary.Platform != "windows" {
		t.Fatalf("summary = %+v, want a non-empty AppVersion and Platform windows", summary)
	}
	if summary.SyncConfigured {
		t.Fatalf("summary.SyncConfigured = true, want false before ConnectServer")
	}
	if summary.ConnectionState != presentation.SyncStateLocalOnly {
		t.Fatalf("ConnectionState = %q, want local_only", summary.ConnectionState)
	}
	if summary.Database != presentation.DatabaseHealthOK {
		t.Fatalf("Database = %q, want ok - the account is open, so its integrity check already passed", summary.Database)
	}
	if summary.Backup.Health != presentation.BackupHealthUnknown {
		t.Fatalf("Backup.Health = %q, want unknown before any daily backup has run", summary.Backup.Health)
	}
	if summary.StorageUsageBytes <= 0 {
		t.Fatalf("StorageUsageBytes = %d, want > 0 for an account with a created note", summary.StorageUsageBytes)
	}
}

func TestTechnicalDiagnosticsReportsIdentifiersAndMigrationVersion(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	info, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	technical, err := a.TechnicalDiagnostics()
	if err != nil {
		t.Fatalf("TechnicalDiagnostics: %v", err)
	}
	if technical.WorkspaceID != info.WorkspaceID {
		t.Fatalf("WorkspaceID = %q, want %q", technical.WorkspaceID, info.WorkspaceID)
	}
	if technical.DeviceID != info.DeviceID {
		t.Fatalf("DeviceID = %q, want %q", technical.DeviceID, info.DeviceID)
	}
	if technical.MigrationVersion <= 0 {
		t.Fatalf("MigrationVersion = %d, want > 0 for a freshly created database", technical.MigrationVersion)
	}
	if technical.TransportProtocol != "" {
		t.Fatalf("TransportProtocol = %q, want empty before ConnectServer", technical.TransportProtocol)
	}
}

// TestCopyDiagnosticsBundleOmitsNoteContentAndPassphrase covers task 4.3's
// seeded-secret sweep across every category
// specs/product-experience's "Layered privacy-preserving diagnostics"
// requirement prohibits: note content and titles, search queries,
// sensitive attachment names, and passwords. Each seed is planted through
// the same public App methods a real user action would use, then checked
// against the real collection pipeline's output - not the fixed schema
// internal/diagnostics validates in isolation, but what CopyDiagnostics
// actually assembles from a live account.
func TestCopyDiagnosticsBundleOmitsNoteContentAndPassphrase(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	passphrase := "seeded-secret-passphrase-canary"
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: passphrase}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	const title = "seeded-secret-note-title-canary"
	note, err := a.CreateNote("", title)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	const body = "seeded-secret-note-body-canary"
	update, format := encodedNoteUpdate(t, body)
	if err := a.CommitNoteBody(CommitNoteBodyRequest{NoteID: note.ID, UpdateBase64: update, UpdateFormat: format}); err != nil {
		t.Fatalf("CommitNoteBody: %v", err)
	}
	const notebookName = "seeded-secret-notebook-canary"
	if _, err := a.CreateNotebook("", notebookName); err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	const tagName = "seeded-secret-tag-canary"
	if _, err := a.CreateTag(tagName); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	const searchQuery = "seeded-secret-search-query-canary"
	if _, err := a.CreateSavedSearch("saved search", searchQuery); err != nil {
		t.Fatalf("CreateSavedSearch: %v", err)
	}
	const attachmentName = "seeded-secret-attachment-name-canary.png"
	if _, err := a.AddAttachmentFromBytes(note.ID, attachmentName, "image/png", base64.StdEncoding.EncodeToString([]byte("fake-image-bytes"))); err != nil {
		t.Fatalf("AddAttachmentFromBytes: %v", err)
	}

	bundle, err := a.CopyDiagnostics()
	if err != nil {
		t.Fatalf("CopyDiagnostics: %v", err)
	}
	seeds := []string{passphrase, title, body, notebookName, tagName, searchQuery, attachmentName}
	for _, secret := range seeds {
		if strings.Contains(bundle, secret) {
			t.Fatalf("CopyDiagnostics() bundle exposed a seeded secret %q: %s", secret, bundle)
		}
	}
}
