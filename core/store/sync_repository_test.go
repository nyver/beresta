package store

import (
	"context"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

func testWireOperation(t testing.TB, workspaceID model.ID, sequence uint64, deviceSeed byte) coresync.WireOperation {
	t.Helper()
	opID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return coresync.WireOperation{
		OpID:        opID,
		WorkspaceID: workspaceID,
		DeviceID:    repoTestDeviceID(t, deviceSeed),
		Clock:       repoClock(t, 1000, 0, deviceSeed),
		KeyID:       []byte("key"),
		Nonce:       []byte("nonce"),
		Ciphertext:  []byte("ciphertext"),
		Signature:   []byte("sig"),
		Sequence:    sequence,
	}
}

func TestSyncRepositoryCountPendingCountsOnlyUnpushedUnrejectedOperationsForTheWorkspace(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	repo, err := NewSyncRepository(db, "test-transport")
	if err != nil {
		t.Fatal(err)
	}

	workspaceID := seedWorkspace(t, db)
	otherWorkspaceID := seedWorkspace(t, db)

	for i, opID := range []model.ID{mustNewID(t), mustNewID(t), mustNewID(t)} {
		op := OutboxOperation{
			OpID: opID, WorkspaceID: workspaceID, DeviceID: repoTestDeviceID(t, 0x02),
			Clock: repoClock(t, uint64(1000+i), 0, 0x02),
			KeyID: []byte("key"), Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), Signature: []byte("sig"),
		}
		if err := InsertOutboxOperation(ctx, db, op, uint64(1000+i)); err != nil {
			t.Fatal(err)
		}
	}
	otherOp := OutboxOperation{
		OpID: mustNewID(t), WorkspaceID: otherWorkspaceID, DeviceID: repoTestDeviceID(t, 0x02),
		Clock: repoClock(t, 2000, 0, 0x02),
		KeyID: []byte("key"), Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), Signature: []byte("sig"),
	}
	if err := InsertOutboxOperation(ctx, db, otherOp, 2000); err != nil {
		t.Fatal(err)
	}

	count, err := repo.CountPending(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("CountPending = %d, want 3", count)
	}

	pending, err := repo.Pending(ctx, workspaceID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkPushed(ctx, workspaceID, []coresync.PushResult{{OpID: pending[0].OpID, Sequence: 1}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	count, err = repo.CountPending(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("CountPending after MarkPushed = %d, want 2", count)
	}

	otherCount, err := repo.CountPending(ctx, otherWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if otherCount != 1 {
		t.Fatalf("CountPending for other workspace = %d, want 1 (must not see the first workspace's operations)", otherCount)
	}
}

func TestSyncRepositoryCountQuarantineCountsOnlyQuarantinedEntriesForTheWorkspace(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	repo, err := NewSyncRepository(db, "test-transport")
	if err != nil {
		t.Fatal(err)
	}

	workspaceID := seedWorkspace(t, db)
	otherWorkspaceID := seedWorkspace(t, db)

	first := testWireOperation(t, workspaceID, 1, 0x03)
	second := testWireOperation(t, workspaceID, 2, 0x03)
	if err := repo.Quarantine(ctx, first, "bad_signature", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Quarantine(ctx, second, "bad_signature", time.Now()); err != nil {
		t.Fatal(err)
	}
	otherEntry := testWireOperation(t, otherWorkspaceID, 1, 0x03)
	if err := repo.Quarantine(ctx, otherEntry, "bad_signature", time.Now()); err != nil {
		t.Fatal(err)
	}

	count, err := repo.CountQuarantine(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("CountQuarantine = %d, want 2", count)
	}

	if err := repo.RetryQuarantined(ctx, workspaceID, first.OpID); err != nil {
		t.Fatal(err)
	}

	count, err = repo.CountQuarantine(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("CountQuarantine after RetryQuarantined = %d, want 1", count)
	}

	otherCount, err := repo.CountQuarantine(ctx, otherWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if otherCount != 1 {
		t.Fatalf("CountQuarantine for other workspace = %d, want 1 (must not see the first workspace's entries)", otherCount)
	}
}

func mustNewID(t testing.TB) model.ID {
	t.Helper()
	id, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
