package main

import (
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

// TestCopyDiagnosticsBundleOmitsNoteContentAndPassphrase covers task 4.2's
// seeded-secret guarantee at the real collection layer (not just the fixed
// schema internal/diagnostics validates): a note's distinctive title/body
// and the account passphrase must never appear in the copied bundle, only
// the allowlisted counts and identifiers.
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

	bundle, err := a.CopyDiagnostics()
	if err != nil {
		t.Fatalf("CopyDiagnostics: %v", err)
	}
	for _, secret := range []string{passphrase, title, body} {
		if strings.Contains(bundle, secret) {
			t.Fatalf("CopyDiagnostics() bundle exposed a seeded secret %q: %s", secret, bundle)
		}
	}
}
