package store

import (
	"context"
	"testing"
	"time"
)

func TestKeyRotationPendingMarksClearsAndIsIdempotent(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	otherWorkspaceID := seedWorkspace(t, db)

	if pending, err := KeyRotationPending(ctx, db, workspaceID); err != nil || pending {
		t.Fatalf("KeyRotationPending before marking = %v, %v, want false, nil", pending, err)
	}

	now := time.Now()
	if err := MarkKeyRotationPending(ctx, db, workspaceID, now); err != nil {
		t.Fatal(err)
	}
	// Marking again must not error or reset the original request time.
	if err := MarkKeyRotationPending(ctx, db, workspaceID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if pending, err := KeyRotationPending(ctx, db, workspaceID); err != nil || !pending {
		t.Fatalf("KeyRotationPending after marking = %v, %v, want true, nil", pending, err)
	}
	if pending, err := KeyRotationPending(ctx, db, otherWorkspaceID); err != nil || pending {
		t.Fatalf("KeyRotationPending for other workspace = %v, %v, want false, nil", pending, err)
	}

	if err := ClearKeyRotationPending(ctx, db, workspaceID); err != nil {
		t.Fatal(err)
	}
	if pending, err := KeyRotationPending(ctx, db, workspaceID); err != nil || pending {
		t.Fatalf("KeyRotationPending after clearing = %v, %v, want false, nil", pending, err)
	}
	// Clearing an already-clear marker must not error.
	if err := ClearKeyRotationPending(ctx, db, workspaceID); err != nil {
		t.Fatal(err)
	}
}

func TestSyncRepositoryPendingHoldsPushWhileKeyRotationIsPendingAndResumesAfter(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	repo, err := NewSyncRepository(db, "test-transport")
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)

	op := OutboxOperation{
		OpID: mustNewID(t), WorkspaceID: workspaceID, DeviceID: repoTestDeviceID(t, 0x04),
		Clock: repoClock(t, 1000, 0, 0x04),
		KeyID: []byte("key"), Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), Signature: []byte("sig"),
	}
	if err := InsertOutboxOperation(ctx, db, op, 1000); err != nil {
		t.Fatal(err)
	}

	pending, err := repo.Pending(ctx, workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("Pending before rotation marker = %d operations, want 1", len(pending))
	}

	if err := MarkKeyRotationPending(ctx, db, workspaceID, time.Now()); err != nil {
		t.Fatal(err)
	}

	held, err := repo.Pending(ctx, workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 0 {
		t.Fatalf("Pending while rotation is pending = %d operations, want 0 (push must be held)", len(held))
	}
	// CountPending is an independent diagnostic-facing count and must keep
	// reporting the true outbox size - only Pending (the worker's push
	// source) holds back while a rotation is unresolved.
	if count, err := repo.CountPending(ctx, workspaceID); err != nil || count != 1 {
		t.Fatalf("CountPending while rotation is pending = %d, %v, want 1, nil", count, err)
	}

	if err := ClearKeyRotationPending(ctx, db, workspaceID); err != nil {
		t.Fatal(err)
	}
	resumed, err := repo.Pending(ctx, workspaceID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 1 {
		t.Fatalf("Pending after rotation cleared = %d operations, want 1 (push must resume)", len(resumed))
	}
}
