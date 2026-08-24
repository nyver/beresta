package main

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/sharecode"
	"github.com/beresta-app/beresta/server"
)

// startDesktopE2EServer runs a real *server.Runtime behind a real TLS
// listener using the runtime's own generated certificate, mirroring
// server/sharing_e2e_test.go's startE2EServer: ConnectServer's certificate
// pinning must see the actual TLS leaf certificate's fingerprint, which
// httptest's own auto-generated certificate would not match.
func startDesktopE2EServer(t *testing.T) (*server.Runtime, string) {
	t.Helper()
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

	pair, err := tls.LoadX509KeyPair(runtime.TLSIdentity.CertificateFile, runtime.TLSIdentity.PrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(runtime.API)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return runtime, ts.URL
}

// connectDesktopE2EActor creates a fresh local account on a and registers it
// against runtime with a new single-use invite, mirroring
// server/sharing_e2e_test.go's enrollE2EActor at the App-bound-method layer.
func connectDesktopE2EActor(t *testing.T, a *App, runtime *server.Runtime, baseURL, name string) {
	t.Helper()
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple " + name}); err != nil {
		t.Fatalf("CreateAccount(%s): %v", name, err)
	}
	invite, err := runtime.Storage.CreateInvite(context.Background(), name, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("CreateInvite(%s): %v", name, err)
	}
	if _, err := a.ConnectServer(ConnectServerRequest{
		URL: baseURL, InviteCode: invite.Code, Fingerprint: runtime.TLSIdentity.Fingerprint,
		SecurityMode: "pinned", DeviceName: name,
	}); err != nil {
		t.Fatalf("ConnectServer(%s): %v", name, err)
	}
}

