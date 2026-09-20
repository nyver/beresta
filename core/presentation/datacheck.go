package presentation

import "time"

// DataCheckIssue is the closed set of plain-language recoverable
// conditions the Advanced "Check my data" action may report, per the
// "Routine maintenance and user data check" requirement in
// specs/product-experience. The action itself decides which internal
// maintenance domain (search index, backup, local database) needs
// attention; the UI only ever sees one issue, never an itemized list.
type DataCheckIssue string

const (
	// DataCheckIssueNone means the check found no problems.
	DataCheckIssueNone DataCheckIssue = "none"
	// DataCheckIssueSearchIndexRepaired means the local search index was
	// found out of step with saved notes and was safely rebuilt during
	// the check; no further user action is needed.
	DataCheckIssueSearchIndexRepaired DataCheckIssue = "search_index_repaired"
	// DataCheckIssueBackupNeedsAttention means the most recent backup
	// failed verification, is overdue, or has never completed, and
	// creating a new backup is the recommended action.
	DataCheckIssueBackupNeedsAttention DataCheckIssue = "backup_needs_attention"
	// DataCheckIssueDatabaseNeedsRestore means local database integrity
	// verification failed and restoring from a backup is the recommended
	// recovery.
	DataCheckIssueDatabaseNeedsRestore DataCheckIssue = "database_needs_restore"
)

// Valid reports whether i is one of the closed DataCheckIssue values.
func (i DataCheckIssue) Valid() bool {
	switch i {
	case DataCheckIssueNone, DataCheckIssueSearchIndexRepaired,
		DataCheckIssueBackupNeedsAttention, DataCheckIssueDatabaseNeedsRestore:
		return true
	default:
		return false
	}
}

// Healthy reports whether i represents a fully healthy result: either no
// problem was found, or one was found and safely repaired in place,
// leaving nothing for the user to do.
func (i DataCheckIssue) Healthy() bool {
	return i == DataCheckIssueNone || i == DataCheckIssueSearchIndexRepaired
}

// DataCheckReport is the bounded, locale-free result of the Advanced
// "Check my data" action: a single consolidated verification pass that
// reports either a healthy result or one actionable summary, never
// individual internal maintenance jobs, per the "Routine maintenance and
// user data check" requirement in specs/product-experience.
type DataCheckReport struct {
	// Issue is the single condition the check found, or
	// DataCheckIssueNone.
	Issue DataCheckIssue
	// CheckedAt is when this check ran.
	CheckedAt time.Time
}
