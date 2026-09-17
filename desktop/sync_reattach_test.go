package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestSyncReattachesAutomaticallyAfterUnlockWhenAServerWasPreviouslyConfigured
// proves activate's background reconnect (desktop/app.go) actually restores
// a previously configured server's sync worker after a lock/unlock cycle,
// without the caller ever calling ConnectServer again itself - the
// "restore pending synchronization automatically after restart/unlock"
// behavior task 3.4 requires.
func TestSyncReattachesAutomaticallyAfterUnlockWhenAServerWasPreviouslyConfigured(t *testing.T) {
	runtime, baseURL := startDesktopE2EServer(t)
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	passphrase := "correct horse battery staple"
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: passphrase}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	invite, err := runtime.Storage.CreateInvite(context.Background(), "restart", time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConnectServer(ConnectServerRequest{
		URL: baseURL, InviteCode: invite.Code, Fingerprint: runtime.TLSIdentity.Fingerprint,
		SecurityMode: "pinned", DeviceName: "restart",
	}); err != nil {
		t.Fatalf("ConnectServer: %v", err)
	}
	if !a.settings.SyncEnabled {
		t.Fatal("settings.SyncEnabled is false after ConnectServer, want true (persisted so a later unlock knows to reconnect)")
	}

	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}
	a.mu.Lock()
	coordinatorWhileLocked := a.syncCoordinator
	a.mu.Unlock()
	if coordinatorWhileLocked != nil {
		t.Fatal("a sync coordinator is still attached while the account is locked")
	}

	if _, err := a.UnlockAccount(UnlockAccountRequest{DatabasePath: dbPath, Passphrase: passphrase}); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}

	// activate's reconnect runs in a background goroutine; poll for it to
	// finish instead of assuming it has by the time UnlockAccount returns.
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		attached := a.syncCoordinator != nil
		a.mu.Unlock()
		if attached {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no sync coordinator was reattached after unlock within the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	deadline = time.Now().Add(5 * time.Second)
	for {
		if status := a.SyncStatus(); status == "current" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("SyncStatus never reached %q after unlock, last was %q", "current", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSyncStaysLocalOnlyAfterUnlockWithNoServerConfigured proves the other
// half of task 3.4's contract: an account that never had a server
// configured must not grow one just because it was locked and unlocked -
// activate's reconnect is gated on settings.SyncEnabled.
func TestSyncStaysLocalOnlyAfterUnlockWithNoServerConfigured(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	passphrase := "correct horse battery staple"
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: passphrase}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}
	if _, err := a.UnlockAccount(UnlockAccountRequest{DatabasePath: dbPath, Passphrase: passphrase}); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}

	// Give any (wrongly-fired) reconnect goroutine a moment to have run.
	time.Sleep(200 * time.Millisecond)

	a.mu.Lock()
	coordinator, httpTransport := a.syncCoordinator, a.httpTransport
	a.mu.Unlock()
	if coordinator != nil || httpTransport != nil {
		t.Fatal("a sync coordinator or HTTP transport exists after unlocking an account that never had a server configured")
	}
	if status := a.SyncStatus(); status != "disabled" {
		t.Fatalf("SyncStatus() = %q, want %q", status, "disabled")
	}
}

// TestConnectServerNeverLeavesALiveCoordinatorAttachedAfterTheAccountIsLocked
// races activate's background reconnect-after-unlock against the account
// being locked again immediately, across several trials to give the
// scheduler a chance to interleave them either way. Regardless of which
// side wins, a locked account must never end up with a live attached sync
// coordinator - the invariant the syncGeneration staleness guard in
// ConnectServer (desktop/sync.go) exists to protect.
func TestConnectServerNeverLeavesALiveCoordinatorAttachedAfterTheAccountIsLocked(t *testing.T) {
	runtime, baseURL := startDesktopE2EServer(t)

	const trials = 8
	for trial := 0; trial < trials; trial++ {
		a := newTestApp(t)
		dbPath := testDatabasePath(t, a)
		if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
			t.Fatalf("trial %d: CreateAccount: %v", trial, err)
		}
		invite, err := runtime.Storage.CreateInvite(context.Background(), "racer", time.Hour, time.Now())
		if err != nil {
			t.Fatalf("trial %d: CreateInvite: %v", trial, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = a.ConnectServer(ConnectServerRequest{
				URL: baseURL, InviteCode: invite.Code, Fingerprint: runtime.TLSIdentity.Fingerprint,
				SecurityMode: "pinned", DeviceName: "racer",
			})
		}()
		go func() {
			defer wg.Done()
			_ = a.LockAccount()
		}()
		wg.Wait()

		a.mu.Lock()
		locked := a.account == nil
		coordinatorAttached := a.syncCoordinator != nil
		a.mu.Unlock()
		if locked && coordinatorAttached {
			t.Fatalf("trial %d: account is locked but a sync coordinator is still attached - a stale ConnectServer reconnect resurrected sync after lock", trial)
		}
		// Leave the account locked so t.Cleanup's teardown never has to
		// tear down a still-open one, regardless of which goroutine won.
		_ = a.LockAccount()
	}
}
