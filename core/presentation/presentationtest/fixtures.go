// Package presentationtest provides one shared set of named
// presentation.* fixtures so every platform adapter's parity and
// compatibility tests exercise the identical semantic states, instead of
// each test file inventing its own values that can drift apart (see
// design.md decision 1: "A contract test will feed common fixtures
// through both adapters and assert equivalent semantic output").
package presentationtest

import (
	"time"

	"github.com/beresta-app/beresta/core/presentation"
)

// referenceTime is the fixed timestamp every fixture with a non-zero time
// field uses, so parity assertions compare stable values across test runs.
var referenceTime = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// SyncCase names one reusable presentation.SyncSummary fixture.
type SyncCase struct {
	Name    string
	Summary presentation.SyncSummary
}

// SyncCases returns one named fixture per closed presentation.SyncState
// value, covering every state a UI can display: local-only, current,
// active, offline, pending, retrying, and action-required.
func SyncCases() []SyncCase {
	return []SyncCase{
		{
			Name:    "local_only",
			Summary: presentation.SyncSummary{State: presentation.SyncStateLocalOnly},
		},
		{
			Name:    "current",
			Summary: presentation.SyncSummary{State: presentation.SyncStateCurrent, LastSuccess: referenceTime},
		},
		{
			Name:    "active",
			Summary: presentation.SyncSummary{State: presentation.SyncStateActive, PendingCount: 2, LastSuccess: referenceTime},
		},
		{
			Name: "offline",
			Summary: presentation.SyncSummary{
				State: presentation.SyncStateOffline, PendingCount: 3, LastSuccess: referenceTime,
			},
		},
		{
			Name:    "pending",
			Summary: presentation.SyncSummary{State: presentation.SyncStatePending, PendingCount: 5, LastSuccess: referenceTime},
		},
		{
			Name: "retrying",
			Summary: presentation.SyncSummary{
				State: presentation.SyncStateRetrying, PendingCount: 5, RetryIn: 8 * time.Second, LastSuccess: referenceTime,
			},
		},
		{
			Name: "action_required",
			Summary: presentation.SyncSummary{
				State: presentation.SyncStateActionRequired, PendingCount: 1, UnsafeCount: 1,
				ActionRequired: presentation.RecoveryActionReviewUnsafeChange, LastSuccess: referenceTime,
			},
		},
	}
}

// LocalSaveCase names one reusable presentation.LocalSaveState fixture.
type LocalSaveCase struct {
	Name  string
	State presentation.LocalSaveState
}

// LocalSaveCases returns one named fixture per closed
// presentation.LocalSaveState value: saving, saved, and could-not-save.
func LocalSaveCases() []LocalSaveCase {
	return []LocalSaveCase{
		{Name: "saving", State: presentation.SaveStateSaving},
		{Name: "saved", State: presentation.SaveStateSaved},
		{Name: "could_not_save", State: presentation.SaveStateCouldNotSave},
	}
}
