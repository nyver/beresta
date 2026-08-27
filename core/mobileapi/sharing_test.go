package mobileapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/sharecode"
	"github.com/beresta-app/beresta/server"
)

// startMobileE2EServer runs a real *server.Runtime behind a real TLS
// listener using the runtime's own generated certificate, mirroring
// server/sharing_e2e_test.go's startE2EServer and desktop's
// startDesktopE2EServer: ConnectServer's certificate pinning must see the
// actual TLS leaf certificate's fingerprint, which httptest's own
// auto-generated certificate would not match.
func startMobileE2EServer(t *testing.T) (*server.Runtime, string) {
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

// newConnectedMobileService creates a fresh Service, an account, and
// registers it against runtime with a new single-use invite, mirroring
// server/sharing_e2e_test.go's enrollE2EActor at the Service-bound-method
// layer.
func newConnectedMobileService(t *testing.T, runtime *server.Runtime, baseURL, name string) (*Service, string) {
	t.Helper()
	// t.TempDir()'s RemoveAll cleanup must run after service.Close closes the
	// account's SQLite handle, or Windows can still hold the file open when
	// cleanup tries to remove it (t.Cleanup runs LIFO, so TempDir must be
	// registered - via this call - before service.Close is).
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService(%s): %v", name, err)
	}
	t.Cleanup(service.Close)

	if _, err := service.CreateAccount("create-"+name, dbPath, "correct horse battery staple "+name); err != nil {
		t.Fatalf("CreateAccount(%s): %v", name, err)
	}
	invite, err := runtime.Storage.CreateInvite(context.Background(), name, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("CreateInvite(%s): %v", name, err)
	}
	config, err := json.Marshal(connectConfig{
		URL: baseURL, InviteCode: invite.Code, Fingerprint: runtime.TLSIdentity.Fingerprint,
		SecurityMode: "pinned", DeviceName: name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConnectServer("connect-"+name, string(config)); err != nil {
		t.Fatalf("ConnectServer(%s): %v", name, err)
	}
	return service, dbPath
}

// TestServiceWorkspaceSharingAcrossTwoAccounts drives ExportIdentity,
// ShareWorkspace, AcceptWorkspaceGrant, ListWorkspaces, and
// SetActiveWorkspace exactly as the mobile Sync sheet will call them,
// against a real server: the household journey documented in
// docs/android-user-guide.md, proved at the Service-bound-method boundary
// rather than only at core/account and server/api.go's own layers (already
// covered by server/sharing_e2e_test.go).
func TestServiceWorkspaceSharingAcrossTwoAccounts(t *testing.T) {
	runtime, baseURL := startMobileE2EServer(t)

	owner, _ := newConnectedMobileService(t, runtime, baseURL, "owner")
	joiner, joinerDBPath := newConnectedMobileService(t, runtime, baseURL, "joiner")

	preexistingNotebookJSON, err := owner.CreateNotebook("create-preexisting-notebook", "", "Shared notebook")
	if err != nil {
		t.Fatalf("CreateNotebook(preexisting): %v", err)
	}
	preexistingNotebookID, ok := decodeJSON[map[string]any](t, preexistingNotebookJSON)["id"].(string)
	if !ok || preexistingNotebookID == "" {
		t.Fatalf("CreateNotebook(preexisting) returned no id: %s", preexistingNotebookJSON)
	}
	if _, err := owner.CreateNote("create-preexisting-note", preexistingNotebookID, "Shared before join"); err != nil {
		t.Fatalf("CreateNote(preexisting): %v", err)
	}
	if err := owner.SyncNow(); err != nil {
		t.Fatalf("owner SyncNow(preexisting): %v", err)
	}

	if _, err := owner.ShareWorkspace("share-bad", "not-a-valid-identity-code"); err == nil {
		t.Fatal("ShareWorkspace(malformed identity code) succeeded, want an error")
	}
	if _, err := joiner.AcceptWorkspaceGrant("accept-bad", "not-a-valid-grant-code"); err == nil {
		t.Fatal("AcceptWorkspaceGrant(malformed grant code) succeeded, want an error")
	}

	identityResponse, err := joiner.ExportIdentity("export-identity")
	if err != nil {
		t.Fatalf("ExportIdentity: %v", err)
	}
	identityCode := decodeJSON[map[string]string](t, identityResponse)["identity_code"]
	if identityCode == "" {
		t.Fatalf("ExportIdentity response missing identity_code: %v", identityResponse)
	}

	grantResponse, err := owner.ShareWorkspace("share", identityCode)
	if err != nil {
		t.Fatalf("ShareWorkspace: %v", err)
	}
	grantCode := decodeJSON[map[string]string](t, grantResponse)["grant_code"]
	if grantCode == "" {
		t.Fatalf("ShareWorkspace response missing grant_code: %v", grantResponse)
	}

	acceptResponse, err := joiner.AcceptWorkspaceGrant("accept", grantCode)
	if err != nil {
		t.Fatalf("AcceptWorkspaceGrant: %v", err)
	}
	summary := decodeJSON[workspaceSummary](t, acceptResponse)
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
	if _, err := owner.CreateNote("create-shared-note", "", "Shared from desktop"); err != nil {
		t.Fatalf("CreateNote(shared): %v", err)
	}
	if err := owner.SyncNow(); err != nil {
		t.Fatalf("owner SyncNow: %v", err)
	}
	if err := joiner.SyncNow(); err != nil {
		t.Fatalf("joiner SyncNow: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	sharedNoteReceived := false
	preexistingNoteReceived := false
	preexistingNotebookReceived := false
	for !sharedNoteReceived || !preexistingNoteReceived || !preexistingNotebookReceived {
		joinedNotesJSON, err := joiner.ListNotes("list-shared-notes")
		if err != nil {
			t.Fatalf("ListNotes(shared): %v", err)
		}
		for _, note := range decodeJSON[[]map[string]any](t, joinedNotesJSON) {
			if note["title"] == "Shared from desktop" {
				sharedNoteReceived = true
			}
			if note["title"] == "Shared before join" {
				preexistingNoteReceived = true
			}
		}
		joinedNotebooksJSON, err := joiner.ListNotebooks("list-shared-notebooks")
		if err != nil {
			t.Fatalf("ListNotebooks(shared): %v", err)
		}
		for _, notebook := range decodeJSON[[]map[string]any](t, joinedNotebooksJSON) {
			if notebook["name"] == "Shared notebook" {
				preexistingNotebookReceived = true
				break
			}
		}
		if sharedNoteReceived && preexistingNoteReceived && preexistingNotebookReceived {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("joining mobile service did not receive all workspace data: post-share note=%t, preexisting note=%t, preexisting notebook=%t, joiner sync error=%q, joiner notebooks=%s", sharedNoteReceived, preexistingNoteReceived, preexistingNotebookReceived, joiner.SyncError(), joinedNotebooksJSON)
		}
		if err := owner.SyncNow(); err != nil {
			t.Fatalf("owner SyncNow retry: %v", err)
		}
		if err := joiner.SyncNow(); err != nil {
			t.Fatalf("joiner SyncNow retry: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}

	joinerWorkspacesJSON, err := joiner.ListWorkspaces("list-joiner")
	if err != nil {
		t.Fatalf("ListWorkspaces(joiner): %v", err)
	}
	joinerWorkspaces := decodeJSON[[]workspaceSummary](t, joinerWorkspacesJSON)
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

	ownerWorkspacesJSON, err := owner.ListWorkspaces("list-owner")
	if err != nil {
		t.Fatalf("ListWorkspaces(owner): %v", err)
	}
	ownerWorkspaces := decodeJSON[[]workspaceSummary](t, ownerWorkspacesJSON)
	if len(ownerWorkspaces) != 1 || ownerWorkspaces[0].WorkspaceID != sharedWorkspaceID ||
		ownerWorkspaces[0].Role != "owner" || ownerWorkspaces[0].MemberCount != 2 {
		t.Fatalf("owner workspaces = %+v", ownerWorkspaces)
	}

	if err := joiner.SetActiveWorkspace("switch-own", joinerOwnWorkspaceID); err != nil {
		t.Fatalf("SetActiveWorkspace(own): %v", err)
	}
	afterSwitchJSON, err := joiner.ListWorkspaces("list-after-switch")
	if err != nil {
		t.Fatalf("ListWorkspaces(joiner) after switch: %v", err)
	}
	for _, ws := range decodeJSON[[]workspaceSummary](t, afterSwitchJSON) {
		if ws.WorkspaceID == joinerOwnWorkspaceID && !ws.Active {
			t.Fatalf("expected %s active after SetActiveWorkspace", joinerOwnWorkspaceID)
		}
		if ws.WorkspaceID == sharedWorkspaceID && ws.Active {
			t.Fatal("expected the shared workspace no longer active")
		}
	}

	if err := joiner.SetActiveWorkspace("switch-bad", "not-a-real-id"); err == nil {
		t.Fatal("SetActiveWorkspace(garbage id) succeeded, want an error")
	}
	unknownID, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := joiner.SetActiveWorkspace("switch-unknown", unknownID.String()); err == nil {
		t.Fatal("SetActiveWorkspace(unknown id) succeeded, want an error")
	}
	if err := joiner.SetActiveWorkspace("restore-shared", sharedWorkspaceID); err != nil {
		t.Fatalf("SetActiveWorkspace(shared): %v", err)
	}
	if err := joiner.Lock(); err != nil {
		t.Fatalf("Lock(joiner): %v", err)
	}
	if _, err := joiner.UnlockAccount("unlock-shared", joinerDBPath, "correct horse battery staple joiner"); err != nil {
		t.Fatalf("UnlockAccount(joiner): %v", err)
	}
	statusJSON, err := joiner.Status()
	if err != nil {
		t.Fatalf("Status(joiner): %v", err)
	}
	status := decodeJSON[map[string]any](t, statusJSON)
	if status["workspace_id"] != sharedWorkspaceID {
		t.Fatalf("workspace after unlock = %q, want shared workspace %q", status["workspace_id"], sharedWorkspaceID)
	}
}

// TestServiceShareWorkspaceAndAcceptWorkspaceGrantRequireSyncEnabled covers
// the guard rails in ShareWorkspace and AcceptWorkspaceGrant that reject
// calls made before ConnectServer has ever run, without needing a real
// server.
func TestServiceShareWorkspaceAndAcceptWorkspaceGrantRequireSyncEnabled(t *testing.T) {
	// t.TempDir() must be registered (called) before service.Close, since
	// t.Cleanup runs LIFO and Close must close the SQLite handle before
	// TempDir's RemoveAll tries to delete it.
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	if _, err := service.CreateAccount("create", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	if _, err := service.ShareWorkspace("share", "beresta://identity?user=x&key=01"); err == nil {
		t.Fatal("ShareWorkspace before sync enabled succeeded, want an error")
	}
	if _, err := service.AcceptWorkspaceGrant("accept", "beresta://grant?workspace=x&key=01&authority=02&sig=03"); err == nil {
		t.Fatal("AcceptWorkspaceGrant before sync enabled succeeded, want an error")
	}
}

// TestServiceExportIdentityRoundTripsAccountIdentity confirms ExportIdentity
// emits exactly this account's own identity, with no server or sync
// requirement, and fails while locked.
func TestServiceExportIdentityRoundTripsAccountIdentity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	created, err := service.CreateAccount("create", dbPath, "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	accountID, _ := decodeJSON[map[string]any](t, created)["account_id"].(string)

	identityResponse, err := service.ExportIdentity("export")
	if err != nil {
		t.Fatalf("ExportIdentity: %v", err)
	}
	identityCode := decodeJSON[map[string]string](t, identityResponse)["identity_code"]
	userID, _, err := sharecode.DecodeIdentity(identityCode)
	if err != nil {
		t.Fatalf("decode ExportIdentity code: %v", err)
	}
	if userID.String() != accountID {
		t.Fatalf("ExportIdentity user id = %s, want %s", userID, accountID)
	}

	if err := service.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := service.ExportIdentity("export-locked"); !errors.Is(err, account.ErrAccountLocked) {
		t.Fatalf("ExportIdentity while locked error = %v, want %v", err, account.ErrAccountLocked)
	}
}
