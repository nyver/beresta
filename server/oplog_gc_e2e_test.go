package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/store"
)

// TestOperationLogGarbageCollectionRespectsRetentionAndSurvivesRecovery
// exercises task 9.6's local operation-log/snapshot garbage collection: it
// leaves recent history untouched, only collects history covered by a
// device-acknowledged snapshot at least the 30-day floor old, supports
// dry-run preview, and leaves the account fully able to keep syncing
// afterward (the "recovery" property - GC must never leave local state the
// sync worker cannot continue from).
func TestOperationLogGarbageCollectionRespectsRetentionAndSurvivesRecovery(t *testing.T) {
	runtime, baseURL := startE2EServer(t)
	alice := enrollE2EActor(t, runtime, baseURL, "alice")
	ctx := context.Background()

	commitNoteE2E(t, alice, alice.WorkspaceID, "First", "first body")
	if err := syncE2E(t, alice, alice.WorkspaceID); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	// Pull the just-pushed operation back so the local cursor advances past
	// it - exactly what CreateWorkspaceSnapshot requires (see
	// core/account/workspace_snapshot.go).
	if err := syncE2E(t, alice, alice.WorkspaceID); err != nil {
		t.Fatalf("echo-back pull: %v", err)
	}

	repo, err := store.NewSyncRepository(alice.Account.DB(), "http")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := alice.Account.CreateWorkspaceSnapshot(ctx, alice.WorkspaceID, repo)
	if err != nil {
		t.Fatalf("CreateWorkspaceSnapshot: %v", err)
	}
	if snapshot.BaseSequence == 0 {
		t.Fatal("expected a non-empty snapshot")
	}

	rowCount := func(table string) int {
		t.Helper()
		var count int
		if err := alice.Account.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	inboxBefore, outboxBefore := rowCount("inbox"), rowCount("outbox")
	if inboxBefore == 0 && outboxBefore == 0 {
		t.Fatal("test setup should have produced local operation-log rows")
	}

	// Too recent: nothing is eligible yet, even though a snapshot exists.
	report, err := alice.Account.RunOperationLogGarbageCollection(ctx, time.Now(), false)
	if err != nil {
		t.Fatalf("RunOperationLogGarbageCollection (recent): %v", err)
	}
	if report.InboxRowsCollected != 0 || report.OutboxRowsCollected != 0 || report.AppliedRowsCollected != 0 {
		t.Fatalf("expected nothing collected before the 30-day floor, got %+v", report)
	}
	if got := rowCount("inbox"); got != inboxBefore {
		t.Fatalf("inbox rows changed despite being within the retention floor: before=%d after=%d", inboxBefore, got)
	}

	future := time.Now().AddDate(0, 0, 31)

	// Dry-run previews without mutating anything.
	dryReport, err := alice.Account.RunOperationLogGarbageCollection(ctx, future, true)
	if err != nil {
		t.Fatalf("RunOperationLogGarbageCollection (dry-run): %v", err)
	}
	if dryReport.InboxRowsCollected == 0 && dryReport.OutboxRowsCollected == 0 {
		t.Fatalf("expected the dry run to report eligible rows past the retention floor: %+v", dryReport)
	}
	if got := rowCount("inbox"); got != inboxBefore {
		t.Fatalf("dry-run must not mutate state: inbox before=%d after=%d", inboxBefore, got)
	}

	// A real run past the floor actually collects it.
	realReport, err := alice.Account.RunOperationLogGarbageCollection(ctx, future, false)
	if err != nil {
		t.Fatalf("RunOperationLogGarbageCollection: %v", err)
	}
	if realReport.InboxRowsCollected != dryReport.InboxRowsCollected ||
		realReport.OutboxRowsCollected != dryReport.OutboxRowsCollected ||
		realReport.AppliedRowsCollected != dryReport.AppliedRowsCollected ||
		realReport.SnapshotRowsCollected != dryReport.SnapshotRowsCollected {
		t.Fatalf("dry-run and real run should report identical counts: dry=%+v real=%+v", dryReport, realReport)
	}
	if got := rowCount("inbox"); got >= inboxBefore && inboxBefore > 0 {
		t.Fatalf("expected inbox rows to be collected: before=%d after=%d", inboxBefore, got)
	}

	// Recovery property: the account can still create, sync, and read notes
	// normally after garbage collection.
	secondNoteID := commitNoteE2E(t, alice, alice.WorkspaceID, "Second", "after gc")
	if err := syncE2E(t, alice, alice.WorkspaceID); err != nil {
		t.Fatalf("sync after GC: %v", err)
	}
	if text, err := readNoteTextE2E(t, alice, alice.WorkspaceID, secondNoteID); err != nil || text != "after gc" {
		t.Fatalf("expected to read the post-GC note normally: text=%q err=%v", text, err)
	}

	// Running it again is idempotent: nothing new to collect immediately.
	again, err := alice.Account.RunOperationLogGarbageCollection(ctx, future, false)
	if err != nil {
		t.Fatal(err)
	}
	if again.SnapshotRowsCollected != 0 {
		t.Fatalf("expected no further snapshot rows to collect on a second run: %+v", again)
	}
}
