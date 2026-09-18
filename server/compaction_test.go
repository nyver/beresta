package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

// TestCompactWorkspaceIsIdempotentAndRestartSafeAcrossRepeatedRuns covers
// task 5.8's restart-safe compaction maintenance: a maintenance run that
// compacts eligible opaque history, then a second run of the very same
// job - exactly what happens if the server restarts (or the periodic job
// simply fires again) before any new operations arrive - must be a safe
// no-op rather than erroring or removing anything twice. It also proves a
// dry-run preview never mutates state, so an operator (or a future
// Advanced "data check" action) can preview what compaction would do
// without it counting as the real run.
func TestCompactWorkspaceIsIdempotentAndRestartSafeAcrossRepeatedRuns(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")
	ctx := context.Background()

	operation := signedTestOperation(t, actor, time.Now())
	if _, err := runtime.Storage.PushOperations(ctx, actor.Principal, []Operation{operation}, time.Now()); err != nil {
		t.Fatal(err)
	}

	secondPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secondDeviceID := mustNewID(t)
	if err := runtime.Storage.AddDevice(ctx, actor.Principal, secondDeviceID, "Second device", secondPublic, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot := signedTestSnapshot(t, actor, 1, time.Now())
	if err := runtime.Storage.PutSnapshot(ctx, actor.Principal, snapshot, time.Now()); err != nil {
		t.Fatal(err)
	}
	ack := SnapshotAck{Protocol: "beresta.sync.v1", SchemaVersion: 1, SnapshotID: snapshot.ID, WorkspaceID: actor.WorkspaceID, DeviceID: actor.DeviceID,
		BaseSequence: snapshot.BaseSequence, CiphertextHash: snapshot.CiphertextHash}
	ack.Signature = ed25519.Sign(actor.PrivateKey, snapshotAckSignatureInput(ack))
	if err := runtime.Storage.RevokeDevice(ctx, actor.Principal, secondDeviceID, time.Now()); err != nil {
		t.Fatal(err)
	}
	afterRetention := time.Now().Add(31 * 24 * time.Hour)
	eligible, err := runtime.Storage.AcknowledgeSnapshot(ctx, actor.Principal, ack, afterRetention)
	if err != nil || !eligible {
		t.Fatalf("AcknowledgeSnapshot eligible=%v error=%v", eligible, err)
	}

	// A preview run, as an operator or a future "data check" action might
	// trigger before actually compacting, must report the eligible work
	// without removing anything.
	preview, err := runtime.Storage.CompactWorkspace(ctx, actor.WorkspaceID, afterRetention, true)
	if err != nil {
		t.Fatalf("CompactWorkspace (dry run): %v", err)
	}
	if preview.EligibleOperations != 1 || preview.RemovedOperations != 0 {
		t.Fatalf("CompactWorkspace dry run = %+v, want 1 eligible and 0 removed", preview)
	}
	var operationCount int
	if err := runtime.Database.QueryRowContext(ctx, `SELECT count(*) FROM operations WHERE workspace_id = ?`, actor.WorkspaceID).Scan(&operationCount); err != nil {
		t.Fatal(err)
	}
	if operationCount != 1 {
		t.Fatalf("operations remaining after a dry run = %d, want 1 (dry run must not mutate)", operationCount)
	}

	// The real maintenance run compacts the eligible operation.
	first, err := runtime.Storage.CompactWorkspace(ctx, actor.WorkspaceID, afterRetention, false)
	if err != nil {
		t.Fatalf("CompactWorkspace (first real run): %v", err)
	}
	if first.RemovedOperations != 1 {
		t.Fatalf("CompactWorkspace first run removed = %d, want 1", first.RemovedOperations)
	}

	// Simulate the same maintenance job firing again after a restart,
	// before any new operation exists: it must succeed as a clean no-op,
	// not fail or report removing anything a second time.
	second, err := runtime.Storage.CompactWorkspace(ctx, actor.WorkspaceID, afterRetention, false)
	if err != nil {
		t.Fatalf("CompactWorkspace (second run after simulated restart): %v", err)
	}
	if second.EligibleOperations != 0 || second.RemovedOperations != 0 {
		t.Fatalf("CompactWorkspace second run = %+v, want nothing left to compact", second)
	}
}
