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

// TestCopyDiagnosticsBundleOmitsNoteContentAndPassphrase covers task 4.2's
// seeded-secret guarantee at the real collection layer (not just the fixed
// schema internal/diagnostics validates): a note's distinctive title and
// the account passphrase must never appear in the copied bundle.
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
	if _, err := service.CreateNote("req-2", "", title); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	bundle, err := service.CopyDiagnostics("4.2.0")
	if err != nil {
		t.Fatalf("CopyDiagnostics: %v", err)
	}
	for _, secret := range []string{passphrase, title} {
		if strings.Contains(bundle, secret) {
			t.Fatalf("CopyDiagnostics() bundle exposed a seeded secret %q: %s", secret, bundle)
		}
	}
}
