// Package backupsummary derives the platform-neutral presentation.BackupStatus
// from the local backup catalog. It sits above core/store and
// core/presentation (core/presentation already depends on core/transport,
// which depends on core/account, so core/account cannot depend back on
// presentation without an import cycle - see core/syncsummary for the same
// situation on the synchronization side); desktop and core/mobileapi both
// call Summarize so the "Understandable verified backup status" requirement
// (specs/backup-and-recovery.md) is derived exactly once.
package backupsummary

import (
	"time"

	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/core/store"
)

// Summarize derives the presentation.BackupStatus for the most recently
// published routine backup among dailyBackups and manualBackups - the kinds
// a user schedules or requests, not the automatic pre_migration/pre_restore
// safety copies a specific mutating operation creates for itself - so that a
// manual backup's completion moves the same status a scheduled daily backup
// would (specs/backup-and-recovery.md, "Manual backup succeeds": "updates
// the last verified backup status"). Both slices must already be ordered
// newest-first, matching store.ListBackups. It reports
// presentation.BackupHealthUnknown, never a fabricated health, when neither
// slice has an entry yet.
func Summarize(dailyBackups, manualBackups []store.Backup) presentation.BackupStatus {
	latest, ok := latestOf(dailyBackups, manualBackups)
	if !ok {
		return presentation.BackupStatus{Health: presentation.BackupHealthUnknown}
	}

	status := presentation.BackupStatus{Location: latest.Location}
	switch {
	case latest.Corrupt:
		status.Health = presentation.BackupHealthCorrupt
	case latest.VerifiedUnixMS != nil:
		status.Health = presentation.BackupHealthHealthy
		status.LastVerified = time.UnixMilli(*latest.VerifiedUnixMS)
	default:
		status.Health = presentation.BackupHealthUnknown
	}
	return status
}

// latestOf returns the newest entry across a and b (each already
// newest-first) and whether either slice had one at all.
func latestOf(a, b []store.Backup) (store.Backup, bool) {
	var latest store.Backup
	found := false
	for _, list := range [][]store.Backup{a, b} {
		if len(list) == 0 {
			continue
		}
		if !found || list[0].CreatedUnixMS > latest.CreatedUnixMS {
			latest = list[0]
			found = true
		}
	}
	return latest, found
}
