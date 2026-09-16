package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

func TestRequestForegroundSyncIsANoOpWithoutASyncCoordinator(t *testing.T) {
	a := newTestApp(t)
	// a.syncCoordinator is nil until a server is configured; showing the
	// window before any server connection exists must not panic.
	a.requestForegroundSync()
}

type foregroundSyncTransport struct{ pulls atomic.Int64 }

func (t *foregroundSyncTransport) Pull(_ context.Context, workspaceID model.ID, cursor coresync.Cursor, _ int) (coresync.PullPage, error) {
	t.pulls.Add(1)
	return coresync.PullPage{Cursor: coresync.Cursor{WorkspaceID: workspaceID, Epoch: cursor.Epoch, LastSequence: cursor.LastSequence}}, nil
}

func (*foregroundSyncTransport) Push(context.Context, model.ID, []coresync.WireOperation) ([]coresync.PushResult, error) {
	return nil, nil
}

type foregroundSyncRepository struct{ cursor coresync.Cursor }

func (r *foregroundSyncRepository) Cursor(context.Context, model.ID) (coresync.Cursor, error) {
	return r.cursor, nil
}

func (*foregroundSyncRepository) Quarantine(context.Context, coresync.WireOperation, string, time.Time) error {
	return nil
}

func (*foregroundSyncRepository) QuarantineBlocked(context.Context, model.ID) (bool, error) {
	return false, nil
}

func (*foregroundSyncRepository) ApplyPage(context.Context, coresync.Cursor, []coresync.WireOperation, coresync.OperationProcessor, time.Time) error {
	return nil
}

func (*foregroundSyncRepository) Pending(context.Context, model.ID, int) ([]coresync.WireOperation, error) {
	return nil, nil
}

func (*foregroundSyncRepository) MarkPushed(context.Context, model.ID, []coresync.PushResult, time.Time) error {
	return nil
}

type foregroundSyncProcessor struct{}

func (foregroundSyncProcessor) Verify(context.Context, coresync.WireOperation) (coresync.VerifiedOperation, error) {
	return nil, nil
}

// TestRequestForegroundSyncTriggersAnAttachedWorker proves showWindow's new
// foreground sync nudge (desktop/shell.go's requestForegroundSync) actually
// reaches an attached coordinator's worker, mirroring the mobile client's
// app-resumed sync trigger (design.md's "foreground ... triggers call its
// coalescing trigger path").
func TestRequestForegroundSyncTriggersAnAttachedWorker(t *testing.T) {
	a := newTestApp(t)
	workspaceID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	repository := &foregroundSyncRepository{cursor: coresync.Cursor{WorkspaceID: workspaceID, Epoch: 1}}
	transport := &foregroundSyncTransport{}
	worker, err := coresync.NewWorker(workspaceID, repository, transport, foregroundSyncProcessor{}, coresync.WorkerOptions{PollInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := coresync.NewCoordinator(context.Background())
	if err := coordinator.Attach(worker); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coordinator.Detach)
	a.mu.Lock()
	a.syncCoordinator = coordinator
	a.mu.Unlock()

	deadline := time.Now().Add(time.Second)
	for transport.pulls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if transport.pulls.Load() == 0 {
		t.Fatal("attached worker never ran its initial cycle")
	}
	// Attach can queue up to two initial cycles (its own explicit trigger
	// plus the worker's zero-duration startup timer); let both settle
	// before recording the baseline pull count.
	time.Sleep(50 * time.Millisecond)
	before := transport.pulls.Load()

	a.requestForegroundSync()

	deadline = time.Now().Add(time.Second)
	for transport.pulls.Load() <= before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if transport.pulls.Load() <= before {
		t.Fatal("requestForegroundSync did not trigger another sync cycle")
	}
}
