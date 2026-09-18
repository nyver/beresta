package backupsummary

import (
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/core/store"
)

func TestSummarizeNoBackupsIsUnknown(t *testing.T) {
	status := Summarize(nil, nil)
	if status.Health != presentation.BackupHealthUnknown {
		t.Fatalf("health = %q, want unknown", status.Health)
	}
	if !status.LastVerified.IsZero() {
		t.Fatalf("last verified = %v, want zero", status.LastVerified)
	}
}

func TestSummarizeVerifiedDailyBackupIsHealthy(t *testing.T) {
	verifiedMS := int64(1_700_000_000_000)
	daily := []store.Backup{{Location: "/backups/daily-1", VerifiedUnixMS: &verifiedMS}}

	status := Summarize(daily, nil)

	if status.Health != presentation.BackupHealthHealthy {
		t.Fatalf("health = %q, want healthy", status.Health)
	}
	if !status.LastVerified.Equal(time.UnixMilli(verifiedMS)) {
		t.Fatalf("last verified = %v, want %v", status.LastVerified, time.UnixMilli(verifiedMS))
	}
	if status.Location != "/backups/daily-1" {
		t.Fatalf("location = %q, want /backups/daily-1", status.Location)
	}
}

func TestSummarizeUnverifiedBackupIsUnknown(t *testing.T) {
	status := Summarize([]store.Backup{{Location: "/backups/daily-1"}}, nil)
	if status.Health != presentation.BackupHealthUnknown {
		t.Fatalf("health = %q, want unknown", status.Health)
	}
}

func TestSummarizeCorruptBackupIsCorruptEvenIfPreviouslyVerified(t *testing.T) {
	verifiedMS := int64(1_700_000_000_000)
	status := Summarize([]store.Backup{{Location: "/backups/daily-1", VerifiedUnixMS: &verifiedMS, Corrupt: true}}, nil)
	if status.Health != presentation.BackupHealthCorrupt {
		t.Fatalf("health = %q, want corrupt", status.Health)
	}
}

// TestSummarizeManualBackupUpdatesStatus proves a manual backup moves the
// displayed status exactly like a scheduled daily one, per
// specs/backup-and-recovery.md's "Manual backup succeeds" scenario ("...
// updates the last verified backup status"): a manual backup newer than the
// latest daily backup must be the one reflected, not silently ignored
// because it is not a daily backup.
func TestSummarizeManualBackupUpdatesStatus(t *testing.T) {
	dailyVerifiedMS := int64(1_700_000_000_000)
	manualVerifiedMS := int64(1_700_000_500_000)
	daily := []store.Backup{{Location: "/backups/daily-1", CreatedUnixMS: 1_700_000_000_000, VerifiedUnixMS: &dailyVerifiedMS}}
	manual := []store.Backup{{Location: "/backups/manual-1", CreatedUnixMS: 1_700_000_500_000, VerifiedUnixMS: &manualVerifiedMS}}

	status := Summarize(daily, manual)

	if status.Location != "/backups/manual-1" {
		t.Fatalf("location = %q, want /backups/manual-1", status.Location)
	}
	if !status.LastVerified.Equal(time.UnixMilli(manualVerifiedMS)) {
		t.Fatalf("last verified = %v, want %v", status.LastVerified, time.UnixMilli(manualVerifiedMS))
	}
}

func TestSummarizeOlderManualBackupDoesNotOverrideNewerDaily(t *testing.T) {
	dailyVerifiedMS := int64(1_700_000_500_000)
	manualVerifiedMS := int64(1_700_000_000_000)
	daily := []store.Backup{{Location: "/backups/daily-1", CreatedUnixMS: 1_700_000_500_000, VerifiedUnixMS: &dailyVerifiedMS}}
	manual := []store.Backup{{Location: "/backups/manual-1", CreatedUnixMS: 1_700_000_000_000, VerifiedUnixMS: &manualVerifiedMS}}

	status := Summarize(daily, manual)

	if status.Location != "/backups/daily-1" {
		t.Fatalf("location = %q, want /backups/daily-1", status.Location)
	}
}
