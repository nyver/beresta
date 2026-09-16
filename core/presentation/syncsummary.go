package presentation

import "time"

// SyncState is the closed set of synchronization states a UI may display,
// per the shared terminology requirement in specs/product-experience:
// synchronization is represented only as local-only, current, active,
// offline, pending, retrying, or action-required.
type SyncState string

const (
	// SyncStateLocalOnly means no transport is configured; all changes
	// stay local until one is.
	SyncStateLocalOnly SyncState = "local_only"
	// SyncStateCurrent means a transport is configured, reachable, and
	// there is no known pending work.
	SyncStateCurrent SyncState = "current"
	// SyncStateActive means a transport is configured and synchronization
	// is actively exchanging work.
	SyncStateActive SyncState = "active"
	// SyncStateOffline means a transport is configured but currently
	// unreachable; local editing continues.
	SyncStateOffline SyncState = "offline"
	// SyncStatePending means a transport is configured and local changes
	// are queued but not yet sent.
	SyncStatePending SyncState = "pending"
	// SyncStateRetrying means a prior attempt failed and Beresta is
	// waiting to retry automatically.
	SyncStateRetrying SyncState = "retrying"
	// SyncStateActionRequired means synchronization cannot proceed
	// without the user taking the summary's ActionRequired recovery
	// action.
	SyncStateActionRequired SyncState = "action_required"
)

// Valid reports whether s is one of the closed SyncState values.
func (s SyncState) Valid() bool {
	switch s {
	case SyncStateLocalOnly, SyncStateCurrent, SyncStateActive, SyncStateOffline,
		SyncStatePending, SyncStateRetrying, SyncStateActionRequired:
		return true
	default:
		return false
	}
}

// SyncSummary is the bounded, locale-free aggregate of synchronization
// facts a UI needs, per the "Shared user terminology and state model" and
// "Layered privacy-preserving diagnostics" requirements in
// specs/product-experience. Pending and unsafe counts come from durable
// repositories rather than transient UI counters; the summary never
// carries note content, cursor, sequence, or other protocol detail.
type SyncSummary struct {
	// State is the current synchronization state.
	State SyncState
	// PendingCount is the durable count of local changes not yet
	// acknowledged by a configured transport.
	PendingCount int
	// LastSuccess is the time of the last successful synchronization
	// round-trip. It is the zero time.Time if synchronization has never
	// succeeded.
	LastSuccess time.Time
	// RetryIn is the remaining backoff before the next automatic retry.
	// It is zero when no retry is pending.
	RetryIn time.Duration
	// UnsafeCount is the durable count of incoming operations quarantined
	// because they failed verification.
	UnsafeCount int
	// ActionRequired is the primary recovery action the UI should offer
	// when State is SyncStateActionRequired. It is RecoveryActionNone
	// otherwise.
	ActionRequired RecoveryAction
}
