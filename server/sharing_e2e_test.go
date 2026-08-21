package server_test

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/account"
	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
	"github.com/beresta-app/beresta/core/transport"
	"github.com/beresta-app/beresta/server"
)

// This file exercises the identity-and-sharing behavioral spec end to end
// against a real server, real TLS, and real client accounts (not opaque
// test fixtures): authorized sharing, cross-user isolation, revocation
// rejecting future operations, uninterrupted access for remaining devices,
// and inability to decrypt post-rotation content with an old key. See
// openspec/changes/build-e2ee-notes-app/specs/identity-and-sharing/spec.md.

// e2eWrapper is a minimal in-memory keystore.Wrapper for tests, matching
// core/account's own test helper but duplicated here since this file lives
// in the external server_test package and cannot reach unexported test
// helpers in core/account's test files.
type e2eWrapper struct{}

func (e2eWrapper) Protection() keystore.Protection { return keystore.ProtectionWindowsDPAPI }

func (e2eWrapper) Wrap(_ context.Context, metadata keystore.Metadata, secret *corecrypto.Secret) ([]byte, error) {
	var plaintext []byte
	if err := secret.Use(func(b []byte) error { plaintext = append([]byte(nil), b...); return nil }); err != nil {
		return nil, err
	}
	defer clear(plaintext)
	return keystore.SealEnvelope(keystore.ProtectionWindowsDPAPI, metadata, plaintext)
}

func (e2eWrapper) Unwrap(_ context.Context, metadata keystore.Metadata, encoded []byte) (*corecrypto.Secret, error) {
	plaintext, err := keystore.OpenEnvelope(encoded, keystore.ProtectionWindowsDPAPI, metadata)
	if err != nil {
		return nil, err
	}
	return corecrypto.TakeSecret(plaintext)
}

func (e2eWrapper) Delete(context.Context, keystore.Metadata) error { return nil }

var _ keystore.Wrapper = e2eWrapper{}

func e2eFastKDF() corecrypto.Argon2idCalibrationOptions {
	return corecrypto.Argon2idCalibrationOptions{MemoryLimitKiB: corecrypto.MinArgon2idMemoryKiB, Parallelism: 1}
}

