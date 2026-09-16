package presentation

import "time"

// BackupHealth is the closed set of plain-language backup health values a
// UI may display, per the "Crash-Safe Data Operations" backup status
// requirement.
type BackupHealth string

const (
	// BackupHealthHealthy means the most recent backup was verified and
	// is usable for restore.
	BackupHealthHealthy BackupHealth = "healthy"
	// BackupHealthWarning means a backup exists but verification found a
	// recoverable concern, such as an overdue rotation.
	BackupHealthWarning BackupHealth = "warning"
	// BackupHealthCorrupt means the most recent backup failed
	// verification and is not usable for restore.
	BackupHealthCorrupt BackupHealth = "corrupt"
	// BackupHealthUnknown means no backup has completed yet or its health
	// has not been verified.
	BackupHealthUnknown BackupHealth = "unknown"
)

// Valid reports whether h is one of the closed BackupHealth values.
func (h BackupHealth) Valid() bool {
	switch h {
	case BackupHealthHealthy, BackupHealthWarning, BackupHealthCorrupt, BackupHealthUnknown:
		return true
	default:
		return false
	}
}

// BackupStatus is the bounded, locale-free backup status a UI needs.
// Location is a display-bound destination description (for example, a
// folder label or configured target name), never a full filesystem path
// carrying user identifiers beyond what the user themselves configured.
type BackupStatus struct {
	// Health is the plain-language health of the most recent backup.
	Health BackupHealth
	// LastVerified is the time the most recent backup last passed
	// verification. It is the zero time.Time if no backup has been
	// verified yet.
	LastVerified time.Time
	// Location is a bounded, display-safe backup destination description.
	Location string
}
