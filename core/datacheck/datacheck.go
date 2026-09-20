// Package datacheck derives the platform-neutral presentation.DataCheckReport
// for the Advanced "Check my data" action from core/account's raw
// DataCheckResult and the account's current backup catalog. It sits above
// core/store and core/presentation, mirroring core/backupsummary and
// core/syncsummary: core/presentation already depends on core/transport,
// which depends on core/account, so core/account cannot depend back on
// presentation without an import cycle. Desktop and core/mobileapi both call
// Summarize so the "Routine maintenance and user data check" requirement
// (specs/product-experience) is derived exactly once.
package datacheck

import (
	"time"

	"github.com/beresta-app/beresta/core/backupsummary"
	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/core/store"
)

// Summarize derives the presentation.DataCheckReport the Advanced "Check my
// data" action displays from core/account.Account.RunDataCheck's raw
// integrityOK/searchIndexRepaired outcome and the account's current backup
// catalog - dailyBackups and manualBackups, both already newest-first,
// matching store.ListBackups and core/backupsummary.Summarize. Problems are
// checked in order of severity: local database integrity first, then
// backup health, then search-index repair, which needs no user action
// since RunDataCheck already fixed it.
func Summarize(integrityOK, searchIndexRepaired bool, dailyBackups, manualBackups []store.Backup, checkedAt time.Time) presentation.DataCheckReport {
	if !integrityOK {
		return presentation.DataCheckReport{Issue: presentation.DataCheckIssueDatabaseNeedsRestore, CheckedAt: checkedAt}
	}

	switch backupsummary.Summarize(dailyBackups, manualBackups).Health {
	case presentation.BackupHealthCorrupt, presentation.BackupHealthWarning, presentation.BackupHealthUnknown:
		return presentation.DataCheckReport{Issue: presentation.DataCheckIssueBackupNeedsAttention, CheckedAt: checkedAt}
	}

	if searchIndexRepaired {
		return presentation.DataCheckReport{Issue: presentation.DataCheckIssueSearchIndexRepaired, CheckedAt: checkedAt}
	}
	return presentation.DataCheckReport{Issue: presentation.DataCheckIssueNone, CheckedAt: checkedAt}
}