// startE2EServer runs a real *server.Runtime behind a real TLS listener
// using the runtime's own generated certificate (so the client's
// certificate-pinning and the server's authentication challenge fingerprint
// - which must match the actual TLS leaf certificate - agree, exactly as in
// production; httptest's own auto-generated certificate would not match
// runtime.TLSIdentity.Fingerprint).
func startE2EServer(t *testing.T) (*server.Runtime, string) {
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

type e2eActor struct {
	Account     *account.Account
	Transport   *transport.HTTP
	WorkspaceID model.ID
}

// enrollE2EActor creates a brand-new local account and enrolls it against
// the running server through a single-use invite and the real HTTP
// registration/challenge-authentication path.
func enrollE2EActor(t *testing.T, runtime *server.Runtime, baseURL, name string) e2eActor {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	acct, err := account.Create(ctx, account.CreateOptions{
		DatabasePath: dbPath, Passphrase: []byte("correct horse battery staple " + name),
		Wrapper: e2eWrapper{}, KDFOptions: e2eFastKDF(),
	})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	t.Cleanup(func() { acct.Lock() })

	workspaces, err := acct.Workspaces()
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("%s: expected exactly one workspace, got %v (err=%v)", name, workspaces, err)
	}
	workspaceID := workspaces[0]

	invite, err := runtime.Storage.CreateInvite(ctx, name, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	registration, err := acct.ServerRegistrationData(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}

	tr, err := transport.NewHTTP(transport.HTTPConfig{
		BaseURL: baseURL, SecurityMode: transport.HTTPSecurityPinned, PinnedFingerprint: runtime.TLSIdentity.Fingerprint,
		DeviceID: acct.DeviceID, SignChallenge: acct.SignDeviceChallenge, RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Register(ctx, transport.RegistrationRequest{InviteCode: invite.Code, DeviceName: name + " device", Data: registration}); err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return e2eActor{Account: acct, Transport: tr, WorkspaceID: workspaceID}
}

// syncE2E runs one real pull-verify-apply-then-push cycle for workspaceID
// through the shared coresync.Worker, exactly as production clients do.
func syncE2E(t *testing.T, actor e2eActor, workspaceID model.ID) error {
	t.Helper()
	repo, err := store.NewSyncRepository(actor.Account.DB(), "http")
	if err != nil {
		t.Fatal(err)
	}
	processor, err := account.NewSyncProcessor(actor.Account, account.SyncProcessorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := coresync.NewWorker(workspaceID, repo, actor.Transport, processor, coresync.WorkerOptions{
		Prepare: func(ctx context.Context) error { return refreshWorkspaceMemberDevices(ctx, actor, workspaceID) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker.SyncOnce(context.Background())
}

// refreshWorkspaceMemberDevices lets this device verify operations signed
// by a fellow workspace member's device: it fetches every member device's
// signing public key (see server.Storage.ListWorkspaceMemberDevices) and
// caches it locally through Account.UpsertRemoteDevices, exactly as a
// production sync worker's Prepare hook would before every cycle.
func refreshWorkspaceMemberDevices(ctx context.Context, actor e2eActor, workspaceID model.ID) error {
	devices, err := actor.Transport.ListWorkspaceMemberDevices(ctx, workspaceID.String())
	if err != nil {
		return err
	}
	records := make([]account.RemoteDeviceRecord, 0, len(devices))
	for _, device := range devices {
		id, err := model.ParseIDString(device.ID)
		if err != nil {
			return err
		}
		records = append(records, account.RemoteDeviceRecord{ID: id, PublicKey: device.SigningPublic, Active: device.RevokedAt == nil})
	}
	return actor.Account.UpsertRemoteDevices(ctx, records)
}

func commitNoteE2E(t *testing.T, actor e2eActor, workspaceID model.ID, title, body string) model.ID {
	t.Helper()
	ctx := context.Background()
	note, err := actor.Account.CreateNote(ctx, workspaceID, model.Nil, title)
	if err != nil {
		t.Fatal(err)
	}
	doc := yjsadapter.New()
	defer doc.Close()
	if err := doc.Insert(noteBodyRootForTest, 0, body, nil); err != nil {
		t.Fatal(err)
	}
	update, err := doc.EncodeStateAsUpdate(yjsadapter.FormatV2)
	if err != nil {
		t.Fatal(err)
	}
	if err := actor.Account.CommitNoteBody(ctx, account.NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: note.ID, Update: update, UpdateFormat: yjsadapter.FormatV2,
	}); err != nil {
		t.Fatal(err)
	}
	return note.ID
}

// noteBodyRootForTest mirrors the unexported noteBodyRoot constant in
// core/account (the fixed Y.Text root name for a note body); it must match
// for NoteDocumentState to project the same text back out.
const noteBodyRootForTest = "body"

func readNoteTextE2E(t *testing.T, actor e2eActor, workspaceID, noteID model.ID) (string, error) {
	t.Helper()
	state, _, err := actor.Account.NoteDocumentState(context.Background(), workspaceID, noteID)
	if err != nil {
		return "", err
	}
	doc, err := yjsadapter.Restore(yjsadapter.FormatV2, state)
	if err != nil {
		return "", err
	}
	defer doc.Close()
	return doc.Text(noteBodyRootForTest)
}

func TestSharingRevocationAndKeyRotationEndToEnd(t *testing.T) {
	runtime, baseURL := startE2EServer(t)

	alice := enrollE2EActor(t, runtime, baseURL, "alice")
	bob := enrollE2EActor(t, runtime, baseURL, "bob")
	carol := enrollE2EActor(t, runtime, baseURL, "carol")

	// Alice writes a note in her workspace and pushes it to the server
	// before sharing anything.
	noteID := commitNoteE2E(t, alice, alice.WorkspaceID, "Shared note", "hello from alice")
	if err := syncE2E(t, alice, alice.WorkspaceID); err != nil {
		t.Fatalf("alice initial sync: %v", err)
	}

	// --- Cross-user / server opacity, before any sharing ---
	// Carol was never invited: the server rejects her attempt to even list
	// key envelopes or pull operations for Alice's workspace.
	if _, err := carol.Transport.GetKeyEnvelopes(context.Background(), alice.WorkspaceID.String()); err == nil {
		t.Fatal("expected an unrelated user to be forbidden from reading key envelopes")
	}
	if err := syncE2E(t, carol, alice.WorkspaceID); err == nil {
		t.Fatal("expected an unrelated user's sync of Alice's workspace to fail")
	}

	// --- 9.1: sharing ---
	invitation, err := alice.Account.ShareWorkspace(alice.WorkspaceID, bob.Account.ID, bob.Account.IdentityPublicKey)
	if err != nil {
		t.Fatalf("ShareWorkspace: %v", err)
	}
	if err := alice.Transport.AddMember(context.Background(), alice.WorkspaceID.String(), bob.Account.ID.String(), invitation.KeyID, invitation.Envelope); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// Bob independently discovers and accepts his own envelope, exactly as
	// a real client would after being told a workspace was shared with it.
	envelopes, err := bob.Transport.GetKeyEnvelopes(context.Background(), alice.WorkspaceID.String())
	if err != nil {
		t.Fatalf("bob GetKeyEnvelopes: %v", err)
	}
	if len(envelopes) != 1 {
		t.Fatalf("expected exactly one envelope for bob, got %d", len(envelopes))
	}
	bobKeyID := mustDecodeHex(t, envelopes[0].KeyID)
	if err := bob.Account.AcceptWorkspaceShare(context.Background(), alice.WorkspaceID, bobKeyID, envelopes[0].Envelope, alice.Account.AuthorityPublicKey, invitation.Signature); err != nil {
		t.Fatalf("bob AcceptWorkspaceShare: %v", err)
	}

	// Bob pulls Alice's already-pushed note and can read it.
	if err := syncE2E(t, bob, alice.WorkspaceID); err != nil {
		t.Fatalf("bob sync after accepting share: %v", err)
	}
	text, err := readNoteTextE2E(t, bob, alice.WorkspaceID, noteID)
	if err != nil {
		t.Fatalf("bob read shared note: %v", err)
	}
	if text != "hello from alice" {
		t.Fatalf("bob read unexpected note content: %q", text)
	}

	// Carol still cannot, even now that the workspace is shared with Bob.
	if err := syncE2E(t, carol, alice.WorkspaceID); err == nil {
		t.Fatal("expected an unrelated user to remain forbidden after the workspace was shared with someone else")
	}

	// --- 9.2 / 9.3: revocation and forward-secure key rotation ---
	if _, err := alice.Account.SignMemberRevocation(alice.WorkspaceID, bob.Account.ID); err != nil {
		t.Fatalf("SignMemberRevocation: %v", err)
	}
	if err := alice.Transport.RevokeMember(context.Background(), alice.WorkspaceID.String(), bob.Account.ID.String()); err != nil {
		t.Fatalf("RevokeMember: %v", err)
	}

	// The server immediately rejects the removed member's further access to
	// this workspace, even though Bob's device itself is not revoked.
	if err := syncE2E(t, bob, alice.WorkspaceID); err == nil {
		t.Fatal("expected the removed member's workspace access to be rejected immediately")
	}

	rotation, err := alice.Account.BeginWorkspaceKeyRotation(alice.WorkspaceID, map[model.ID][]byte{alice.Account.ID: alice.Account.IdentityPublicKey})
	if err != nil {
		t.Fatalf("BeginWorkspaceKeyRotation: %v", err)
	}
	var aliceEnvelope []byte
	for _, r := range rotation.Recipients {
		if r.UserID == alice.Account.ID {
			aliceEnvelope = r.Envelope
		}
	}
	if err := alice.Transport.RotateWorkspaceKey(context.Background(), alice.WorkspaceID.String(), rotation.KeyID, []transport.RotationEnvelope{{UserID: alice.Account.ID.String(), Envelope: aliceEnvelope}}); err != nil {
		t.Fatalf("RotateWorkspaceKey: %v", err)
	}
	if err := alice.Account.AcceptWorkspaceKeyRotation(context.Background(), alice.WorkspaceID, rotation.KeyID, aliceEnvelope, alice.Account.AuthorityPublicKey, rotation.Signature, []model.ID{alice.Account.ID}); err != nil {
		t.Fatalf("alice AcceptWorkspaceKeyRotation: %v", err)
	}

	// Alice - the remaining authorized device - keeps working uninterrupted:
	// she can still write and sync new content under the rotated key.
	secondNoteID := commitNoteE2E(t, alice, alice.WorkspaceID, "Post-rotation note", "rotated content")
	if err := syncE2E(t, alice, alice.WorkspaceID); err != nil {
		t.Fatalf("alice sync after rotation: %v", err)
	}
	if text, err := readNoteTextE2E(t, alice, alice.WorkspaceID, secondNoteID); err != nil || text != "rotated content" {
		t.Fatalf("alice should read her own post-rotation note: text=%q err=%v", text, err)
	}

	// Bob never received the rotated key (he was excluded as a revoked
	// member), so even if he still held his old device credentials for this
	// workspace, the server already rejects his membership entirely - his
	// stale copy of the old key cannot decrypt anything written afterward.
	if err := syncE2E(t, bob, alice.WorkspaceID); err == nil {
		t.Fatal("expected the revoked member's sync to keep failing after rotation")
	}
	if _, keyID, err := bob.Account.WorkspaceKey(alice.WorkspaceID); err != nil || bytesEqualForTest(keyID, rotation.KeyID) {
		t.Fatal("bob must not end up holding the rotated key")
	}

	// --- Device revocation boundary ---
	// Bob revokes his own (only) device; the server must reject every
	// subsequent authenticated request from it, including for his own
	// workspace that nothing above touched.
	if _, err := bob.Account.SignDeviceRevocation(bob.Account.DeviceID); err != nil {
		t.Fatalf("SignDeviceRevocation: %v", err)
	}
	if err := bob.Transport.RevokeDevice(context.Background(), bob.Account.DeviceID.String()); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if err := syncE2E(t, bob, bob.WorkspaceID); err == nil {
		t.Fatal("expected a revoked device's operations to be rejected even for its own workspace")
	} else if !transport.IsLikelyRevoked(err) && !errors.Is(err, transport.ErrPermanent) {
		t.Fatalf("expected an authentication-classified rejection, got: %v", err)
	}
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func bytesEqualForTest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
