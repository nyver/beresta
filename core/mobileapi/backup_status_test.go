package mobileapi

import (
	"path/filepath"
	"testing"
)

// TestBackupStatusReflectsManualBackup mirrors desktop's identical test: the
// Data settings backup status must report "unknown" before any backup
// exists, then move to "healthy" with the backup's own location once
// CreateBackup completes and is verified - specs/backup-and-recovery.md's
// "Manual backup succeeds" scenario ("updates the last verified backup
// status").
func TestBackupStatusReflectsManualBackup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)

	if _, err := service.CreateAccount("create", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	before := decodeJSON[map[string]any](t, must(service.BackupStatus()))
	if before["health"] != "unknown" {
		t.Fatalf("health = %v, want unknown before any backup exists", before["health"])
	}

	backupDestination := filepath.Join(t.TempDir(), "backups")
	backupJSON, err := service.CreateBackup("create-backup", backupDestination)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	backup := decodeJSON[map[string]any](t, backupJSON)

	after := decodeJSON[map[string]any](t, must(service.BackupStatus()))
	if after["health"] != "healthy" {
		t.Fatalf("health = %v, want healthy after a verified manual backup", after["health"])
	}
	if after["location"] != backup["location"] {
		t.Fatalf("location = %v, want %v", after["location"], backup["location"])
	}
	lastVerified, _ := after["last_verified_unix_ms"].(float64)
	if lastVerified <= 0 {
		t.Fatalf("last_verified_unix_ms = %v, want > 0", after["last_verified_unix_ms"])
	}
}
