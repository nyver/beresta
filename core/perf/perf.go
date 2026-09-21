// Package perf provides bounded, in-memory monotonic-timing instrumentation
// for the fixed set of component-boundary stages named in
// openspec/changes/harden-product-ux-reliability/design.md's "Make
// performance budgets observable at component boundaries" decision: process
// start, unlock readiness, database open, first note list, editor
// readiness, local commit acknowledgement, sync-event projection, settings
// open, and cached attachment preview. It never records note content,
// titles, or other free-form input - only a closed Stage identifier and an
// elapsed time.Duration - and keeps only a bounded number of recent samples
// per stage, so a long-running process cannot grow this state without
// limit. Desktop and core/mobileapi each own one Recorder and forward
// aggregated snapshots (never raw samples) into diagnostics.
package perf

import (
	"sort"
	"sync"
	"time"
)

// Stage is the closed set of instrumented component boundaries.
type Stage string

const (
	// StageProcessStart measures elapsed time from process creation to the
	// first interactive main window (desktop) or service readiness
	// (mobile).
	StageProcessStart Stage = "process_start"
	// StageUnlockReady measures elapsed time for a local account unlock
	// attempt to complete, including database open and key derivation.
	StageUnlockReady Stage = "unlock_ready"
	// StageDatabaseOpen measures elapsed time for the local encrypted
	// database to open (and migrate, if pending), a sub-stage of unlock.
	StageDatabaseOpen Stage = "database_open"
	// StageFirstNoteList measures elapsed time from an unlocked session
	// becoming ready to the first note list finishing its initial load.
	StageFirstNoteList Stage = "first_note_list"
	// StageEditorReady measures elapsed time from opening a note to the
	// editor becoming interactive.
	StageEditorReady Stage = "editor_ready"
	// StageCommitAcknowledged measures elapsed time from an editor commit
	// request to its durable acknowledgement (see core/editorcommit).
	StageCommitAcknowledged Stage = "commit_acknowledged"
	// StageSyncProjection measures elapsed time for an incoming
	// synchronization event to be projected into presentation state.
	StageSyncProjection Stage = "sync_projection"
	// StageSettingsOpen measures elapsed time from requesting settings to
	// the settings surface becoming interactive.
	StageSettingsOpen Stage = "settings_open"
	// StageCachedPreview measures elapsed time for an already-cached
	// attachment preview to become visible.
	StageCachedPreview Stage = "cached_preview"
)

// stages lists every valid Stage in a fixed, stable order used by
// Snapshot so aggregates are reported deterministically.
var stages = []Stage{
	StageProcessStart,
	StageUnlockReady,
	StageDatabaseOpen,
	StageFirstNoteList,
	StageEditorReady,
	StageCommitAcknowledged,
	StageSyncProjection,
	StageSettingsOpen,
	StageCachedPreview,
}

// Valid reports whether s is one of the closed Stage values.
func (s Stage) Valid() bool {
	for _, candidate := range stages {
		if s == candidate {
			return true
		}
	}
	return false
}

// maxSamplesPerStage bounds how many recent samples each stage retains.
// Once full, the oldest sample is overwritten, so memory use never grows
// with process lifetime.
const maxSamplesPerStage = 128

// Hook records one completed stage measurement. A nil Hook is always safe
// to call through Timer.
type Hook func(stage Stage, elapsed time.Duration)

// ring is a fixed-capacity circular buffer of durations for one stage.
type ring struct {
	samples [maxSamplesPerStage]time.Duration
	len     int
	next    int
}

func (r *ring) add(d time.Duration) {
	r.samples[r.next] = d
	r.next = (r.next + 1) % maxSamplesPerStage
	if r.len < maxSamplesPerStage {
		r.len++
	}
}

func (r *ring) sorted() []time.Duration {
	out := make([]time.Duration, r.len)
	copy(out, r.samples[:r.len])
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Recorder holds bounded recent-sample state for every Stage. The zero
// value is not usable; construct with NewRecorder. A Recorder is safe for
// concurrent use.
type Recorder struct {
	mu   sync.Mutex
	data map[Stage]*ring
}

// NewRecorder returns an empty Recorder ready to record samples for every
// closed Stage.
func NewRecorder() *Recorder {
	data := make(map[Stage]*ring, len(stages))
	for _, stage := range stages {
		data[stage] = &ring{}
	}
	return &Recorder{data: data}
}

// Record stores one elapsed-time sample for stage. Invalid stages and
// negative durations (which cannot occur with a monotonic clock, but could
// reach this call through a caller error) are silently dropped rather than
// corrupting aggregates.
func (r *Recorder) Record(stage Stage, elapsed time.Duration) {
	if !stage.Valid() || elapsed < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[stage].add(elapsed)
}

// Hook returns a Hook bound to this Recorder, for passing into lower
// layers (such as core/account.UnlockOptions.Hook) that report timing
// without holding a *Recorder reference of their own.
func (r *Recorder) Hook() Hook {
	return r.Record
}

// Start begins timing stage using the monotonic clock and returns a stop
// function that records the elapsed duration when called. Typical use is
// `defer recorder.Start(perf.StageUnlockReady)()`.
func (r *Recorder) Start(stage Stage) func() {
	return Timer(r.Hook(), stage)
}

// Timer begins timing stage and returns a stop function that invokes hook
// with the elapsed duration when called. hook may be nil, in which case
// the returned function is a no-op; this lets callers unconditionally
// defer Timer(...)() even when no recorder is configured.
func Timer(hook Hook, stage Stage) func() {
	start := time.Now()
	return func() {
		if hook != nil {
			hook(stage, time.Since(start))
		}
	}
}

// Aggregate is one Stage's bounded summary: sample count and min/p50/p95/max
// elapsed time over the retained samples. It never carries raw samples,
// timestamps, or any caller-supplied content.
type Aggregate struct {
	Stage Stage
	Count int
	Min   time.Duration
	P50   time.Duration
	P95   time.Duration
	Max   time.Duration
}

// Snapshot returns one Aggregate per closed Stage that has recorded at
// least one sample, in the fixed Stage order, computed from up to the most
// recent maxSamplesPerStage samples.
func (r *Recorder) Snapshot() []Aggregate {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]Aggregate, 0, len(stages))
	for _, stage := range stages {
		sorted := r.data[stage].sorted()
		if len(sorted) == 0 {
			continue
		}
		out = append(out, Aggregate{
			Stage: stage,
			Count: len(sorted),
			Min:   sorted[0],
			P50:   percentile(sorted, 0.50),
			P95:   percentile(sorted, 0.95),
			Max:   sorted[len(sorted)-1],
		})
	}
	return out
}

// percentile returns the value at fraction p (0..1) of sorted, which must
// be non-empty and ascending. It uses nearest-rank interpolation, which is
// stable and sufficient for the bounded sample sizes this package retains.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := int(p * float64(len(sorted)-1))
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
