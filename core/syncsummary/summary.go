// Package syncsummary derives the platform-neutral presentation.SyncSummary
// from live coordinator progress and durable repository counts. It sits
// above core/sync and core/presentation (core/presentation already depends
// on core/transport, which depends on core/sync, so neither of those
// packages can depend back on presentation without an import cycle);
// desktop and core/mobileapi both call Summarize so the seven-state
// precedence rules in specs/sync-engine's "Aggregated synchronization
// state" requirement are defined exactly once.
package syncsummary

import (
	"time"

	"github.com/beresta-app/beresta/core/presentation"
	coresync "github.com/beresta-app/beresta/core/sync"
)

// Inputs bundles the durable and live facts needed to derive a
// presentation.SyncSummary.
type Inputs struct {
	// Configured reports whether a transport is currently configured for
	// this workspace (independent of whether its worker is attached right
	// now - a quarantined worker detaches itself while remaining
	// configured; see coresync.Coordinator.Attach's goroutine).
	Configured bool
	// Progress is the attached coordinator's latest snapshot. Its zero
	// value is used when no coordinator is attached.
	Progress coresync.CoordinatorProgress
	// PendingCount is the durable count of local changes not yet
	// acknowledged by the configured transport.
	PendingCount int
	// UnsafeCount is the durable count of incoming operations quarantined
	// because they failed verification.
	UnsafeCount int
	// Now is the current time, used to compute the remaining retry
	// backoff from Progress.RetryDeadline.
	Now time.Time
}

// networkErrorClasses are the classifySyncError results (see
// core/sync/worker.go) that indicate the configured transport itself is
// unreachable, as opposed to a failure that does not bear on connectivity
// (such as a protocol version mismatch).
var networkErrorClasses = map[string]bool{
	"timeout":             true,
	"transient_transport": true,
}

// Summarize derives the platform-neutral presentation.SyncSummary from in.
// State precedence: an unconfigured transport always reports local-only; a
// durable unsafe entry always requires user action regardless of the
// worker's current phase, because a quarantined operation blocks the
// cursor until the user acts; a worker backing off after a failed attempt
// reports offline (connectivity error classes) or retrying (other
// transient classes); a worker actively exchanging work reports active;
// otherwise the summary reports pending or current depending on whether
// durable outbound work remains queued.
func Summarize(in Inputs) presentation.SyncSummary {
	summary := presentation.SyncSummary{
		PendingCount:   in.PendingCount,
		LastSuccess:    in.Progress.LastSuccess,
		UnsafeCount:    in.UnsafeCount,
		ActionRequired: presentation.RecoveryActionNone,
	}
	if !in.Configured {
		summary.State = presentation.SyncStateLocalOnly
		return summary
	}
	if in.UnsafeCount > 0 {
		summary.State = presentation.SyncStateActionRequired
		summary.ActionRequired = presentation.RecoveryActionReviewUnsafeChange
		return summary
	}
	switch in.Progress.Phase {
	case coresync.PhaseBackoff:
		if retryIn := in.Progress.RetryDeadline.Sub(in.Now); retryIn > 0 {
			summary.RetryIn = retryIn
		}
		if networkErrorClasses[in.Progress.ErrorClass] {
			summary.State = presentation.SyncStateOffline
		} else {
			summary.State = presentation.SyncStateRetrying
		}
	case coresync.PhasePull, coresync.PhaseApply, coresync.PhasePush:
		summary.State = presentation.SyncStateActive
	default:
		if in.PendingCount > 0 {
			summary.State = presentation.SyncStatePending
		} else {
			summary.State = presentation.SyncStateCurrent
		}
	}
	return summary
}
