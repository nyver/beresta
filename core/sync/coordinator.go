package sync

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Coordinator owns at most one worker per workspace. Attaching or detaching
// a server only replaces runtime transport state; it never migrates or clears
// the local SQLCipher database, outbox, cursor, or materialized collection.
type Coordinator struct {
	lifecycle sync.Mutex
	mu        sync.Mutex
	root      context.Context
	cancel    context.CancelFunc
	worker    *Worker
	trigger   chan struct{}
	done      chan error

	// lastPhase, lastErrorClass, lastSuccess, and retryDeadline track the
	// most recent Progress reported by the attached worker, exposed via
	// Progress, so callers can derive a SyncSummary without replaying every
	// Progress event themselves. They are reset on each Attach, since a new
	// worker means a new (or newly reconnected) workspace whose progress
	// history does not carry over.
	lastPhase      Phase
	lastErrorClass string
	lastSuccess    time.Time
	retryDeadline  time.Time
	lastRetryCount int
}

func NewCoordinator(root context.Context) *Coordinator {
	if root == nil {
		root = context.Background()
	}
	return &Coordinator{root: root}
}

func (c *Coordinator) Attach(worker *Worker) error {
	if worker == nil {
		return errors.New("sync: cannot attach a nil worker")
	}
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	c.stopAndWait()
	c.mu.Lock()
	defer c.mu.Unlock()

	// Reset progress state for the newly attached worker: it belongs to a
	// new (or newly reconnected) workspace whose history does not carry
	// over from whatever was attached before.
	c.lastPhase = ""
	c.lastErrorClass = ""
	c.lastSuccess = time.Time{}
	c.retryDeadline = time.Time{}
	c.lastRetryCount = 0

	// Wrap the worker's own Progress callback so Coordinator can derive
	// CoordinatorProgress without every caller replaying Progress events
	// itself, while still invoking the caller-supplied callback (if any).
	nowFunc := worker.options.Now
	upstream := worker.options.Progress
	worker.options.Progress = func(p Progress) {
		c.recordProgress(p, nowFunc)
		if upstream != nil {
			upstream(p)
		}
	}

	ctx, cancel := context.WithCancel(c.root)
	c.cancel, c.worker = cancel, worker
	c.trigger = make(chan struct{}, 1)
	c.done = make(chan error, 1)
	done := c.done
	go func(done chan<- error, triggers <-chan struct{}) {
		done <- worker.Run(ctx, triggers)
		// A quarantined worker exits permanently. Clear its live state so a
		// foreground sync request cannot be accepted into an unread trigger
		// channel and leave the UI indefinitely showing "Synchronizing".
		c.mu.Lock()
		if c.done == done {
			c.cancel, c.worker, c.trigger, c.done = nil, nil, nil, nil
		}
		c.mu.Unlock()
	}(done, c.trigger)
	c.trigger <- struct{}{}
	return nil
}

// Trigger queues a new cycle and reports whether a worker is still running.
// A false result means the caller must reattach or surface the terminal sync
// error instead of pretending the request is in progress.
func (c *Coordinator) Trigger() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.trigger == nil {
		return false
	}
	select {
	case c.trigger <- struct{}{}:
	default:
	}
	return true
}

func (c *Coordinator) Detach() {
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	c.stopAndWait()
}

func (c *Coordinator) stopAndWait() {
	c.mu.Lock()
	done := c.done
	if c.cancel != nil {
		c.cancel()
	}
	c.cancel, c.worker, c.trigger, c.done = nil, nil, nil, nil
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (c *Coordinator) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.worker != nil
}

func (c *Coordinator) Close() error { c.Detach(); return nil }

// recordProgress updates the coordinator's view of the attached worker's
// most recent phase, last successful round-trip time, and pending retry
// deadline. now is the worker's own clock (WorkerOptions.Now), kept
// consistent with the timing the worker itself used to compute p.RetryIn.
func (c *Coordinator) recordProgress(p Progress, now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastPhase = p.Phase
	c.lastErrorClass = p.ErrorClass
	c.lastRetryCount = p.RetryCount
	switch p.Phase {
	case PhaseCurrent:
		c.lastSuccess = now()
		c.retryDeadline = time.Time{}
	case PhaseBackoff:
		c.retryDeadline = now().Add(p.RetryIn)
	default:
		c.retryDeadline = time.Time{}
	}
}

// CoordinatorProgress is the coordinator's contribution to the shared
// SyncSummary: the most recently observed worker phase, the time of the
// last successful synchronization round-trip, and the deadline of a
// pending automatic retry. Combining it with durable pending/unsafe counts
// and transport reachability into a presentation.SyncSummary happens at
// the application layer, per specs/sync-engine's "Aggregated
// synchronization state" requirement.
type CoordinatorProgress struct {
	// Enabled reports whether a worker is currently attached.
	Enabled bool
	// Phase is the last phase reported by the attached worker's Progress
	// callback, or the zero value if none has been reported yet.
	Phase Phase
	// LastSuccess is the time of the last successful synchronization
	// round-trip, or the zero time.Time if synchronization has never
	// succeeded since this worker was attached.
	LastSuccess time.Time
	// RetryDeadline is when the next automatic retry is due, or the zero
	// time.Time when no retry is pending.
	RetryDeadline time.Time
	// ErrorClass is the classification of the error behind the last
	// PhaseBackoff report (see classifySyncError), or empty when Phase is
	// not PhaseBackoff. It is diagnostic-only and never carries operation
	// content or protocol detail.
	ErrorClass string
	// RetryCount is how many consecutive cycles have failed since the last
	// success. It is zero once a cycle succeeds.
	RetryCount int
}

// Progress returns the coordinator's current CoordinatorProgress snapshot.
func (c *Coordinator) Progress() CoordinatorProgress {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CoordinatorProgress{
		Enabled:       c.worker != nil,
		Phase:         c.lastPhase,
		LastSuccess:   c.lastSuccess,
		RetryDeadline: c.retryDeadline,
		ErrorClass:    c.lastErrorClass,
		RetryCount:    c.lastRetryCount,
	}
}
