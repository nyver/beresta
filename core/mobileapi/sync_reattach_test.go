package mobileapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/beresta-app/beresta/server"
)

// syncState decodes the "state" field out of a SyncSummary() JSON result,
// so tests can assert on it without depending on the full DTO shape.
func syncState(t *testing.T, encoded string) string {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", encoded, err)
	}
	state, _ := decoded["state"].(string)
	return state
}

// TestServiceReattachesAutomaticallyAfterUnlockWhenAServerWasPreviouslyConfigured
// proves activate's reconnectSavedServer (core/mobileapi/service.go) actually
// restores a previously configured server's sync worker after a lock/unlock
// cycle, without the caller calling ConnectServer again itself - the
// "restore pending synchronization automatically after restart/unlock"
// behavior task 3.4 requires, mirrored from desktop's identical test.
func TestServiceReattachesAutomaticallyAfterUnlockWhenAServerWasPreviouslyConfigured(t *testing.T) {
	runtime, baseURL := startMobileE2EServer(t)
	service, dbPath := newConnectedMobileService(t, runtime, baseURL, "restart")

	if err := service.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	service.mu.Lock()
	coordinatorWhileLocked := service.coordinator
	service.mu.Unlock()
	if coordinatorWhileLocked != nil {
		t.Fatal("a sync coordinator is still attached while the account is locked")
	}

	if _, err := service.UnlockAccount("unlock-restart", dbPath, "correct horse battery staple restart"); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}

	// activate's reconnectSavedServer runs in a background goroutine; poll
	// for it to finish instead of assuming it has by the time
	// UnlockAccount returns.
	deadline := time.Now().Add(5 * time.Second)
	for {
		service.mu.Lock()
		attached := service.coordinator != nil
		service.mu.Unlock()
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
		encoded, err := service.SyncSummary()
		if err != nil {
			t.Fatalf("SyncSummary: %v", err)
		}
		if state := syncState(t, encoded); state == "current" {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("SyncSummary state never reached %q after unlock, last was %q", "current", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestServiceSyncStaysLocalOnlyAfterUnlockWithNoServerConfigured proves the
// other half of task 3.4's contract: an account that never had a server
// configured must not grow one just because it was locked and unlocked -
// reconnectSavedServer is gated on the persisted config's Enabled flag.
func TestServiceSyncStaysLocalOnlyAfterUnlockWithNoServerConfigured(t *testing.T) {
	// retryTempDir's RemoveAll cleanup must run after service.Close closes
	// the account's SQLite handle, or Windows can still hold the file open
	// when cleanup tries to remove it (t.Cleanup runs LIFO, so TempDir must
	// be registered - via this call - before service.Close is).
	dbPath := retryTempDir(t) + "/beresta.db"
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	if _, err := service.CreateAccount("create-local", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := service.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := service.UnlockAccount("unlock-local", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}

	// Give any (wrongly-fired) reconnect goroutine a moment to have run.
	time.Sleep(200 * time.Millisecond)

	service.mu.Lock()
	coordinator, remote := service.coordinator, service.remote
	service.mu.Unlock()
	if coordinator != nil || remote != nil {
		t.Fatal("a sync coordinator or remote transport exists after unlocking an account that never had a server configured")
	}
	encoded, err := service.SyncSummary()
	if err != nil {
		t.Fatal(err)
	}
	if state := syncState(t, encoded); state != "local_only" {
		t.Fatalf("SyncSummary() state = %q, want %q", state, "local_only")
	}
}

// TestServiceConnectServerNeverLeavesALiveCoordinatorAttachedAfterLock races
// activate's background reconnectSavedServer against the account being
// locked again immediately, across several trials to give the scheduler a
// chance to interleave them either way. Regardless of which side wins, a
// locked account must never end up with a live attached sync coordinator -
// the invariant the syncGeneration staleness guard in ConnectServer and
// Lock (core/mobileapi/service.go) exists to protect.
func TestServiceConnectServerNeverLeavesALiveCoordinatorAttachedAfterLock(t *testing.T) {
	runtime, baseURL := startMobileE2EServer(t)

	const trials = 8
	for trial := 0; trial < trials; trial++ {
		service, err := NewService(newTestServiceDeviceSecret(t))
		if err != nil {
			t.Fatal(err)
		}
		dbPath := t.TempDir() + "/beresta.db"
		if _, err := service.CreateAccount("racer", dbPath, "correct horse battery staple"); err != nil {
			t.Fatalf("trial %d: CreateAccount: %v", trial, err)
		}
		invite, err := runtime.Storage.CreateInvite(context.Background(), "racer", time.Hour, time.Now())
		if err != nil {
			t.Fatalf("trial %d: CreateInvite: %v", trial, err)
		}
		config, err := marshal(connectConfig{
			URL: baseURL, InviteCode: invite.Code, Fingerprint: runtime.TLSIdentity.Fingerprint,
			SecurityMode: "pinned", DeviceName: "racer",
		})
		if err != nil {
			t.Fatalf("trial %d: marshal: %v", trial, err)
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = service.ConnectServer("connect-racer", config)
		}()
		go func() {
			defer wg.Done()
			_ = service.Lock()
		}()
		wg.Wait()

		service.mu.Lock()
		locked := service.account == nil
		coordinatorAttached := service.coordinator != nil
		service.mu.Unlock()
		if locked && coordinatorAttached {
			t.Fatalf("trial %d: account is locked but a sync coordinator is still attached - a stale ConnectServer reconnect resurrected sync after lock", trial)
		}
		service.Close()
	}
}

// startMobileTLSServerOnAddr serves runtime's API over TLS on addr ("" or a
// ":0" port picks a fresh one) and returns the server and the address it
// actually bound, so a test can stop it and later restart it on exactly the
// same address - what a restarted server looks like from the client side.
func startMobileTLSServerOnAddr(t *testing.T, addr string, runtime *server.Runtime) (*httptest.Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(runtime.TLSIdentity.CertificateFile, runtime.TLSIdentity.PrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(runtime.API)
	ts.Listener.Close()
	ts.Listener = listener
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	return ts, listener.Addr().String()
}

// TestServiceReattachesAfterUnlockWhileTheServerIsDown covers the regression
// where unlocking while the configured server was unreachable left the app
// permanently local-only: reconnectSavedServer's single attempt failed its
// preflight and nothing ever retried it. The worker must instead attach
// immediately, report offline rather than local-only while the server is
// down, and converge once the server is reachable again at the same address.
func TestServiceReattachesAfterUnlockWhileTheServerIsDown(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.Server.DataDirectory = retryTempDir(t)
	cfg.Backups.Enabled = false
	cfg.Limits.RequestsPerSecond = 10000
	cfg.Limits.RequestBurst = 10000
	runtime, err := server.Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	ts, addr := startMobileTLSServerOnAddr(t, "127.0.0.1:0", runtime)

	service, dbPath := newConnectedMobileService(t, runtime, ts.URL, "unlock-offline")
	if err := service.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	ts.Close()
	if _, err := service.UnlockAccount("unlock-offline", dbPath, "correct horse battery staple unlock-offline"); err != nil {
		t.Fatalf("UnlockAccount while server is down: %v", err)
	}
	waitForServiceSyncState(t, service, "offline")

	recovered, _ := startMobileTLSServerOnAddr(t, addr, runtime)
	t.Cleanup(recovered.Close)

	// SyncNow skips the remaining backoff delay; the worker would otherwise
	// pick the server up on its own once that delay elapses.
	if err := service.SyncNow(); err != nil {
		t.Fatalf("SyncNow after recovery: %v", err)
	}
	waitForServiceSyncState(t, service, "current")
}

func waitForServiceSyncState(t *testing.T, service *Service, want string) {
	t.Helper()
	waitFor(t, 10*time.Second, "sync state never reached "+want, func() bool {
		encoded, err := service.SyncSummary()
		return err == nil && syncState(t, encoded) == want
	})
}
