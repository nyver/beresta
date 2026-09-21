package main

import (
	"time"

	"github.com/beresta-app/beresta/core/perf"
)

// RecordPerfStage lets the frontend report bounded elapsed-time samples for
// the render-dependent component-boundary stages the Go side cannot observe
// directly - first note list, editor readiness, settings open, and cached
// attachment preview (see design.md's "Make performance budgets observable
// at component boundaries" decision, task 11.1). stage must be one of
// perf.Stage's closed values; durationMs must be non-negative. Both
// constraints are enforced here rather than trusted from the frontend,
// since this method is reachable from any JS bound to this App.
func (a *App) RecordPerfStage(stage string, durationMs int64) error {
	s := perf.Stage(stage)
	if !s.Valid() || durationMs < 0 {
		return &AppError{Code: ErrCodeInvalidInput, Message: "Unknown performance stage or negative duration."}
	}
	a.perf.Record(s, time.Duration(durationMs)*time.Millisecond)
	return nil
}
