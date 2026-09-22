package datacheck

import (
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/core/store"
)

// task 11.7: this package (task 7.10's shared "Check my data" derivation,
// called by both desktop and core/mobileapi) had zero test coverage - the
// only core/* directory found with no _test.go file at all while auditing
// coverage for this task. Summarize is a pure function over already-loaded
// values (no store.Executor, no CGO/SQLCipher dependency despite the
// package's transitive import of core/store for the store.Backup type), so
// unlike almost every other core/account/core/store-backed test in this
// codebase, this one runs and is verified in every environment, including
// this sandbox's CGO_ENABLED=0 one.

func TestSummarizeIntegrityFailureReportsDatabaseNeedsRestoreRegardlessOfEverythingElse(t *testing.T) {
	checkedAt := time.UnixMilli(1_700_000_000_000)
	verifiedMS := checkedAt.UnixMilli()
	healthyBackup := []store.Backup{{Location: "/backups/daily-1", VerifiedUnixMS: &verifiedMS}}

	// Integrity failure must win even when backups are healthy and the
	// search index was also repaired - it is checked first because it is
	// the most severe condition (a corrupt local database), per
	// Summarize's own doc comment on severity ordering.
	got := Summarize(false, true, healthyBackup, nil, checkedAt)

	want := presentation.DataCheckReport{Issue: presentation.DataCheckIssueDatabaseNeedsRestore, CheckedAt: checkedAt}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestSummarizeNoBackupsReportsBackupNeedsAttention(t *testing.T) {
	checkedAt := time.UnixMilli(1_700_000_000_000)
	got := Summarize(true, false, nil, nil, checkedAt)
	want := presentation.DataCheckReport{Issue: presentation.DataCheckIssueBackupNeedsAttention, CheckedAt: checkedAt}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestSummarizeCorruptBackupReportsBackupNeedsAttentionEvenIfSearchIndexWasRepaired(t *testing.T) {
	checkedAt := time.UnixMilli(1_700_000_000_000)
	verifiedMS := checkedAt.UnixMilli()
	corrupt := []store.Backup{{Location: "/backups/daily-1", VerifiedUnixMS: &verifiedMS, Corrupt: true}}

	// Backup health is checked before search-index repair (Summarize's
	// severity ordering): a corrupt backup needing a new one is more
	// actionable than an already-fixed index needing nothing.
	got := Summarize(true, true, corrupt, nil, checkedAt)

	want := presentation.DataCheckReport{Issue: presentation.DataCheckIssueBackupNeedsAttention, CheckedAt: checkedAt}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestSummarizeHealthyBackupWithRepairedSearchIndexReportsSearchIndexRepaired(t *testing.T) {
	checkedAt := time.UnixMilli(1_700_000_000_000)
	verifiedMS := checkedAt.UnixMilli()
	healthy := []store.Backup{{Location: "/backups/daily-1", VerifiedUnixMS: &verifiedMS}}

	got := Summarize(true, true, healthy, nil, checkedAt)

	want := presentation.DataCheckReport{Issue: presentation.DataCheckIssueSearchIndexRepaired, CheckedAt: checkedAt}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestSummarizeHealthyBackupAndIntactSearchIndexReportsNoIssue(t *testing.T) {
	checkedAt := time.UnixMilli(1_700_000_000_000)
	verifiedMS := checkedAt.UnixMilli()
	healthy := []store.Backup{{Location: "/backups/daily-1", VerifiedUnixMS: &verifiedMS}}

	got := Summarize(true, false, healthy, nil, checkedAt)

	want := presentation.DataCheckReport{Issue: presentation.DataCheckIssueNone, CheckedAt: checkedAt}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}

func TestSummarizeConsidersAManualBackupThatIsHealthierThanTheDailyOne(t *testing.T) {
	checkedAt := time.UnixMilli(1_700_000_000_000)
	manualVerifiedMS := checkedAt.UnixMilli()
	manual := []store.Backup{{Location: "/backups/manual-1", CreatedUnixMS: checkedAt.UnixMilli(), VerifiedUnixMS: &manualVerifiedMS}}
	unverifiedDaily := []store.Backup{{Location: "/backups/daily-1"}}

	// The most recent backup across both daily and manual sets is what
	// backupsummary.Summarize (and therefore this function) reflects - an
	// unverified daily backup alongside a verified manual one must not
	// report BackupNeedsAttention, matching core/backupsummary's own
	// "manual backup updates status" guarantee.
	got := Summarize(true, false, unverifiedDaily, manual, checkedAt)

	want := presentation.DataCheckReport{Issue: presentation.DataCheckIssueNone, CheckedAt: checkedAt}
	if got != want {
		t.Fatalf("Summarize() = %+v, want %+v", got, want)
	}
}
