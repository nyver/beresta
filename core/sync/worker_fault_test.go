package sync

import (
	"context"
	"errors"
	"testing"
	"time"
)

// rejectingApplyRepository wraps a *workerRepository but makes ApplyPage
// return a canned RejectedOperationError instead of succeeding, so tests
// can prove how Worker.SyncOnce reacts to a rejected operation without
// needing a real processor/repository round trip (core/store's
// sync_repository_fault_test.go already covers how that rejection itself
// gets produced).
type rejectingApplyRepository struct {
	*workerRepository
	err error
}

func (r *rejectingApplyRepository) ApplyPage(_ context.Context, _ Cursor, _ []WireOperation, _ OperationProcessor, _ time.Time) error {
	r.calls = append(r.calls, "apply")
	return r.err
}

// TestWorkerQuarantinesARejectedOperationAndStopsBeforePush covers task
// 3.8's "corrupt ... operations" regression: when the repository rejects
// an operation partway through a pulled page, the worker must quarantine
// it, report ErrWorkspaceQuarantined, and stop - never proceeding to push
// pending local work on top of a workspace whose cursor is now blocked.
func TestWorkerQuarantinesARejectedOperationAndStopsBeforePush(t *testing.T) {
	workspace := testID(60)
	remote := fixtureWireOperation(t, 1)
	remote.WorkspaceID = workspace
	remote.Sequence = 1
	local := fixtureWireOperation(t, 2)
	local.WorkspaceID = workspace

	base := &workerRepository{cursor: Cursor{WorkspaceID: workspace, Epoch: 1}, pending: []WireOperation{local}}
	repository := &rejectingApplyRepository{workerRepository: base, err: Reject(remote, "bad_signature", errors.New("signature verification failed"))}
	transport := &workerTransport{pages: []PullPage{{Cursor: Cursor{WorkspaceID: workspace, LastSequence: 1, Epoch: 1}, Operations: []WireOperation{remote}}}}

	worker, err := NewWorker(workspace, repository, transport, acceptingProcessor{}, WorkerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.SyncOnce(context.Background()); !errors.Is(err, ErrWorkspaceQuarantined) {
		t.Fatalf("SyncOnce error = %v, want ErrWorkspaceQuarantined", err)
	}
	if len(base.quarantined) != 1 || base.quarantined[0].OpID != remote.OpID {
		t.Fatalf("quarantined = %+v, want exactly the rejected operation", base.quarantined)
	}
	for _, call := range transport.calls {
		if call == "push" {
			t.Fatal("transport.Push was called after a quarantine - pending local work must not be pushed on top of a blocked cursor")
		}
	}
}

// TestWorkerRejectsAMalformedPullPage covers task 3.8's "duplicate, and
// reordered operations" regression: a pulled page whose operations skip a
// sequence, repeat one, or arrive out of order must be rejected before it
// ever reaches ApplyPage - validatePullPage is the worker's own defense
// against a buggy or actively malicious transport, independent of
// whatever the repository would otherwise do with the operations.
func TestWorkerRejectsAMalformedPullPage(t *testing.T) {
	workspace := testID(61)

	build := func(sequences ...uint64) []WireOperation {
		ops := make([]WireOperation, len(sequences))
		for i, seq := range sequences {
			op := fixtureWireOperation(t, byte(10+i))
			op.WorkspaceID = workspace
			op.Sequence = seq
			ops[i] = op
		}
		return ops
	}

	cases := []struct {
		name  string
		pages []PullPage
	}{
		{"sequence gap", []PullPage{{Cursor: Cursor{WorkspaceID: workspace, LastSequence: 3, Epoch: 1}, Operations: build(3)}}},
		{"duplicate sequence within a page", []PullPage{{Cursor: Cursor{WorkspaceID: workspace, LastSequence: 2, Epoch: 1}, Operations: build(1, 1)}}},
		{"reordered (descending) sequence", []PullPage{{Cursor: Cursor{WorkspaceID: workspace, LastSequence: 2, Epoch: 1}, Operations: build(2, 1)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repository := &workerRepository{cursor: Cursor{WorkspaceID: workspace, Epoch: 1}}
			transport := &workerTransport{pages: tc.pages}
			worker, err := NewWorker(workspace, repository, transport, acceptingProcessor{}, WorkerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := worker.SyncOnce(context.Background()); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("SyncOnce error = %v, want ErrInvalidCursor", err)
			}
			if len(repository.calls) != 0 {
				t.Fatalf("repository.calls = %v, want none - a malformed page must never reach ApplyPage", repository.calls)
			}
		})
	}
}
