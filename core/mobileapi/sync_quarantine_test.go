package mobileapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

// waitFor polls until check() returns true or timeout elapses, failing the
// test with message on timeout.
func waitFor(t *testing.T, timeout time.Duration, message string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRetryQuarantinedReattachesAPermanentlyDetachedCoordinator proves task
// 3.7's retry action actually resumes synchronization on Android/Flutter:
// a quarantined worker exits and detaches itself permanently (see
// coresync.Coordinator.Attach's doc comment), so RetryQuarantined must
// reattach a fresh worker before triggering it, mirroring desktop's
// identical fix and SyncNow's own reattach-then-trigger pattern.
func TestRetryQuarantinedReattachesAPermanentlyDetachedCoordinator(t *testing.T) {
	runtime, baseURL := startMobileE2EServer(t)
	service, _ := newConnectedMobileService(t, runtime, baseURL, "quarantine")

	service.mu.Lock()
	workspaceID, repository := service.workspaceID, service.repository
	service.mu.Unlock()
	if repository == nil {
		t.Fatal("no sync repository after connecting")
	}

	// Seed a fake quarantined operation directly - this test exercises the
	// detach/reattach mechanics, not real-server verification failure, so
	// the operation's content need not itself be rejectable: QuarantineBlocked
	// only checks that a row exists, before the worker ever pulls anything.
	opID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	deviceID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repository.Quarantine(ctx, coresync.WireOperation{
		OpID: opID, WorkspaceID: workspaceID, DeviceID: deviceID, Sequence: 1,
		KeyID: []byte("key"), Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext"), Signature: []byte("signature"),
	}, "test_reason", time.Now()); err != nil {
		t.Fatalf("Quarantine: %v", err)
	}

	// SyncNow's own blanket quarantine-retry-on-manual-sync (see its doc
	// comment) would immediately clear this seeded entry before the worker
	// ever observes it as blocking, defeating the point of this test, so
	// trigger a cycle directly instead of going through SyncNow.
	service.mu.Lock()
	coordinator := service.coordinator
	service.mu.Unlock()
	if coordinator == nil || !coordinator.Trigger() {
		t.Fatal("could not trigger an initial sync cycle")
	}
	waitFor(t, 5*time.Second, "coordinator never detached after the worker discovered the quarantined operation", func() bool {
		service.mu.Lock()
		coordinator := service.coordinator
		service.mu.Unlock()
		return coordinator == nil || !coordinator.Enabled()
	})

	encoded, err := service.ListSyncQuarantine()
	if err != nil {
		t.Fatalf("ListSyncQuarantine: %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(encoded), &entries); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", encoded, err)
	}
	if len(entries) != 1 || entries[0]["operation_id"] != opID.String() {
		t.Fatalf("ListSyncQuarantine() = %s, want one entry for %s", encoded, opID)
	}

	if err := service.RetryQuarantined(opID.String()); err != nil {
		t.Fatalf("RetryQuarantined: %v", err)
	}

	waitFor(t, 5*time.Second, "coordinator was not reattached by RetryQuarantined", func() bool {
		service.mu.Lock()
		coordinator := service.coordinator
		service.mu.Unlock()
		return coordinator != nil && coordinator.Enabled()
	})

	encoded, err = service.ListSyncQuarantine()
	if err != nil {
		t.Fatalf("ListSyncQuarantine after retry: %v", err)
	}
	if encoded != "[]" {
		t.Fatalf("ListSyncQuarantine() after retry = %s, want none (RetryQuarantined discards the locally-rejected copy)", encoded)
	}
}
