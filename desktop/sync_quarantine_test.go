package main

import (
	"context"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

// waitFor polls until check() returns true or deadline elapses, failing the
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

// TestRetrySyncQuarantineReattachesAPermanentlyDetachedCoordinator proves
// task 3.7's retry action actually resumes synchronization: a quarantined
// worker exits and detaches itself permanently (see
// coresync.Coordinator.Attach's doc comment), so a retry that only calls
// Trigger on the stale coordinator reference would silently do nothing.
// RetrySyncQuarantine must reattach a fresh worker first.
func TestRetrySyncQuarantineReattachesAPermanentlyDetachedCoordinator(t *testing.T) {
	runtime, baseURL := startDesktopE2EServer(t)
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	passphrase := "correct horse battery staple"
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: passphrase}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	invite, err := runtime.Storage.CreateInvite(context.Background(), "quarantine", time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConnectServer(ConnectServerRequest{
		URL: baseURL, InviteCode: invite.Code, Fingerprint: runtime.TLSIdentity.Fingerprint,
		SecurityMode: "pinned", DeviceName: "quarantine",
	}); err != nil {
		t.Fatalf("ConnectServer: %v", err)
	}

	_, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	repository := a.syncRepository
	a.mu.Unlock()
	if repository == nil {
		t.Fatal("no sync repository after ConnectServer")
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

	if err := a.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	waitFor(t, 5*time.Second, "coordinator never detached after the worker discovered the quarantined operation", func() bool {
		a.mu.Lock()
		coordinator := a.syncCoordinator
		a.mu.Unlock()
		return coordinator == nil || !coordinator.Enabled()
	})

	entries, err := a.ListSyncQuarantine()
	if err != nil {
		t.Fatalf("ListSyncQuarantine: %v", err)
	}
	if len(entries) != 1 || entries[0].OperationID != opID.String() {
		t.Fatalf("ListSyncQuarantine() = %+v, want one entry for %s", entries, opID)
	}

	if err := a.RetrySyncQuarantine(opID.String()); err != nil {
		t.Fatalf("RetrySyncQuarantine: %v", err)
	}

	waitFor(t, 5*time.Second, "coordinator was not reattached by RetrySyncQuarantine", func() bool {
		a.mu.Lock()
		coordinator := a.syncCoordinator
		a.mu.Unlock()
		return coordinator != nil && coordinator.Enabled()
	})

	entries, err = a.ListSyncQuarantine()
	if err != nil {
		t.Fatalf("ListSyncQuarantine after retry: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ListSyncQuarantine() after retry = %+v, want none (RetryQuarantined discards the locally-rejected copy)", entries)
	}
}