// TestWorkspaceSharingAcrossTwoAccounts drives ExportIdentity, ShareWorkspace,
// AcceptWorkspaceGrant, ListWorkspaces, and SetActiveWorkspace exactly as the
// desktop and mobile Sync panels will call them, against a real server: the
// household journey documented in docs/android-user-guide.md, proved at the
// bound-method boundary rather than only at core/account and server/api.go's
// own layers (already covered by server/sharing_e2e_test.go).
func TestWorkspaceSharingAcrossTwoAccounts(t *testing.T) {
	runtime, baseURL := startDesktopE2EServer(t)

	owner := newTestApp(t)
	connectDesktopE2EActor(t, owner, runtime, baseURL, "owner")
	joiner := newTestApp(t)
	connectDesktopE2EActor(t, joiner, runtime, baseURL, "joiner")

	if _, err := owner.ShareWorkspace("not-a-valid-identity-code"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("ShareWorkspace(malformed identity code) error = %v, want %s", err, ErrCodeInvalidInput)
	}
	if _, err := joiner.AcceptWorkspaceGrant("not-a-valid-grant-code"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("AcceptWorkspaceGrant(malformed grant code) error = %v, want %s", err, ErrCodeInvalidInput)
	}

	identityCode, err := joiner.ExportIdentity()
	if err != nil {
		t.Fatalf("ExportIdentity: %v", err)
	}
	grantCode, err := owner.ShareWorkspace(identityCode)
	if err != nil {
		t.Fatalf("ShareWorkspace: %v", err)
	}

	summary, err := joiner.AcceptWorkspaceGrant(grantCode)
	if err != nil {
		t.Fatalf("AcceptWorkspaceGrant: %v", err)
	}
	if summary.Role != "member" {
		t.Fatalf("joiner role = %q, want %q", summary.Role, "member")
	}
	if summary.MemberCount != 2 {
		t.Fatalf("joiner-visible member count = %d, want 2", summary.MemberCount)
	}
	if !summary.Active {
		t.Fatal("expected the newly accepted workspace to become active")
	}
	sharedWorkspaceID := summary.WorkspaceID

	joinerWorkspaces, err := joiner.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces(joiner): %v", err)
	}
	if len(joinerWorkspaces) != 2 {
		t.Fatalf("joiner workspaces = %+v, want 2 entries", joinerWorkspaces)
	}
	var joinerOwnWorkspaceID string
	for _, ws := range joinerWorkspaces {
		switch ws.WorkspaceID {
		case sharedWorkspaceID:
			if !ws.Active || ws.Role != "member" {
				t.Fatalf("shared workspace entry = %+v, want active member", ws)
			}
		default:
			joinerOwnWorkspaceID = ws.WorkspaceID
			if ws.Active || ws.Role != "owner" {
				t.Fatalf("joiner's own workspace entry = %+v, want inactive owner", ws)
			}
		}
	}
	if joinerOwnWorkspaceID == "" {
		t.Fatal("expected joiner to still hold its own solo workspace alongside the shared one")
	}

	ownerWorkspaces, err := owner.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces(owner): %v", err)
	}
	if len(ownerWorkspaces) != 1 || ownerWorkspaces[0].WorkspaceID != sharedWorkspaceID ||
		ownerWorkspaces[0].Role != "owner" || ownerWorkspaces[0].MemberCount != 2 {
		t.Fatalf("owner workspaces = %+v", ownerWorkspaces)
	}

	if err := joiner.SetActiveWorkspace(joinerOwnWorkspaceID); err != nil {
		t.Fatalf("SetActiveWorkspace(own): %v", err)
	}
	afterSwitch, err := joiner.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces(joiner) after switch: %v", err)
	}
	for _, ws := range afterSwitch {
		if ws.WorkspaceID == joinerOwnWorkspaceID && !ws.Active {
			t.Fatalf("expected %s active after SetActiveWorkspace, got %+v", joinerOwnWorkspaceID, afterSwitch)
		}
		if ws.WorkspaceID == sharedWorkspaceID && ws.Active {
			t.Fatalf("expected the shared workspace no longer active, got %+v", afterSwitch)
		}
	}

	if err := joiner.SetActiveWorkspace("not-a-real-id"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("SetActiveWorkspace(garbage id) error = %v, want %s", err, ErrCodeInvalidInput)
	}
	unknownID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := joiner.SetActiveWorkspace(unknownID.String()); !isAppErrorCode(err, ErrCodeWorkspaceNotHeld) {
		t.Fatalf("SetActiveWorkspace(unknown id) error = %v, want %s", err, ErrCodeWorkspaceNotHeld)
	}
}

// TestShareWorkspaceAndAcceptWorkspaceGrantRequireSyncEnabled covers the
// guard rails in ShareWorkspace and AcceptWorkspaceGrant that reject calls
// made before ConnectServer has ever run, without needing a real server.
func TestShareWorkspaceAndAcceptWorkspaceGrantRequireSyncEnabled(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	if _, err := a.ShareWorkspace("beresta://identity?user=x&key=01"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("ShareWorkspace before sync enabled error = %v, want %s", err, ErrCodeInvalidInput)
	}
	if _, err := a.AcceptWorkspaceGrant("beresta://grant?workspace=x&key=01&authority=02&sig=03"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("AcceptWorkspaceGrant before sync enabled error = %v, want %s", err, ErrCodeInvalidInput)
	}
}

// TestExportIdentityRoundTripsAccountIdentity confirms ExportIdentity emits
// exactly this account's own identity, with no server or sync requirement.
func TestExportIdentityRoundTripsAccountIdentity(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	info, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	code, err := a.ExportIdentity()
	if err != nil {
		t.Fatalf("ExportIdentity: %v", err)
	}
	userID, _, err := sharecode.DecodeIdentity(code)
	if err != nil {
		t.Fatalf("decode ExportIdentity code: %v", err)
	}
	if userID.String() != info.AccountID {
		t.Fatalf("ExportIdentity user id = %s, want %s", userID, info.AccountID)
	}

	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}
	if _, err := a.ExportIdentity(); !isAppErrorCode(err, ErrCodeLocked) {
		t.Fatalf("ExportIdentity while locked error = %v, want %s", err, ErrCodeLocked)
	}
}
