package mobileapi

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticSummaryReportsLocalOnlyBeforeSyncIsConfigured(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)
	if _, err := service.CreateAccount("req-1", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	encoded, err := service.DiagnosticSummary("4.2.0")
	if err != nil {
		t.Fatalf("DiagnosticSummary: %v", err)
	}
	summary := decodeJSON[map[string]any](t, encoded)
	if summary["app_version"] != "4.2.0" || summary["platform"] != "android" {
		t.Fatalf("summary = %+v, want app_version 4.2.0 and platform android", summary)
	}
	if summary["sync_configured"] != false {
		t.Fatalf("sync_configured = %v, want false before ConnectServer", summary["sync_configured"])
	}
	if summary["connection_state"] != "local_only" {
		t.Fatalf("connection_state = %v, want local_only", summary["connection_state"])
	}
	if summary["database"] != "ok" {
		t.Fatalf("database = %v, want ok - the account is open, so its integrity check already passed", summary["database"])
	}
}

func TestTechnicalDiagnosticsReportsIdentifiers(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)
	created, err := service.CreateAccount("req-1", dbPath, "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	account := decodeJSON[map[string]any](t, created)

	encoded, err := service.TechnicalDiagnostics()
	if err != nil {
		t.Fatalf("TechnicalDiagnostics: %v", err)
	}
	technical := decodeJSON[map[string]any](t, encoded)
	if technical["workspace_id"] != account["workspace_id"] {
		t.Fatalf("workspace_id = %v, want %v", technical["workspace_id"], account["workspace_id"])
	}
	if technical["device_id"] != account["device_id"] {
		t.Fatalf("device_id = %v, want %v", technical["device_id"], account["device_id"])
	}
	migrationVersion, _ := technical["migration_version"].(float64)
	if migrationVersion <= 0 {
		t.Fatalf("migration_version = %v, want > 0 for a freshly created database", technical["migration_version"])
	}
}

// TestCopyDiagnosticsBundleOmitsNoteContentAndPassphrase covers task 4.3's
// seeded-secret sweep across every category
// specs/product-experience's "Layered privacy-preserving diagnostics"
// requirement prohibits: note content and titles, sensitive attachment
// names, and passwords. Each seed is planted through the same public
// Service methods a real user action would use, then checked against the
// real collection pipeline's output - not the fixed schema
// internal/diagnostics validates in isolation, but what CopyDiagnostics
// actually assembles from a live account.
func TestCopyDiagnosticsBundleOmitsNoteContentAndPassphrase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)
	passphrase := "seeded-secret-passphrase-canary"
	if _, err := service.CreateAccount("req-1", dbPath, passphrase); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	const title = "seeded-secret-note-title-canary"
	created, err := service.CreateNote("req-2", "", title)
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	noteID := decodeJSON[map[string]any](t, created)["id"].(string)
	const notebookName = "seeded-secret-notebook-canary"
	if _, err := service.CreateNotebook("req-3", "", notebookName); err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	const tagName = "seeded-secret-tag-canary"
	if _, err := service.CreateTag("req-4", tagName); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	const attachmentName = "seeded-secret-attachment-name-canary.png"
	if err := service.AddAttachmentData("req-5", noteID, attachmentName, "image/png", []byte("fake-image-bytes")); err != nil {
		t.Fatalf("AddAttachmentData: %v", err)
	}

	bundle, err := service.CopyDiagnostics("4.2.0")
	if err != nil {
		t.Fatalf("CopyDiagnostics: %v", err)
	}
	seeds := []string{passphrase, title, notebookName, tagName, attachmentName}
	for _, secret := range seeds {
		if strings.Contains(bundle, secret) {
			t.Fatalf("CopyDiagnostics() bundle exposed a seeded secret %q: %s", secret, bundle)
		}
	}
}
