package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

// TestLANSyncBudgetForOneThousandOperations is task 9.13's LAN
// synchronization release benchmark: the release-quality spec requires
// 1,000 incremental operations to synchronize over a local network within
// three seconds. httptest's loopback server stands in for "local network"
// here (near-zero transit latency, matching a real LAN's dominant
// characteristic compared to a wide-area link); what this measures is the
// client/server processing overhead per operation - encryption, signing,
// HTTP round trips, and server-side verification and sequencing - which
// does not change with the physical network.
func TestLANSyncBudgetForOneThousandOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 1,000-operation sync benchmark in -short mode")
	}
	const operationCount = 1000
	const budget = 3 * time.Second

	runtime, baseURL := startE2EServer(t)
	alice := enrollE2EActor(t, runtime, baseURL, "alice")

	ctx := context.Background()
	for i := 0; i < operationCount; i++ {
		if _, err := alice.Account.CreateNote(ctx, alice.WorkspaceID, model.Nil, "Benchmark note"); err != nil {
			t.Fatalf("CreateNote %d: %v", i, err)
		}
	}

	started := time.Now()
	if err := syncE2E(t, alice, alice.WorkspaceID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	elapsed := time.Since(started)

	t.Logf("synchronized %d operations in %v (budget %v)", operationCount, elapsed, budget)
	if elapsed > budget {
		t.Fatalf("synchronizing %d operations took %v, exceeding the %v release budget", operationCount, elapsed, budget)
	}

	var pushedCount int64
	if err := runtime.Database.QueryRow(`SELECT latest_seq FROM workspaces WHERE workspace_id = ?`, alice.WorkspaceID.String()).Scan(&pushedCount); err != nil {
		t.Fatal(err)
	}
	if pushedCount != operationCount {
		t.Fatalf("expected all %d operations to reach the server, server latest_seq=%d", operationCount, pushedCount)
	}
}
