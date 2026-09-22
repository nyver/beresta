package main

import (
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/perf"
)

// TestSyncStatusLatencyBudget is task 11.4's sync-status regression gate:
// the release-quality spec requires synchronization status to update
// within 250ms of an event (openspec/specs/release-quality/spec.md,
// "Target-scale performance budgets"). It exercises the real production
// code path SyncSummary already instruments with perf.StageSyncProjection
// (desktop/sync.go: a.lastSyncProgressAt, set by the sync coordinator's own
// Progress callback firing after a real cycle against a real server, and
// consumed by the next SyncSummary() call) rather than mocking the
// coordinator or the callback. Only elapsed durations are ever logged or
// asserted on here - no cursor, operation ID, or note content is read or
// reported, matching the task's explicit privacy constraint.
func TestSyncStatusLatencyBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sync-status latency benchmark in -short mode")
	}
	const budget = 250 * time.Millisecond

	runtime, baseURL := startDesktopE2EServer(t)
	a := newTestApp(t)
	connectDesktopE2EActor(t, a, runtime, baseURL, "solo")

	if _, err := a.CreateNote("", "Benchmark note"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := a.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	// SyncNow only triggers the background sync worker (coordinator.Trigger
	// wakes it asynchronously); wait for its Progress callback to actually
	// fire - the real "event" the spec's budget starts from - rather than
	// racing it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		fired := !a.lastSyncProgressAt.IsZero()
		a.mu.Unlock()
		if fired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the sync coordinator's Progress callback to fire")
		}
		time.Sleep(time.Millisecond)
	}

	start := time.Now()
	if _, err := a.SyncSummary(); err != nil {
		t.Fatalf("SyncSummary: %v", err)
	}
	elapsed := time.Since(start)
	t.Logf("SyncSummary projected the pending sync event in %v (budget %v)", elapsed, budget)
	if elapsed > budget {
		t.Fatalf("SyncSummary projection took %v, exceeding the %v release budget", elapsed, budget)
	}

	for _, agg := range a.perf.Snapshot() {
		if agg.Stage != perf.StageSyncProjection {
			continue
		}
		t.Logf("recorded sync-projection p95 = %v across %d sample(s) (budget %v)", agg.P95, agg.Count, budget)
		if agg.P95 > budget {
			t.Fatalf("perf.StageSyncProjection p95 = %v across %d sample(s), exceeding the %v release budget", agg.P95, agg.Count, budget)
		}
	}
}
