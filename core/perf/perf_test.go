package perf

import (
	"sync"
	"testing"
	"time"
)

func TestRecordAndSnapshot(t *testing.T) {
	r := NewRecorder()
	r.Record(StageUnlockReady, 10*time.Millisecond)
	r.Record(StageUnlockReady, 20*time.Millisecond)
	r.Record(StageUnlockReady, 30*time.Millisecond)

	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot() len = %d, want 1 (only recorded stages should appear)", len(snap))
	}
	agg := snap[0]
	if agg.Stage != StageUnlockReady {
		t.Fatalf("Stage = %v, want %v", agg.Stage, StageUnlockReady)
	}
	if agg.Count != 3 {
		t.Fatalf("Count = %d, want 3", agg.Count)
	}
	if agg.Min != 10*time.Millisecond {
		t.Fatalf("Min = %v, want 10ms", agg.Min)
	}
	if agg.Max != 30*time.Millisecond {
		t.Fatalf("Max = %v, want 30ms", agg.Max)
	}
}

func TestSnapshotOmitsUnrecordedStages(t *testing.T) {
	r := NewRecorder()
	if snap := r.Snapshot(); len(snap) != 0 {
		t.Fatalf("Snapshot() on empty recorder = %+v, want empty", snap)
	}
}

func TestSnapshotOrderIsStable(t *testing.T) {
	r := NewRecorder()
	r.Record(StageSettingsOpen, time.Millisecond)
	r.Record(StageProcessStart, time.Millisecond)
	r.Record(StageDatabaseOpen, time.Millisecond)

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot() len = %d, want 3", len(snap))
	}
	want := []Stage{StageProcessStart, StageDatabaseOpen, StageSettingsOpen}
	for i, stage := range want {
		if snap[i].Stage != stage {
			t.Fatalf("Snapshot()[%d].Stage = %v, want %v (stage order must be fixed, not insertion order)", i, snap[i].Stage, stage)
		}
	}
}

func TestRecordIgnoresInvalidStage(t *testing.T) {
	r := NewRecorder()
	r.Record(Stage("not_a_real_stage"), time.Millisecond)
	if snap := r.Snapshot(); len(snap) != 0 {
		t.Fatalf("Snapshot() after invalid Record = %+v, want empty", snap)
	}
}

func TestRecordIgnoresNegativeDuration(t *testing.T) {
	r := NewRecorder()
	r.Record(StageEditorReady, -time.Millisecond)
	if snap := r.Snapshot(); len(snap) != 0 {
		t.Fatalf("Snapshot() after negative-duration Record = %+v, want empty", snap)
	}
}

// TestRingIsBounded proves recording far more than maxSamplesPerStage
// samples never grows retained state past the bound, and that the oldest
// samples are the ones dropped (so Min tracks the newest low value, not a
// stale one from long ago).
func TestRingIsBounded(t *testing.T) {
	r := NewRecorder()
	for i := 0; i < maxSamplesPerStage*3; i++ {
		r.Record(StageSyncProjection, time.Duration(i+1)*time.Millisecond)
	}
	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot() len = %d, want 1", len(snap))
	}
	agg := snap[0]
	if agg.Count != maxSamplesPerStage {
		t.Fatalf("Count = %d, want %d (ring must stay bounded)", agg.Count, maxSamplesPerStage)
	}
	wantMin := time.Duration(maxSamplesPerStage*2+1) * time.Millisecond
	if agg.Min != wantMin {
		t.Fatalf("Min = %v, want %v (oldest samples should have been overwritten)", agg.Min, wantMin)
	}
}

func TestPercentilesAreOrdered(t *testing.T) {
	r := NewRecorder()
	for i := 1; i <= 100; i++ {
		r.Record(StageCommitAcknowledged, time.Duration(i)*time.Millisecond)
	}
	agg := r.Snapshot()[0]
	if !(agg.Min <= agg.P50 && agg.P50 <= agg.P95 && agg.P95 <= agg.Max) {
		t.Fatalf("percentiles out of order: min=%v p50=%v p95=%v max=%v", agg.Min, agg.P50, agg.P95, agg.Max)
	}
	if agg.P95 < 90*time.Millisecond {
		t.Fatalf("P95 = %v, want close to the top of a uniform 1..100ms distribution", agg.P95)
	}
}

func TestTimerNilHookIsNoop(t *testing.T) {
	stop := Timer(nil, StageProcessStart)
	stop() // must not panic
}

func TestTimerRecordsElapsed(t *testing.T) {
	r := NewRecorder()
	stop := r.Start(StageProcessStart)
	time.Sleep(time.Millisecond)
	stop()

	snap := r.Snapshot()
	if len(snap) != 1 || snap[0].Count != 1 {
		t.Fatalf("Snapshot() = %+v, want one sample for %v", snap, StageProcessStart)
	}
	if snap[0].Min <= 0 {
		t.Fatalf("Min = %v, want > 0", snap[0].Min)
	}
}

// TestRecordIsConcurrencySafe proves Recorder tolerates concurrent Record
// calls from multiple goroutines without a data race (run with -race).
func TestRecordIsConcurrencySafe(t *testing.T) {
	r := NewRecorder()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				r.Record(StageFirstNoteList, time.Duration(i)*time.Microsecond)
			}
		}()
	}
	wg.Wait()

	snap := r.Snapshot()
	if len(snap) != 1 || snap[0].Count != maxSamplesPerStage {
		t.Fatalf("Snapshot() = %+v, want a full ring after 400 concurrent records", snap)
	}
}
