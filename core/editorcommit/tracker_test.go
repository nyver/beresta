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

func TestAcceptDuplicateGenerationIsDiscarded(t *testing.T) {
	var tracker Tracker
	g := tracker.Dirty()

	if _, accepted := tracker.Accept(g, true); !accepted {
		t.Fatal("first Accept() accepted = false, want true")
	}
	state, accepted := tracker.Accept(g, true)
	if accepted {
		t.Fatalf("duplicate Accept() accepted = true, want false (got state %q)", state)
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

	var acceptWG sync.WaitGroup
	for g := range seen {
		acceptWG.Add(1)
		go func(g Generation) {
			defer acceptWG.Done()
			tracker.Accept(g, true)
		}(g)
	}
	acceptWG.Wait()

	// Exactly the true latest generation must end up resolved and
	// reported as Saved; every other concurrent Accept for an older
	// generation must have been discarded by the same invariant the
	// sequential tests above exercise directly.
	state, accepted := tracker.Accept(tracker.Latest(), true)
	if accepted {
		t.Fatalf("re-Accept of the already-resolved latest generation: accepted = true, want false (got state %q)", state)
	}
}
