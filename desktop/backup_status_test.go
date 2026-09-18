package main

import (
	"testing"

	"github.com/beresta-app/beresta/core/presentation"
)

// TestBackupStatusReflectsManualBackup covers task 5.4's Data settings
// backup status surface: it must report BackupHealthUnknown before any
// backup exists, then move to healthy with the manual backup's own location
// once CreateManualBackup completes and is verified -
// specs/backup-and-recovery.md's "Manual backup succeeds" scenario ("updates
// the last verified backup status").
func TestBackupStatusReflectsManualBackup(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	before, err := a.BackupStatus()
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	if before.Health != presentation.BackupHealthUnknown {
		t.Fatalf("Health = %q, want unknown before any backup exists", before.Health)
	}

	backupRoot := t.TempDir()
	backup, err := a.CreateManualBackup(backupRoot)
	if err != nil {
		t.Fatalf("CreateManualBackup: %v", err)
	}

	after, err := a.BackupStatus()
	if err != nil {
		t.Fatalf("BackupStatus: %v", err)
	}
	if after.Health != presentation.BackupHealthHealthy {
		t.Fatalf("Health = %q, want healthy after a verified manual backup", after.Health)
	}
	if after.Location != backup.Location {
		t.Fatalf("Location = %q, want %q", after.Location, backup.Location)
	}
	if after.LastVerifiedUnixMS <= 0 {
		t.Fatalf("LastVerifiedUnixMS = %d, want > 0", after.LastVerifiedUnixMS)
	}
}
