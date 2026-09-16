package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

type quarantinedWorkerRepository struct{ workerRepository }

func (*quarantinedWorkerRepository) QuarantineBlocked(context.Context, model.ID) (bool, error) {
	return true, nil
}

func TestCoordinatorDoesNotAcceptTriggerAfterWorkerStops(t *testing.T) {
	workspaceID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewWorker(workspaceID, &quarantinedWorkerRepository{}, &workerTransport{}, acceptingProcessor{}, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewCoordinator(context.Background())
	if err := coordinator.Attach(worker); err != nil {
		t.Fatal(err)
	}
	defer coordinator.Detach()

	deadline := time.Now().Add(time.Second)
	for coordinator.Enabled() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if coordinator.Enabled() {
		t.Fatal("coordinator still reports an exited worker as enabled")
	}
	if coordinator.Trigger() {
		t.Fatal("Trigger accepted work after the worker had stopped")
	}
}

type failingWorkerTransport struct{}

func (failingWorkerTransport) Pull(context.Context, model.ID, Cursor, int) (PullPage, error) {
	return PullPage{}, errors.New("boom")
}

func (failingWorkerTransport) Push(context.Context, model.ID, []WireOperation) ([]PushResult, error) {
	return nil, nil
}

func TestCoordinatorProgressReflectsSuccessfulSync(t *testing.T) {
	workspaceID := testID(30)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	// Two pages: Attach queues an explicit trigger in addition to the
	// worker's own initial zero-duration timer tick, so up to two
	// SyncOnce cycles can run before PollInterval next fires.
	transport := &workerTransport{pages: []PullPage{
		{Cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}},
		{Cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}},
	}}
	worker, err := NewWorker(workspaceID, repository, transport, acceptingProcessor{}, WorkerOptions{PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewCoordinator(context.Background())
	if err := coordinator.Attach(worker); err != nil {
		t.Fatal(err)
	}
	defer coordinator.Detach()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		progress := coordinator.Progress()
		if progress.Phase == PhaseCurrent {
			if !progress.Enabled {
				t.Fatal("Progress().Enabled = false while a worker is attached")
			}
			if progress.LastSuccess.IsZero() {
				t.Fatal("Progress().LastSuccess is zero after a successful sync")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("coordinator never reported PhaseCurrent progress")
}

func TestCoordinatorProgressReflectsPendingBackoffRetry(t *testing.T) {
	workspaceID := testID(31)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	worker, err := NewWorker(workspaceID, repository, failingWorkerTransport{}, acceptingProcessor{}, WorkerOptions{
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     time.Second,
		Jitter:         func(cap time.Duration) time.Duration { return cap },
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewCoordinator(context.Background())
	if err := coordinator.Attach(worker); err != nil {
		t.Fatal(err)
	}
	defer coordinator.Detach()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		progress := coordinator.Progress()
		if progress.Phase == PhaseBackoff {
			if !progress.RetryDeadline.After(time.Now().Add(-50 * time.Millisecond)) {
				t.Fatalf("Progress().RetryDeadline = %v is not an upcoming retry", progress.RetryDeadline)
			}
			if !progress.LastSuccess.IsZero() {
				t.Fatalf("Progress().LastSuccess = %v, want zero (sync has never succeeded)", progress.LastSuccess)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("coordinator never reported PhaseBackoff progress")
}

func TestCoordinatorProgressResetsOnReattach(t *testing.T) {
	workspaceID := testID(32)
	repository := &workerRepository{cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	// Two pages: Attach queues an explicit trigger in addition to the
	// worker's own initial zero-duration timer tick, so up to two
	// SyncOnce cycles can run before PollInterval next fires.
	transport := &workerTransport{pages: []PullPage{
		{Cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}},
		{Cursor: Cursor{WorkspaceID: workspaceID, Epoch: 1}},
	}}
	worker, err := NewWorker(workspaceID, repository, transport, acceptingProcessor{}, WorkerOptions{PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewCoordinator(context.Background())
	if err := coordinator.Attach(worker); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && coordinator.Progress().Phase != PhaseCurrent {
		time.Sleep(time.Millisecond)
	}
	if coordinator.Progress().Phase != PhaseCurrent {
		t.Fatal("first worker never reached PhaseCurrent")
	}

	// Attaching a second worker resets progress before its goroutine
	// starts, so nothing the first worker reported (its PhaseCurrent
	// phase, its LastSuccess time) can bleed into the newly attached
	// one. The second worker's own repository reports itself blocked, so
	// it exits immediately after reporting exactly one PhaseQuarantine
	// event and nothing else - if the reset above had not happened, that
	// event would land on top of the first worker's still-set LastSuccess
	// instead of the zero value it must combine with.
	secondWorker, err := NewWorker(workspaceID, &quarantinedWorkerRepository{}, &workerTransport{}, acceptingProcessor{}, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Attach(secondWorker); err != nil {
		t.Fatal(err)
	}
	defer coordinator.Detach()

	deadline = time.Now().Add(time.Second)
	for coordinator.Enabled() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if coordinator.Enabled() {
		t.Fatal("second worker never stopped")
	}

	progress := coordinator.Progress()
	if progress.Phase != PhaseQuarantine {
		t.Fatalf("Progress().Phase = %q after the second (blocked) worker stopped, want %q", progress.Phase, PhaseQuarantine)
	}
	if !progress.LastSuccess.IsZero() {
		t.Fatalf("Progress().LastSuccess = %v, want zero - the first worker's success must not leak into the second worker's progress", progress.LastSuccess)
	}
	if !progress.RetryDeadline.IsZero() {
		t.Fatalf("Progress().RetryDeadline = %v, want zero", progress.RetryDeadline)
	}
}
