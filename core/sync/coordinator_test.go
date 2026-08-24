package sync

import (
	"context"
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
