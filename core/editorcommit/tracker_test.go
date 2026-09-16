package editorcommit

import (
	"sync"
	"testing"

	"github.com/beresta-app/beresta/core/presentation"
)

func TestDirtyIssuesStrictlyIncreasingGenerations(t *testing.T) {
	var tracker Tracker
	var previous Generation
	for i := 0; i < 5; i++ {
		current := tracker.Dirty()
		if current <= previous {
			t.Fatalf("Dirty() = %d, want strictly greater than previous %d", current, previous)
		}
		previous = current
	}
}

// TestAcceptZeroGenerationFailureIsNotDiscarded covers a real bug found
// while wiring this tracker into desktop's editor commit controller: a
// commit with no prior Dirty call (for example, a title-only rename
// commit before any body edit ever happened) targets the zero
// Generation. That is a legitimate first commit attempt, not a stale or
// already-resolved duplicate, so its failure must still be accepted.
func TestAcceptZeroGenerationFailureIsNotDiscarded(t *testing.T) {
	var tracker Tracker
	state, accepted := tracker.Accept(0, false)
	if !accepted || state != presentation.SaveStateCouldNotSave {
		t.Fatalf("Accept(0, false) = (%q, %v), want (%q, true)", state, accepted, presentation.SaveStateCouldNotSave)
	}
}

func TestAcceptZeroGenerationSuccessIsNotDiscarded(t *testing.T) {
	var tracker Tracker
	state, accepted := tracker.Accept(0, true)
	if !accepted || state != presentation.SaveStateSaved {
		t.Fatalf("Accept(0, true) = (%q, %v), want (%q, true)", state, accepted, presentation.SaveStateSaved)
	}
}

func TestLatestDoesNotMarkDirty(t *testing.T) {
	var tracker Tracker
	g := tracker.Dirty()
	if got := tracker.Latest(); got != g {
		t.Fatalf("Latest() = %d, want %d", got, g)
	}
	if got := tracker.Latest(); got != g {
		t.Fatalf("second Latest() = %d, want %d (Latest must not itself mark dirty)", got, g)
	}
}

func TestAcceptLatestGenerationSuccessReportsSaved(t *testing.T) {
	var tracker Tracker
	g := tracker.Dirty()
	state, accepted := tracker.Accept(g, true)
	if !accepted {
		t.Fatal("Accept() accepted = false, want true for the latest generation")
	}
	if state != presentation.SaveStateSaved {
		t.Fatalf("state = %q, want %q", state, presentation.SaveStateSaved)
	}
}

func TestAcceptLatestGenerationFailureReportsCouldNotSave(t *testing.T) {
	var tracker Tracker
	g := tracker.Dirty()
	state, accepted := tracker.Accept(g, false)
	if !accepted {
		t.Fatal("Accept() accepted = false, want true for the latest generation")
	}
	if state != presentation.SaveStateCouldNotSave {
		t.Fatalf("state = %q, want %q", state, presentation.SaveStateCouldNotSave)
	}
}

// TestAcceptStaleSuccessCannotMarkNewerDirtyContentAsSaved is the
// scenario task 1's design decision 2 requires: a slow commit for an
// older generation must not report Saved once newer, still-unsaved input
// has arrived, even though that older commit itself succeeded.
func TestAcceptStaleSuccessCannotMarkNewerDirtyContentAsSaved(t *testing.T) {
	var tracker Tracker
	stale := tracker.Dirty() // generation 1: the slow in-flight commit
	tracker.Dirty()          // generation 2: newer input arrives first

	state, accepted := tracker.Accept(stale, true)
	if accepted {
		t.Fatalf("Accept(stale, true) accepted = true, want false: a stale completion must never report a status (got state %q)", state)
	}
	if state != "" {
		t.Fatalf("state = %q, want empty for a discarded stale completion", state)
	}
}

func TestAcceptStaleFailureIsDiscardedNotShownAsCouldNotSave(t *testing.T) {
	var tracker Tracker
	stale := tracker.Dirty()
	tracker.Dirty()

	state, accepted := tracker.Accept(stale, false)
	if accepted {
		t.Fatalf("Accept(stale, false) accepted = true, want false (got state %q)", state)
	}
	if state != "" {
		t.Fatalf("state = %q, want empty for a discarded stale completion", state)
	}
}

func TestAcceptLatestGenerationAfterStaleCompletionStillReportsCorrectly(t *testing.T) {
	var tracker Tracker
	stale := tracker.Dirty()
	latest := tracker.Dirty()

	if _, accepted := tracker.Accept(stale, true); accepted {
		t.Fatal("stale completion must be discarded")
	}
	state, accepted := tracker.Accept(latest, true)
	if !accepted {
		t.Fatal("Accept(latest, true) accepted = false, want true")
	}
	if state != presentation.SaveStateSaved {
		t.Fatalf("state = %q, want %q", state, presentation.SaveStateSaved)
	}
}

