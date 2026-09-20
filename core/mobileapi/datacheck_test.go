package mobileapi

import (
	"path/filepath"
	"testing"
)

// TestRunDataCheckReportsBackupNeedsAttentionBeforeAnyBackup mirrors
// desktop's identical test: the Advanced "Check my data" action (task 7.10)
// must report the backup domain needs attention before any backup exists,
// then report healthy once a backup completes and is verified.
func TestRunDataCheckReportsBackupNeedsAttentionBeforeAnyBackup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)

	if _, err := service.CreateAccount("create", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	before := decodeJSON[map[string]any](t, must(service.RunDataCheck()))
	if before["issue"] != "backup_needs_attention" {
		t.Fatalf("issue = %v, want backup_needs_attention before any backup exists", before["issue"])
	}
	if before["healthy"] != false {
		t.Fatalf("healthy = %v, want false before any backup exists", before["healthy"])
	}

	backupDestination := filepath.Join(t.TempDir(), "backups")
	if _, err := service.CreateBackup("create-backup", backupDestination); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	after := decodeJSON[map[string]any](t, must(service.RunDataCheck()))
	if after["issue"] != "none" {
		t.Fatalf("issue = %v, want none after a verified manual backup", after["issue"])
	}
	if after["healthy"] != true {
		t.Fatalf("healthy = %v, want true after a verified manual backup", after["healthy"])
	}
}
