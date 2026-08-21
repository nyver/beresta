package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/gorilla/websocket"
)

type testActor struct {
	Principal   Principal
	Session     Session
	PrivateKey  ed25519.PrivateKey
	UserID      string
	DeviceID    string
	WorkspaceID string
	KeyID       string
}

func newTestRuntime(t *testing.T) *Runtime {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Server.DataDirectory = t.TempDir()
	cfg.Backups.Enabled = false
	cfg.Limits.RequestsPerSecond = 10000
	cfg.Limits.RequestBurst = 10000
	return newTestRuntimeWithConfig(t, cfg)
}

func newTestRuntimeWithConfig(t *testing.T, cfg Config) *Runtime {
	t.Helper()
	runtime, err := Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

func registerTestActor(t *testing.T, runtime *Runtime, name string) testActor {
	t.Helper()
	userID := mustNewID(t)
	deviceID := mustNewID(t)
	workspaceID := mustNewID(t)
	keyID := strings.Repeat("1", 32)
	identityPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authorityPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	invite, err := runtime.Storage.CreateInvite(context.Background(), name, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Storage.Register(context.Background(), Registration{
		InviteCode: invite.Code, UserID: userID, IdentityPublic: identityPublic[:32], AuthorityPublic: authorityPublic[:32],
		DeviceID: deviceID, DeviceName: name + " device", SigningPublic: public,
		WorkspaceID: workspaceID, WorkspaceKeyID: keyID, WorkspaceEnvelope: []byte("opaque-envelope"),
		KeybagCiphertext: []byte("opaque-keybag-" + name),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := runtime.Storage.IssueChallenge(context.Background(), deviceID, "sync", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	proof := ChallengeProof{ChallengeID: challenge.ID, DeviceID: deviceID, ServerFingerprint: challenge.ServerFingerprint, Nonce: challenge.Nonce, Scope: challenge.Scope}
	proof.Signature = ed25519.Sign(private, authSignatureInput(proof))
	session, err := runtime.Storage.VerifyChallenge(context.Background(), proof, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	principal, err := runtime.Storage.Authenticate(context.Background(), session.Token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return testActor{Principal: principal, Session: session, PrivateKey: private, UserID: userID, DeviceID: deviceID, WorkspaceID: workspaceID, KeyID: keyID}
}

func TestInviteChallengeReplayAndRevocationBoundaries(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")
	invite, err := runtime.Storage.CreateInvite(context.Background(), "Reuse", time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := Registration{
		InviteCode: invite.Code, UserID: mustNewID(t), IdentityPublic: bytes.Repeat([]byte{1}, 32), AuthorityPublic: bytes.Repeat([]byte{2}, 32),
		DeviceID: mustNewID(t), DeviceName: "device", SigningPublic: bytes.Repeat([]byte{3}, 32), WorkspaceID: mustNewID(t),
		WorkspaceKeyID: strings.Repeat("2", 32), WorkspaceEnvelope: []byte("envelope"), KeybagCiphertext: []byte("keybag"),
	}
	if _, err := runtime.Storage.Register(context.Background(), request, time.Now()); err != nil {
		t.Fatal(err)
	}
	request.UserID, request.DeviceID, request.WorkspaceID = mustNewID(t), mustNewID(t), mustNewID(t)
	if _, err := runtime.Storage.Register(context.Background(), request, time.Now()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invite reuse error = %v", err)
	}
	expired, err := runtime.Storage.CreateInvite(context.Background(), "Expired", time.Second, time.Now().Add(-2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	request.InviteCode = expired.Code
	if _, err := runtime.Storage.Register(context.Background(), request, time.Now()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired invite error = %v", err)
	}

	challenge, err := runtime.Storage.IssueChallenge(context.Background(), actor.DeviceID, "sync", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	proof := ChallengeProof{ChallengeID: challenge.ID, DeviceID: actor.DeviceID, ServerFingerprint: challenge.ServerFingerprint, Nonce: challenge.Nonce, Scope: "sync"}
	proof.Signature = ed25519.Sign(actor.PrivateKey, authSignatureInput(proof))
	wrongServer := proof
	wrongServer.ServerFingerprint = strings.Repeat("0", len(proof.ServerFingerprint))
	wrongServer.Signature = ed25519.Sign(actor.PrivateKey, authSignatureInput(wrongServer))
	if _, err := runtime.Storage.VerifyChallenge(context.Background(), wrongServer, time.Now()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("cross-server challenge error = %v", err)
	}
	if _, err := runtime.Storage.VerifyChallenge(context.Background(), proof, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Storage.VerifyChallenge(context.Background(), proof, time.Now()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("challenge replay error = %v", err)
	}
	refreshChallenge, err := runtime.Storage.IssueChallenge(context.Background(), actor.DeviceID, "sync", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	refreshProof := ChallengeProof{ChallengeID: refreshChallenge.ID, DeviceID: actor.DeviceID,
		ServerFingerprint: refreshChallenge.ServerFingerprint, Nonce: refreshChallenge.Nonce, Scope: refreshChallenge.Scope}
	refreshProof.Signature = ed25519.Sign(actor.PrivateKey, authSignatureInput(refreshProof))
	refreshResponse := performAPIRequest(t, runtime.API, actor.Session.Token, http.MethodPost, "/v1/auth/refresh", refreshProof)
	if refreshResponse.Code != http.StatusCreated {
		t.Fatalf("refresh status=%d body=%s", refreshResponse.Code, refreshResponse.Body.String())
	}
	var refreshed Session
	if err := json.Unmarshal(refreshResponse.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Storage.Authenticate(context.Background(), actor.Session.Token, time.Now()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("replaced session error = %v", err)
	}
	actor.Session = refreshed
	if _, err := runtime.Storage.Authenticate(context.Background(), actor.Session.Token, actor.Session.ExpiresAt.Add(time.Second)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session error = %v", err)
	}
	if err := runtime.Storage.AdminRevokeDevice(context.Background(), actor.DeviceID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Storage.Authenticate(context.Background(), actor.Session.Token, time.Now()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked session error = %v", err)
	}
	revokedOperation := signedTestOperation(t, actor, time.Now())
	if _, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{revokedOperation}, time.Now()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked operation error = %v", err)
	}
}

func TestOperationBlobAndSnapshotLifecycle(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")
	operation := signedTestOperation(t, actor, time.Now())
	accepted, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{operation}, time.Now())
	if err != nil || len(accepted) != 1 || accepted[0].Sequence != 1 {
		t.Fatalf("PushOperations = %v, %v", accepted, err)
	}
	duplicate, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{operation}, time.Now())
	if err != nil || !duplicate[0].Duplicate || duplicate[0].Sequence != 1 {
		t.Fatalf("duplicate PushOperations = %v, %v", duplicate, err)
	}
	changes, err := runtime.Storage.PullChanges(context.Background(), actor.Principal, actor.WorkspaceID, 0, 100)
	if err != nil || changes.Cursor != 1 || len(changes.Operations) != 1 {
		t.Fatalf("PullChanges = %+v, %v", changes, err)
	}

	chunk := []byte("opaque encrypted chunk")
	chunkHash := sha256.Sum256(chunk)
	blobDigest := sha256.Sum256([]byte("private blob identifier"))
	blobID := hex.EncodeToString(blobDigest[:])
	info, err := runtime.Storage.BeginBlob(context.Background(), actor.UserID, BlobInit{
		WorkspaceID: actor.WorkspaceID, BlobID: blobID, KeyID: actor.KeyID, EncryptedManifest: []byte("opaque manifest"),
		TotalBytes: int64(len(chunk)), Chunks: []BlobChunkSpec{{Index: 0, Bytes: int64(len(chunk)), SHA256: hex.EncodeToString(chunkHash[:])}},
	}, time.Now())
	if err != nil || info.State != "staging" {
		t.Fatalf("BeginBlob = %+v, %v", info, err)
	}
	if err := runtime.Storage.PutBlobChunk(context.Background(), actor.UserID, actor.WorkspaceID, blobID, 0, chunk, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Storage.PutBlobChunk(context.Background(), actor.UserID, actor.WorkspaceID, blobID, 0, chunk, time.Now()); err != nil {
		t.Fatalf("idempotent chunk upload: %v", err)
	}
	finalBlob := runtime.Storage.finalBlobPath(actor.WorkspaceID, blobID)
	if err := os.MkdirAll(filepath.Dir(finalBlob), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(runtime.Storage.stagingBlobPath(actor.WorkspaceID, blobID), finalBlob); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Storage.CompleteBlob(context.Background(), actor.UserID, actor.WorkspaceID, blobID, time.Now()); err != nil {
		t.Fatal(err)
	}
	read, err := runtime.Storage.ReadBlobChunk(context.Background(), actor.UserID, actor.WorkspaceID, blobID, 0)
	if err != nil || !bytes.Equal(read, chunk) {
		t.Fatalf("ReadBlobChunk = %q, %v", read, err)
	}
	referenceID := mustNewID(t)
	for attempt := 0; attempt < 2; attempt++ {
		if err := runtime.Storage.SetBlobReferenced(context.Background(), actor.UserID, actor.WorkspaceID, blobID, referenceID, true, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	info, err = runtime.Storage.GetBlob(context.Background(), actor.UserID, actor.WorkspaceID, blobID)
	if err != nil || info.ReferenceCount != 1 {
		t.Fatalf("idempotent blob reference count = %d, error=%v", info.ReferenceCount, err)
	}
	if candidates, err := runtime.Storage.GarbageCollectBlobs(context.Background(), time.Now().Add(31*24*time.Hour), true); err != nil || len(candidates) != 0 {
		t.Fatalf("referenced blob GC candidates=%v error=%v", candidates, err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := runtime.Storage.SetBlobReferenced(context.Background(), actor.UserID, actor.WorkspaceID, blobID, referenceID, false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if candidates, err := runtime.Storage.GarbageCollectBlobs(context.Background(), time.Now().Add(31*24*time.Hour), true); err != nil || len(candidates) != 1 {
		t.Fatalf("unreferenced blob GC candidates=%v error=%v", candidates, err)
	}

	snapshot := signedTestSnapshot(t, actor, 1, time.Now())
	if err := runtime.Storage.PutSnapshot(context.Background(), actor.Principal, snapshot, time.Now()); err != nil {
		t.Fatal(err)
	}
	ack := SnapshotAck{Protocol: "beresta.sync.v1", SchemaVersion: 1, SnapshotID: snapshot.ID, WorkspaceID: actor.WorkspaceID, DeviceID: actor.DeviceID,
		BaseSequence: snapshot.BaseSequence, CiphertextHash: snapshot.CiphertextHash}
	ack.Signature = ed25519.Sign(actor.PrivateKey, snapshotAckSignatureInput(ack))
	eligible, err := runtime.Storage.AcknowledgeSnapshot(context.Background(), actor.Principal, ack, time.Now())
	if err != nil || !eligible {
		t.Fatalf("AcknowledgeSnapshot = %v, %v", eligible, err)
	}
}

func TestOperationValidationAndSnapshotCompactionBoundaries(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")

	invalidSignature := signedTestOperation(t, actor, time.Now())
	invalidSignature.Signature[0] ^= 0xff
	if _, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{invalidSignature}, time.Now()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("invalid operation signature error = %v", err)
	}
	future := signedTestOperation(t, actor, time.Now().Add(runtime.Config.Limits.MaxHLCFutureSkew.Value()+time.Second))
	if _, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{future}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("future operation HLC error = %v", err)
	}
	stale := signedTestOperation(t, actor, time.Now().Add(-runtime.Config.Limits.MaxHLCPastAge.Value()-time.Second))
	if _, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{stale}, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("stale operation HLC error = %v", err)
	}
	operation := signedTestOperation(t, actor, time.Now())
	if _, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{operation}, time.Now()); err != nil {
		t.Fatal(err)
	}
	conflict := operation
	conflict.Ciphertext = []byte("different opaque operation")
	conflict.Signature = ed25519.Sign(actor.PrivateKey, operationSignatureInput(conflict))
	if _, err := runtime.Storage.PushOperations(context.Background(), actor.Principal, []Operation{conflict}, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("operation identifier conflict error = %v", err)
	}

	secondPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secondDeviceID := mustNewID(t)
	if err := runtime.Storage.AddDevice(context.Background(), actor.Principal, secondDeviceID, "Second device", secondPublic, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot := signedTestSnapshot(t, actor, 1, time.Now())
	if err := runtime.Storage.PutSnapshot(context.Background(), actor.Principal, snapshot, time.Now()); err != nil {
		t.Fatal(err)
	}
	ack := SnapshotAck{Protocol: "beresta.sync.v1", SchemaVersion: 1, SnapshotID: snapshot.ID, WorkspaceID: actor.WorkspaceID, DeviceID: actor.DeviceID,
		BaseSequence: snapshot.BaseSequence, CiphertextHash: snapshot.CiphertextHash}
	ack.Signature = ed25519.Sign(actor.PrivateKey, snapshotAckSignatureInput(ack))
	eligible, err := runtime.Storage.AcknowledgeSnapshot(context.Background(), actor.Principal, ack, time.Now())
	if err != nil || eligible {
		t.Fatalf("snapshot with unacknowledged active device eligible=%v error=%v", eligible, err)
	}
	if err := runtime.Storage.RevokeDevice(context.Background(), actor.Principal, secondDeviceID, time.Now()); err != nil {
		t.Fatal(err)
	}
	eligible, err = runtime.Storage.AcknowledgeSnapshot(context.Background(), actor.Principal, ack, time.Now())
	if err != nil || eligible {
		t.Fatalf("snapshot before revocation retention eligible=%v error=%v", eligible, err)
	}
	afterRetention := time.Now().Add(31 * 24 * time.Hour)
	eligible, err = runtime.Storage.AcknowledgeSnapshot(context.Background(), actor.Principal, ack, afterRetention)
	if err != nil || !eligible {
		t.Fatalf("snapshot after revocation retention eligible=%v error=%v", eligible, err)
	}
	compacted, err := runtime.Storage.CompactWorkspace(context.Background(), actor.WorkspaceID, afterRetention, false)
	if err != nil || compacted.RemovedOperations != 1 {
		t.Fatalf("CompactWorkspace = %+v, %v", compacted, err)
	}
	if _, err := runtime.Storage.PullChanges(context.Background(), actor.Principal, actor.WorkspaceID, 0, 100); !errors.Is(err, ErrSnapshotRequired) {
		t.Fatalf("pull before compacted boundary error = %v", err)
	}
}

func TestMembershipKeyRotationAndRevocationBoundary(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := registerTestActor(t, runtime, "Owner")
	recipient := registerTestActor(t, runtime, "Recipient")
	if err := runtime.Storage.AddMember(context.Background(), owner.Principal, owner.WorkspaceID, recipient.UserID,
		owner.KeyID, []byte("recipient initial envelope"), time.Now()); err != nil {
		t.Fatal(err)
	}
	newKeyID := strings.Repeat("2", 32)
	if err := runtime.Storage.RotateWorkspaceKey(context.Background(), owner.Principal, owner.WorkspaceID, newKeyID,
		[]KeyEnvelopeInput{{UserID: owner.UserID, Envelope: []byte("owner rotated envelope")}}, time.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("incomplete key rotation error = %v", err)
	}
	if err := runtime.Storage.RotateWorkspaceKey(context.Background(), owner.Principal, owner.WorkspaceID, newKeyID,
		[]KeyEnvelopeInput{
			{UserID: owner.UserID, Envelope: []byte("owner rotated envelope")},
			{UserID: recipient.UserID, Envelope: []byte("recipient rotated envelope")},
		}, time.Now()); err != nil {
		t.Fatal(err)
	}
	envelopes, err := runtime.Storage.GetKeyEnvelopes(context.Background(), recipient.Principal, owner.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, envelope := range envelopes {
		if envelope.KeyID == newKeyID && bytes.Equal(envelope.Envelope, []byte("recipient rotated envelope")) {
			found = true
		}
	}
	if !found {
		t.Fatalf("recipient did not receive rotated key envelope: %+v", envelopes)
	}
	if err := runtime.Storage.RevokeMember(context.Background(), owner.Principal, owner.WorkspaceID, recipient.UserID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Storage.PullChanges(context.Background(), recipient.Principal, owner.WorkspaceID, 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked member access error = %v", err)
	}
}

func TestKeybagCompareAndSwapReturnsCurrentOpaqueVersion(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")
	updated, err := runtime.Storage.PutKeybag(context.Background(), actor.Principal, 1, []byte("new opaque keybag"), time.Now())
	if err != nil || updated.Version != 2 {
		t.Fatalf("keybag update=%+v error=%v", updated, err)
	}
	current, err := runtime.Storage.PutKeybag(context.Background(), actor.Principal, 1, []byte("stale overwrite"), time.Now())
	if !errors.Is(err, ErrConflict) || current.Version != 2 || !bytes.Equal(current.Ciphertext, updated.Ciphertext) {
		t.Fatalf("stale keybag result=%+v error=%v", current, err)
	}
	response := performAPIRequest(t, runtime.API, actor.Session.Token, http.MethodPut, "/v1/keybag",
		map[string]any{"expected_version": 1, "ciphertext": []byte("another stale overwrite")})
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte(`"current"`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"version":2`)) {
		t.Fatalf("keybag conflict response status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGeneratedIDORMatrixDeniesEveryForeignResourceAction(t *testing.T) {
	runtime := newTestRuntime(t)
	owner := registerTestActor(t, runtime, "Owner")
	attacker := registerTestActor(t, runtime, "Attacker")
	operation := signedTestOperation(t, owner, time.Now())
	if _, err := runtime.Storage.PushOperations(context.Background(), owner.Principal, []Operation{operation}, time.Now()); err != nil {
		t.Fatal(err)
	}
	snapshot := signedTestSnapshot(t, owner, 1, time.Now())
	if err := runtime.Storage.PutSnapshot(context.Background(), owner.Principal, snapshot, time.Now()); err != nil {
		t.Fatal(err)
	}
	foreignOperation := signedTestOperation(t, attacker, time.Now())
	foreignOperation.WorkspaceID = owner.WorkspaceID
	foreignOperation.Signature = ed25519.Sign(attacker.PrivateKey, operationSignatureInput(foreignOperation))

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		raw    []byte
	}{
		{name: "foreign device revoke", method: http.MethodDelete, path: "/v1/devices/" + owner.DeviceID},
		{name: "foreign members list", method: http.MethodGet, path: "/v1/workspaces/" + owner.WorkspaceID + "/members"},
		{name: "foreign member add", method: http.MethodPost, path: "/v1/workspaces/" + owner.WorkspaceID + "/members", body: map[string]any{"user_id": attacker.UserID, "key_id": owner.KeyID, "envelope": []byte("opaque")}},
		{name: "foreign member revoke", method: http.MethodDelete, path: "/v1/workspaces/" + owner.WorkspaceID + "/members/" + attacker.UserID},
		{name: "foreign envelopes", method: http.MethodGet, path: "/v1/workspaces/" + owner.WorkspaceID + "/key-envelopes"},
		{name: "foreign key rotation", method: http.MethodPut, path: "/v1/workspaces/" + owner.WorkspaceID + "/key-envelopes", body: map[string]any{"key_id": strings.Repeat("2", 32), "envelopes": []KeyEnvelopeInput{{UserID: owner.UserID, Envelope: []byte("opaque")}}}},
		{name: "foreign operation push", method: http.MethodPost, path: "/v1/sync/ops", body: map[string]any{"operations": []Operation{foreignOperation}}},
		{name: "foreign operation pull", method: http.MethodGet, path: "/v1/sync/changes?workspace_id=" + owner.WorkspaceID + "&cursor=0&limit=10"},
		{name: "foreign blob init", method: http.MethodPost, path: "/v1/blobs/init", body: BlobInit{WorkspaceID: owner.WorkspaceID, BlobID: strings.Repeat("a", 64), KeyID: owner.KeyID, EncryptedManifest: []byte("x"), TotalBytes: 1, Chunks: []BlobChunkSpec{{Index: 0, Bytes: 1, SHA256: hex.EncodeToString(sha256.New().Sum(nil))}}}},
		{name: "foreign blob chunk upload", method: http.MethodPut, path: "/v1/blobs/" + strings.Repeat("a", 64) + "/chunks/0?workspace_id=" + owner.WorkspaceID, raw: []byte("x")},
		{name: "foreign blob read", method: http.MethodGet, path: "/v1/blobs/" + strings.Repeat("a", 64) + "?workspace_id=" + owner.WorkspaceID},
		{name: "foreign blob chunk read", method: http.MethodGet, path: "/v1/blobs/" + strings.Repeat("a", 64) + "/chunks/0?workspace_id=" + owner.WorkspaceID},
		{name: "foreign blob complete", method: http.MethodPost, path: "/v1/blobs/" + strings.Repeat("a", 64) + "/complete?workspace_id=" + owner.WorkspaceID},
		{name: "foreign blob reference add", method: http.MethodPut, path: "/v1/blobs/" + strings.Repeat("a", 64) + "/references/" + mustNewID(t) + "?workspace_id=" + owner.WorkspaceID},
		{name: "foreign blob reference remove", method: http.MethodDelete, path: "/v1/blobs/" + strings.Repeat("a", 64) + "/references/" + mustNewID(t) + "?workspace_id=" + owner.WorkspaceID},
		{name: "foreign snapshot upload", method: http.MethodPost, path: "/v1/snapshots", body: snapshot},
		{name: "foreign snapshots list", method: http.MethodGet, path: "/v1/snapshots?workspace_id=" + owner.WorkspaceID},
		{name: "foreign snapshot latest", method: http.MethodGet, path: "/v1/snapshots/latest?workspace_id=" + owner.WorkspaceID},
		{name: "foreign snapshot read", method: http.MethodGet, path: "/v1/snapshots/" + snapshot.ID},
		{name: "foreign snapshot ack", method: http.MethodPost, path: "/v1/snapshots/" + snapshot.ID + "/ack", body: SnapshotAck{Protocol: "beresta.sync.v1", SchemaVersion: 1, SnapshotID: snapshot.ID, WorkspaceID: owner.WorkspaceID, DeviceID: attacker.DeviceID, BaseSequence: 1, CiphertextHash: snapshot.CiphertextHash, Signature: make([]byte, 64)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performAPIRequestWithRaw(t, runtime.API, attacker.Session.Token, test.method, test.path, test.body, test.raw)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}

	response := performAPIRequest(t, runtime.API, attacker.Session.Token, http.MethodGet, "/v1/keybag?user_id="+owner.UserID, nil)
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("opaque-keybag-Owner")) {
		t.Fatalf("keybag ownership bypass: status=%d body=%s", response.Code, response.Body.String())
	}
	response = performAPIRequest(t, runtime.API, attacker.Session.Token, http.MethodPut, "/v1/keybag?user_id="+owner.UserID,
		map[string]any{"expected_version": 1, "ciphertext": []byte("attacker replacement")})
	if response.Code != http.StatusOK {
		t.Fatalf("self-scoped keybag update status=%d body=%s", response.Code, response.Body.String())
	}
	ownerKeybag, err := runtime.Storage.GetKeybag(context.Background(), owner.Principal)
	if err != nil || !bytes.Equal(ownerKeybag.Ciphertext, []byte("opaque-keybag-Owner")) {
		t.Fatalf("foreign keybag mutated: %+v error=%v", ownerKeybag, err)
	}
	response = performAPIRequest(t, runtime.API, attacker.Session.Token, http.MethodGet, "/v1/devices?user_id="+owner.UserID, nil)
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(owner.DeviceID)) {
		t.Fatalf("device list ownership bypass: status=%d body=%s", response.Code, response.Body.String())
	}
	response = performAPIRequest(t, runtime.API, attacker.Session.Token, http.MethodGet, "/v1/workspaces?user_id="+owner.UserID, nil)
	if response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(owner.WorkspaceID)) {
		t.Fatalf("workspace list ownership bypass: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAuthenticationBodyQuotaAndRateLimits(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.DataDirectory = t.TempDir()
	cfg.Backups.Enabled = false
	cfg.Limits.UserQuotaBytes = 4
	cfg.Limits.MaxBlobBytes = 1024
	cfg.Limits.RequestsPerSecond = 1
	cfg.Limits.RequestBurst = 2
	runtime := newTestRuntimeWithConfig(t, cfg)
	actor := registerTestActor(t, runtime, "Limited")

	unauthorized := performAPIRequest(t, runtime.API, "", http.MethodGet, "/v1/devices", nil)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthorized.Code)
	}
	digest := sha256.Sum256([]byte("five bytes"))
	_, err := runtime.Storage.BeginBlob(context.Background(), actor.UserID, BlobInit{
		WorkspaceID: actor.WorkspaceID, BlobID: hex.EncodeToString(digest[:]), KeyID: actor.KeyID,
		EncryptedManifest: []byte("m"), TotalBytes: 5,
		Chunks: []BlobChunkSpec{{Index: 0, Bytes: 5, SHA256: hex.EncodeToString(digest[:])}},
	}, time.Now())
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("quota error = %v", err)
	}

	var limited bool
	for attempt := 0; attempt < 4; attempt++ {
		response := performAPIRequest(t, runtime.API, actor.Session.Token, http.MethodGet, "/v1/devices", nil)
		if response.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("request-rate limiter did not reject the abuse burst")
	}
}

func TestStructuredLogsAndMetricsExcludeProtectedIdentifiersAndContent(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	secret := "seeded-secret-never-log"
	response := performAPIRequest(t, runtime.API, actor.Session.Token, http.MethodPut, "/v1/keybag",
		map[string]any{"expected_version": 1, "ciphertext": []byte(secret)})
	if response.Code != http.StatusOK {
		t.Fatalf("keybag update status=%d body=%s", response.Code, response.Body.String())
	}
	for _, protected := range []string{secret, actor.Session.Token, actor.UserID, actor.DeviceID, actor.WorkspaceID} {
		if strings.Contains(logs.String(), protected) {
			t.Fatalf("structured request log contains protected value %q: %s", protected, logs.String())
		}
	}
	metrics := httptest.NewRecorder()
	runtime.API.metricsHandler(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, protected := range []string{actor.UserID, actor.DeviceID, actor.WorkspaceID} {
		if strings.Contains(metrics.Body.String(), protected) {
			t.Fatalf("metrics contain protected identifier %q: %s", protected, metrics.Body.String())
		}
	}
}

func TestOversizedRequestBodyIsRejectedBeforeDispatch(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")
	oversized := bytes.Repeat([]byte{'x'}, int(runtime.Config.Limits.MaxOperationBytes*6)+1)
	response := performAPIRequestWithRaw(t, runtime.API, actor.Session.Token, http.MethodPut, "/v1/keybag", nil, oversized)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized request status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAuthorizedWebSocketHintsAndDisconnectCleanup(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Alice")
	attacker := registerTestActor(t, runtime, "Mallory")
	server := httptest.NewTLSServer(runtime.API)
	defer server.Close()
	websocketURL := "wss" + strings.TrimPrefix(server.URL, "https") + "/v1/sync/stream?workspace_id=" + actor.WorkspaceID
	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // #nosec G402 -- httptest uses an ephemeral self-signed certificate.
	foreignHeaders := http.Header{"Authorization": []string{"Bearer " + attacker.Session.Token}}
	foreignConnection, foreignResponse, err := dialer.Dial(websocketURL, foreignHeaders)
	if foreignConnection != nil {
		foreignConnection.Close()
	}
	if err == nil || foreignResponse == nil || foreignResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign websocket response=%v error=%v", foreignResponse, err)
	}
	headers := http.Header{"Authorization": []string{"Bearer " + actor.Session.Token}}
	connection, response, err := dialer.Dial(websocketURL, headers)
	if err != nil {
		t.Fatalf("websocket dial response=%v error=%v", response, err)
	}
	runtime.API.pubsub.Publish(CursorHint{Protocol: "beresta.sync.v1", WorkspaceID: actor.WorkspaceID, LatestSeq: 7, CursorEpoch: 1})
	connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	var hint CursorHint
	if err := connection.ReadJSON(&hint); err != nil || hint.LatestSeq != 7 {
		t.Fatalf("websocket hint = %+v, %v", hint, err)
	}
	connection.Close()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.API.pubsub.subscriberCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count := runtime.API.pubsub.subscriberCount(); count != 0 {
		t.Fatalf("websocket subscriptions after disconnect = %d", count)
	}
}

func mustNewID(t *testing.T) string {
	t.Helper()
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func signedTestOperation(t *testing.T, actor testActor, now time.Time) Operation {
	t.Helper()
	operation := Operation{Protocol: "beresta.sync.v1", SchemaVersion: 1, OpID: mustNewID(t), WorkspaceID: actor.WorkspaceID,
		DeviceID: actor.DeviceID, HLCPhysicalMS: now.UnixMilli(), KeyID: actor.KeyID, Nonce: make([]byte, 24), Ciphertext: []byte("opaque operation")}
	operation.Signature = ed25519.Sign(actor.PrivateKey, operationSignatureInput(operation))
	return operation
}

func signedTestSnapshot(t *testing.T, actor testActor, base int64, now time.Time) Snapshot {
	t.Helper()
	ciphertext := []byte("opaque encrypted snapshot")
	digest := sha256.Sum256(ciphertext)
	snapshot := Snapshot{Protocol: "beresta.sync.v1", SchemaVersion: 1, ID: mustNewID(t), WorkspaceID: actor.WorkspaceID, BaseSequence: base, CursorEpoch: 1, KeyID: actor.KeyID,
		CreatorDeviceID: actor.DeviceID, HLCPhysicalMS: now.UnixMilli(), Nonce: make([]byte, 24), CiphertextHash: digest[:], Ciphertext: ciphertext}
	snapshot.Signature = ed25519.Sign(actor.PrivateKey, snapshotSignatureInput(snapshot))
	return snapshot
}

func performAPIRequest(t *testing.T, api http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	return performAPIRequestWithRaw(t, api, token, method, path, body, nil)
}

func performAPIRequestWithRaw(t *testing.T, api http.Handler, token, method, path string, body any, raw []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if raw != nil {
		reader = bytes.NewReader(raw)
	} else if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = "127.0.0.1:12345"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

// operationSignatureInput reproduces the domain-separated signing input a
// client feeds to Ed25519, so tests can forge and tamper with envelopes the
// way a real device would build them.
func operationSignatureInput(operation Operation) []byte {
	payload, err := operationSignaturePayload(operation)
	if err != nil {
		return nil
	}
	domain := []byte(corecrypto.SignatureDomainOperation)
	result := binary.BigEndian.AppendUint32(nil, uint32(len(domain)))
	result = append(result, domain...)
	return append(result, payload...)
}
