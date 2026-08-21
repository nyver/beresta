package server

import (
	"context"
	"testing"
	"time"
)

// TestPushOperationsBatchIsAtomicAcrossForcedFailure covers the
// "server persistence" and "sync" legs of task 9.12's forced-termination
// matrix: a batch push must not leave a partial effect if any operation in
// it is rejected partway through. PushOperations processes the whole batch
// inside one transaction (see operation_store.go), so a rejection anywhere
// in the batch must roll back every sequence assignment already made
// earlier in the same call - exactly what would also happen if the process
// were killed mid-transaction, since neither case reaches a commit.
func TestPushOperationsBatchIsAtomicAcrossForcedFailure(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")

	valid := signedTestOperation(t, actor, time.Now())
	invalid := signedTestOperation(t, actor, time.Now())
	invalid.Signature[0] ^= 0xFF // fails signature verification partway through the batch

	if _, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{valid, invalid}, time.Now()); err == nil {
		t.Fatal("expected a batch containing an invalid operation to be rejected")
	}

	// Nothing from the rejected batch may be visible: not the workspace
	// sequence counter, and not the supposedly valid first operation.
	var latestSeq int64
	if err := runtime.Database.QueryRow(`SELECT latest_seq FROM workspaces WHERE workspace_id = ?`, actor.WorkspaceID).Scan(&latestSeq); err != nil {
		t.Fatal(err)
	}
	if latestSeq != 0 {
		t.Fatalf("expected the workspace sequence counter to be untouched by the rolled-back batch, got %d", latestSeq)
	}
	changes, err := runtime.Storage.PullChanges(context.Background(), actor.Principal, actor.WorkspaceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes.Operations) != 0 {
		t.Fatalf("expected no operations to be visible after the rolled-back batch, got %d", len(changes.Operations))
	}

	// A subsequent push of the same valid operation alone must succeed and
	// claim sequence 1 - not 2 - proving the earlier attempt left no trace.
	accepted, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{valid}, time.Now())
	if err != nil || len(accepted) != 1 || accepted[0].Sequence != 1 {
		t.Fatalf("PushOperations after rollback = %+v, %v", accepted, err)
	}
}
