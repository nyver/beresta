package presentation

import "time"

// DatabaseHealth is the closed set of plain-language local-database
// health values reported in DiagnosticSummary.
type DatabaseHealth string

const (
	// DatabaseHealthOK means the local database opened and passed its
	// routine integrity check.
	DatabaseHealthOK DatabaseHealth = "ok"
	// DatabaseHealthDegraded means the local database is usable but a
	// routine check found a recoverable concern.
	DatabaseHealthDegraded DatabaseHealth = "degraded"
	// DatabaseHealthUnknown means no routine check has completed yet.
	DatabaseHealthUnknown DatabaseHealth = "unknown"
)

// Valid reports whether h is one of the closed DatabaseHealth values.
func (h DatabaseHealth) Valid() bool {
	switch h {
	case DatabaseHealthOK, DatabaseHealthDegraded, DatabaseHealthUnknown:
		return true
	default:
		return false
	}
}

// UpdateStatus is the closed set of plain-language application update
// states reported in DiagnosticSummary.
type UpdateStatus string

const (
	// UpdateStatusUpToDate means no newer signed update is available.
	UpdateStatusUpToDate UpdateStatus = "up_to_date"
	// UpdateStatusAvailable means a newer signed update was found but has
	// not started downloading.
	UpdateStatusAvailable UpdateStatus = "available"
	// UpdateStatusDownloading means a newer signed update is being
	// staged.
	UpdateStatusDownloading UpdateStatus = "downloading"
	// UpdateStatusReadyToInstall means a staged update is verified and
	// waiting for the user to restart into it.
	UpdateStatusReadyToInstall UpdateStatus = "ready_to_install"
	// UpdateStatusFailed means the most recent update attempt did not
	// complete.
	UpdateStatusFailed UpdateStatus = "failed"
	// UpdateStatusUnknown means update state has not been checked yet.
	UpdateStatusUnknown UpdateStatus = "unknown"
)

// Valid reports whether s is one of the closed UpdateStatus values.
func (s UpdateStatus) Valid() bool {
	switch s {
	case UpdateStatusUpToDate, UpdateStatusAvailable, UpdateStatusDownloading,
		UpdateStatusReadyToInstall, UpdateStatusFailed, UpdateStatusUnknown:
		return true
	default:
		return false
	}
}

// DiagnosticSummary is the bounded, locale-free set of facts the user
// diagnostics view reports, per the "Layered privacy-preserving
// diagnostics" requirement in specs/product-experience: app version,
// platform, whether sync is configured, last successful sync, pending
// count, connection state, backup status, storage usage, high-level
// database health, and update status. It never carries note content,
// titles, search queries, sensitive attachment names, passwords, keys,
// tokens, invite codes, plaintext exports, or clipboard content; workspace
// and device identifiers, sanitized codes, cursor state, retry counters,
// transport details, and migration version belong only in a separate
// technical-details schema, never here.
type DiagnosticSummary struct {
	// AppVersion is the running application's display version.
	AppVersion string
	// Platform is the running platform ("windows" or "android").
	Platform string
	// SyncConfigured is true when a transport is configured, regardless
	// of its current reachability.
	SyncConfigured bool
	// LastSuccessfulSync is the time of the last successful
	// synchronization round-trip. It is the zero time.Time if
	// synchronization has never succeeded.
	LastSuccessfulSync time.Time
	// PendingCount is the durable count of local changes not yet
	// acknowledged by a configured transport.
	PendingCount int
	// ConnectionState is the current synchronization state.
	ConnectionState SyncState
	// Backup is the current backup status.
	Backup BackupStatus
	// StorageUsageBytes is the total local storage used by the account,
	// including the database and attachment cache.
	StorageUsageBytes int64
	// Database is the high-level local-database health.
	Database DatabaseHealth
	// Update is the current application update status.
	Update UpdateStatus
}