func TestAcceptDuplicateSuccessForSameGenerationIsIdempotent(t *testing.T) {
	var tracker Tracker
	g := tracker.Dirty()

	state, accepted := tracker.Accept(g, true)
	if !accepted || state != presentation.SaveStateSaved {
		t.Fatalf("first Accept(success) = (%q, %v), want (%q, true)", state, accepted, presentation.SaveStateSaved)
	}
	state, accepted = tracker.Accept(g, true)
	if !accepted || state != presentation.SaveStateSaved {
		t.Fatalf("duplicate Accept(success) = (%q, %v), want (%q, true): a repeat success for the same generation is harmless and must re-affirm Saved", state, accepted, presentation.SaveStateSaved)
	}
}

func TestAcceptFailureCannotRegressAnAlreadySavedGeneration(t *testing.T) {
	// A retry legitimately reuses the same generation number (no new
	// Dirty call happens between a failed attempt and its retry, since
	// the retried content is unchanged) and must be able to move status
	// from CouldNotSave to Saved - see the next test. But once any
	// attempt has confirmed a generation saved, a late or duplicate
	// failure for that same generation must never regress the display
	// back to CouldNotSave.
	var tracker Tracker
	g := tracker.Dirty()

	if _, accepted := tracker.Accept(g, true); !accepted {
		t.Fatal("first Accept(success) accepted = false, want true")
	}
	state, accepted := tracker.Accept(g, false)
	if accepted {
		t.Fatalf("late failure for an already-saved generation: accepted = true, want false (got state %q)", state)
	}
}

func TestAcceptRetrySameGenerationAfterFailureCanStillSucceed(t *testing.T) {
	// The common retry path: a commit fails, its payload is requeued for
	// the next attempt, and since nothing new was typed in between, the
	// retry captures the exact same generation as the failed attempt.
	// That retry's eventual success must be accepted, not discarded as a
	// stale or duplicate completion.
	var tracker Tracker
	g := tracker.Dirty()

	state, accepted := tracker.Accept(g, false)
	if !accepted || state != presentation.SaveStateCouldNotSave {
		t.Fatalf("first Accept(failure) = (%q, %v), want (%q, true)", state, accepted, presentation.SaveStateCouldNotSave)
	}
	state, accepted = tracker.Accept(g, true)
	if !accepted || state != presentation.SaveStateSaved {
		t.Fatalf("retry Accept(success) = (%q, %v), want (%q, true)", state, accepted, presentation.SaveStateSaved)
	}
}

func TestAcceptOutOfOrderCompletionOfTwoInFlightCommits(t *testing.T) {
	// Two commits can be in flight if a debounce commits generation 1,
	// more input arrives (generation 2), and generation 2's commit
	// completes before generation 1's slower one. Generation 1's late
	// completion must still be discarded even though it arrives second.
	var tracker Tracker
	first := tracker.Dirty()
	second := tracker.Dirty()

	state, accepted := tracker.Accept(second, true)
	if !accepted || state != presentation.SaveStateSaved {
		t.Fatalf("Accept(second) = (%q, %v), want (%q, true)", state, accepted, presentation.SaveStateSaved)
	}

	state, accepted = tracker.Accept(first, true)
	if accepted {
		t.Fatalf("Accept(first) after second resolved: accepted = true, want false (got state %q)", state)
	}
}

func TestTrackerConcurrentDirtyAndAccept(t *testing.T) {
	var tracker Tracker
	const goroutines = 50

	var wg sync.WaitGroup
	generations := make(chan Generation, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			generations <- tracker.Dirty()
		}()
	}
	wg.Wait()
	close(generations)

	seen := make(map[Generation]bool)
	for g := range generations {
		if seen[g] {
			t.Fatalf("generation %d issued more than once", g)
		}
		seen[g] = true
	}
	if len(seen) != goroutines {
		t.Fatalf("issued %d distinct generations, want %d", len(seen), goroutines)
	}

	var maxGeneration Generation
	for g := range seen {
		if g > maxGeneration {
			maxGeneration = g
		}
	}

	var mu sync.Mutex
	accepted := make(map[Generation]bool)
	var acceptWG sync.WaitGroup
	for g := range seen {
		acceptWG.Add(1)
		go func(g Generation) {
			defer acceptWG.Done()
			_, ok := tracker.Accept(g, true)
			mu.Lock()
			accepted[g] = ok
			mu.Unlock()
		}(g)
	}
	acceptWG.Wait()

	// Every concurrent Accept for a generation older than the true
	// maximum must have been discarded by the same staleness invariant
	// the sequential tests above exercise directly; only the maximum
	// generation (and any that happen to tie it, none here since Dirty
	// issued strictly increasing values) may be accepted.
	for g, ok := range accepted {
		if g < maxGeneration && ok {
			t.Fatalf("generation %d (max %d) was accepted, want discarded", g, maxGeneration)
		}
	}
	if !accepted[maxGeneration] {
		t.Fatalf("the true latest generation %d was not accepted", maxGeneration)
	}
}
