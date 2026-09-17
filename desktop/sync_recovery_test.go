package main

import (
	"context"
	"crypto/tls"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/server"
)

// waitForSyncState polls SyncSummary until its state matches want, failing
// the test if it never does.
func waitForSyncState(t *testing.T, a *App, want presentation.SyncState) {
	t.Helper()
	waitFor(t, 5*time.Second, "sync state never reached "+string(want), func() bool {
		summary, err := a.SyncSummary()
		return err == nil && summary.State == want
	})
}

// startTLSServerOnAddr starts an httptest TLS server bound to addr (empty
// string picks a fresh ephemeral port) fronting handler with identity. It
// returns the started server and the address it actually bound, so a
// caller can later restart a server on that exact same address.
func startTLSServerOnAddr(t *testing.T, addr string, identity server.TLSIdentity, handler *server.Runtime) (*httptest.Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(identity.CertificateFile, identity.PrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(handler.API)
	ts.Listener.Close()
	ts.Listener = listener
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	return ts, listener.Addr().String()
}

// TestSyncRecoversAutomaticallyAfterTheServerBecomesReachableAgain covers
// task 3.8's "server disappearance/recovery" regression: local editing and
// queuing must continue while the configured server is completely
// unreachable, and once a server with the same identity becomes reachable
// again at the same address - exactly what a restarted home server looks
// like from the client's side - the coordinator's own retry loop drains the
// queued note without any reconnect or other user action
// (specs/sync-engine's "Incremental synchronization and notification"
// requirement).
func TestSyncRecoversAutomaticallyAfterTheServerBecomesReachableAgain(t *testing.T) {
	cfg := server.DefaultConfig()
	cfg.Server.DataDirectory = t.TempDir()
	cfg.Backups.Enabled = false
	cfg.Limits.RequestsPerSecond = 10000
	cfg.Limits.RequestBurst = 10000
	runtime, err := server.Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })

	ts, addr := startTLSServerOnAddr(t, "127.0.0.1:0", runtime.TLSIdentity, runtime)

	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	invite, err := runtime.Storage.CreateInvite(context.Background(), "recovery", time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConnectServer(ConnectServerRequest{
		URL: ts.URL, InviteCode: invite.Code, Fingerprint: runtime.TLSIdentity.Fingerprint,
		SecurityMode: "pinned", DeviceName: "recovery",
	}); err != nil {
		t.Fatalf("ConnectServer: %v", err)
	}
	waitForSyncState(t, a, presentation.SyncStateCurrent)

	// Take the server down. Every attempt from here on must fail with a
	// retryable connectivity classification instead of corrupting local
	// state or blocking the editor.
	ts.Close()

	note, err := a.CreateNote("", "Written while the server is down")
	if err != nil {
		t.Fatalf("CreateNote while server is down: %v", err)
	}
	if err := a.SyncNow(); err != nil {
		t.Fatalf("SyncNow while server is down: %v", err)
	}
	waitForSyncState(t, a, presentation.SyncStateOffline)
	if summary, err := a.SyncSummary(); err != nil || summary.PendingCount == 0 {
		t.Fatalf("SyncSummary while offline = %+v, %v; want the note still queued", summary, err)
	}
	if _, err := a.GetNote(note.ID); err != nil {
		t.Fatalf("GetNote while server is down: %v", err)
	}

	// Bring a server with the same identity back up on the exact same
	// address: no client-side reconnect, no re-entered credentials.
	recovered, _ := startTLSServerOnAddr(t, addr, runtime.TLSIdentity, runtime)
	t.Cleanup(recovered.Close)

	if err := a.SyncNow(); err != nil {
		t.Fatalf("SyncNow after recovery: %v", err)
	}
	waitForSyncState(t, a, presentation.SyncStateCurrent)
	if summary, err := a.SyncSummary(); err != nil || summary.PendingCount != 0 {
		t.Fatalf("SyncSummary after recovery = %+v, %v; want the queued note drained", summary, err)
	}
}

// TestSyncSurfacesActionRequiredWhenTheServerTLSIdentityChanges covers task
// 3.8's "TLS identity change" regression end to end, against real TLS
// handshakes rather than a fake transport (specs/release-quality's "TLS
// identity changes" scenario: "WHEN a configured server presents an
// unexpected certificate identity THEN synchronization stops before
// credentials or operations are sent while local use continues and trust
// action is requested"). A fresh server.Initialize on the same address
// (simulating a home server whose data directory was reinitialized) gets a
// brand-new self-signed identity, so the client's pinned fingerprint from
// the original connection no longer matches - exactly what a pinned
// device sees if it were ever pointed at an impostor.
func TestSyncSurfacesActionRequiredWhenTheServerTLSIdentityChanges(t *testing.T) {
	newTestServer := func() *server.Runtime {
		cfg := server.DefaultConfig()
		cfg.Server.DataDirectory = t.TempDir()
		cfg.Backups.Enabled = false
		cfg.Limits.RequestsPerSecond = 10000
		cfg.Limits.RequestBurst = 10000
		runtime, err := server.Initialize(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { runtime.Close() })
		return runtime
	}

	original := newTestServer()
	ts, addr := startTLSServerOnAddr(t, "127.0.0.1:0", original.TLSIdentity, original)

	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	invite, err := original.Storage.CreateInvite(context.Background(), "identity-change", time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConnectServer(ConnectServerRequest{
		URL: ts.URL, InviteCode: invite.Code, Fingerprint: original.TLSIdentity.Fingerprint,
		SecurityMode: "pinned", DeviceName: "identity-change",
	}); err != nil {
		t.Fatalf("ConnectServer: %v", err)
	}
	waitForSyncState(t, a, presentation.SyncStateCurrent)
	ts.Close()

	// A note created now must remain queued locally, never silently sent to
	// whatever answers on addr next.
	note, err := a.CreateNote("", "Written before the identity change")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	impostor := newTestServer()
	impostorServer, _ := startTLSServerOnAddr(t, addr, impostor.TLSIdentity, impostor)
	t.Cleanup(impostorServer.Close)

	if err := a.SyncNow(); err != nil {
		t.Fatalf("SyncNow against the changed identity: %v", err)
	}
	waitForSyncState(t, a, presentation.SyncStateActionRequired)
	summary, err := a.SyncSummary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.ActionRequired != presentation.RecoveryActionReviewConnection {
		t.Fatalf("ActionRequired = %q, want review_connection", summary.ActionRequired)
	}
	if summary.PendingCount == 0 {
		t.Fatal("PendingCount = 0, want the note still queued - it must never have reached the impostor")
	}

	if _, err := a.GetNote(note.ID); err != nil {
		t.Fatalf("GetNote: %v", err)
	}
}
