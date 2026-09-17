package syncsummary

import (
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/presentation"
	coresync "github.com/beresta-app/beresta/core/sync"
)

func TestSummarizeUnconfiguredIsLocalOnly(t *testing.T) {
	summary := Summarize(Inputs{Configured: false, PendingCount: 3})
	if summary.State != presentation.SyncStateLocalOnly {
		t.Fatalf("state = %q, want local_only", summary.State)
	}
	if summary.ActionRequired != presentation.RecoveryActionNone {
		t.Fatalf("action required = %q, want none", summary.ActionRequired)
	}
}

func TestSummarizeUnsafeCountIsActionRequiredRegardlessOfPhase(t *testing.T) {
	summary := Summarize(Inputs{
		Configured:   true,
		UnsafeCount:  1,
		PendingCount: 5,
		Progress:     coresync.CoordinatorProgress{Phase: coresync.PhasePull},
	})
	if summary.State != presentation.SyncStateActionRequired {
		t.Fatalf("state = %q, want action_required", summary.State)
	}
	if summary.ActionRequired != presentation.RecoveryActionReviewUnsafeChange {
		t.Fatalf("action required = %q, want review_unsafe_change", summary.ActionRequired)
	}
	if summary.UnsafeCount != 1 {
		t.Fatalf("unsafe count = %d, want 1", summary.UnsafeCount)
	}
}

func TestSummarizeActivePhasesReportActive(t *testing.T) {
	for _, phase := range []coresync.Phase{coresync.PhasePull, coresync.PhaseApply, coresync.PhasePush} {
		summary := Summarize(Inputs{Configured: true, Progress: coresync.CoordinatorProgress{Phase: phase}})
		if summary.State != presentation.SyncStateActive {
			t.Fatalf("phase %q: state = %q, want active", phase, summary.State)
		}
	}
}

func TestSummarizeBackoffWithNetworkErrorClassIsOffline(t *testing.T) {
	now := time.Now()
	summary := Summarize(Inputs{
		Configured: true,
		Progress: coresync.CoordinatorProgress{
			Phase:         coresync.PhaseBackoff,
			ErrorClass:    "transient_transport",
			RetryDeadline: now.Add(5 * time.Second),
		},
		Now: now,
	})
	if summary.State != presentation.SyncStateOffline {
		t.Fatalf("state = %q, want offline", summary.State)
	}
	if summary.RetryIn <= 0 || summary.RetryIn > 5*time.Second {
		t.Fatalf("retry in = %v, want ~5s", summary.RetryIn)
	}
}

func TestSummarizeBackoffWithOtherErrorClassIsRetrying(t *testing.T) {
	now := time.Now()
	summary := Summarize(Inputs{
		Configured: true,
		Progress: coresync.CoordinatorProgress{
			Phase:         coresync.PhaseBackoff,
			ErrorClass:    "unsupported_version",
			RetryDeadline: now.Add(2 * time.Second),
		},
		Now: now,
	})
	if summary.State != presentation.SyncStateRetrying {
		t.Fatalf("state = %q, want retrying", summary.State)
	}
	if summary.RetryIn <= 0 {
		t.Fatalf("retry in = %v, want > 0", summary.RetryIn)
	}
}

// TestSummarizeBackoffWithTrustErrorClassIsActionRequired covers task 3.8's
// "TLS identity change" regression (specs/release-quality's "TLS identity
// changes" scenario: "trust action is requested"): a certificate/auth
// trust-boundary failure must surface as actionable review_connection, not
// as an ordinary offline/retrying state a user would dismiss as a routine
// network hiccup that resolves itself.
func TestSummarizeBackoffWithTrustErrorClassIsActionRequired(t *testing.T) {
	now := time.Now()
	summary := Summarize(Inputs{
		Configured: true,
		Progress: coresync.CoordinatorProgress{
			Phase:         coresync.PhaseBackoff,
			ErrorClass:    "trust_or_configuration",
			RetryDeadline: now.Add(5 * time.Second),
		},
		Now: now,
	})
	if summary.State != presentation.SyncStateActionRequired {
		t.Fatalf("state = %q, want action_required", summary.State)
	}
	if summary.ActionRequired != presentation.RecoveryActionReviewConnection {
		t.Fatalf("action required = %q, want review_connection", summary.ActionRequired)
	}
	if summary.RetryIn != 0 {
		t.Fatalf("retry in = %v, want 0 - a trust failure is not an automatic retry", summary.RetryIn)
	}
}

func TestSummarizeBackoffPastDeadlineReportsNoRetryIn(t *testing.T) {
	now := time.Now()
	summary := Summarize(Inputs{
		Configured: true,
		Progress: coresync.CoordinatorProgress{
			Phase:         coresync.PhaseBackoff,
			ErrorClass:    "timeout",
			RetryDeadline: now.Add(-time.Second),
		},
		Now: now,
	})
	if summary.RetryIn != 0 {
		t.Fatalf("retry in = %v, want 0 once the deadline has passed", summary.RetryIn)
	}
}

func TestSummarizeIdleWithPendingWorkIsPending(t *testing.T) {
	summary := Summarize(Inputs{
		Configured:   true,
		PendingCount: 4,
		Progress:     coresync.CoordinatorProgress{Phase: coresync.PhaseCurrent},
	})
	if summary.State != presentation.SyncStatePending {
		t.Fatalf("state = %q, want pending", summary.State)
	}
	if summary.PendingCount != 4 {
		t.Fatalf("pending count = %d, want 4", summary.PendingCount)
	}
}

func TestSummarizeIdleWithNoPendingWorkIsCurrent(t *testing.T) {
	lastSuccess := time.Now().Add(-time.Minute)
	summary := Summarize(Inputs{
		Configured: true,
		Progress:   coresync.CoordinatorProgress{Phase: coresync.PhaseCurrent, LastSuccess: lastSuccess},
	})
	if summary.State != presentation.SyncStateCurrent {
		t.Fatalf("state = %q, want current", summary.State)
	}
	if !summary.LastSuccess.Equal(lastSuccess) {
		t.Fatalf("last success = %v, want %v", summary.LastSuccess, lastSuccess)
	}
}

func TestSummarizeNeverRunWithoutPendingWorkIsCurrent(t *testing.T) {
	// A freshly attached coordinator that has not yet reported any Progress
	// (zero-value Phase) must not be mistaken for a permanently stuck
	// state - it settles into current/pending exactly like PhaseCurrent.
	summary := Summarize(Inputs{Configured: true})
	if summary.State != presentation.SyncStateCurrent {
		t.Fatalf("state = %q, want current", summary.State)
	}
}
