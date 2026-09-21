package keyrotation

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/account"
	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/transport"
)

// fakeWrapper is a deterministic in-memory keystore.Wrapper for tests,
// mirroring core/account's own test fixture (unexported there, so
// duplicated here across the package boundary).
type fakeWrapper struct{}

func (fakeWrapper) Protection() keystore.Protection { return keystore.ProtectionWindowsDPAPI }

func (fakeWrapper) Wrap(_ context.Context, metadata keystore.Metadata, secret *corecrypto.Secret) ([]byte, error) {
	var plaintext []byte
	if err := secret.Use(func(b []byte) error {
		plaintext = append([]byte(nil), b...)
		return nil
	}); err != nil {
		return nil, err
	}
	defer clear(plaintext)
	return keystore.SealEnvelope(keystore.ProtectionWindowsDPAPI, metadata, plaintext)
}

func (fakeWrapper) Unwrap(_ context.Context, metadata keystore.Metadata, encoded []byte) (*corecrypto.Secret, error) {
	plaintext, err := keystore.OpenEnvelope(encoded, keystore.ProtectionWindowsDPAPI, metadata)
	if err != nil {
		return nil, err
	}
	return corecrypto.TakeSecret(plaintext)
}

func (fakeWrapper) Delete(context.Context, keystore.Metadata) error { return nil }

var _ keystore.Wrapper = fakeWrapper{}

func fastKDF() corecrypto.Argon2idCalibrationOptions {
	return corecrypto.Argon2idCalibrationOptions{MemoryLimitKiB: corecrypto.MinArgon2idMemoryKiB, Parallelism: 1}
}

func newTestAccount(t *testing.T) *account.Account {
	t.Helper()
	dir := t.TempDir()
	acct, err := account.Create(context.Background(), account.CreateOptions{
		DatabasePath: filepath.Join(dir, "beresta.db"),
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      fakeWrapper{},
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { acct.Lock() })
	return acct
}

func onlyWorkspace(t *testing.T, a *account.Account) model.ID {
	t.Helper()
	workspaces, err := a.Workspaces()
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("expected exactly one workspace, got %v (err=%v)", workspaces, err)
	}
	return workspaces[0]
}

// fakeServerState is the shared in-memory backing behind every account's
// fakeTransport in a test, mirroring the server's memberships/key_envelopes/
// key_transitions tables closely enough to exercise Reconcile and
// TriggerAfterRevocation without a real HTTP server or database.
type fakeServerState struct {
	mu          sync.Mutex
	members     []transport.RemoteMember
	envelopes   []transport.RemoteKeyEnvelope
	transitions []transport.RemoteKeyTransition
	clock       time.Time
}

func newFakeServerState() *fakeServerState {
	return &fakeServerState{clock: time.Unix(1_700_000_000, 0)}
}

func (s *fakeServerState) tick() time.Time {
	s.clock = s.clock.Add(time.Second)
	return s.clock
}

func (s *fakeServerState) revoke(userID model.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.tick()
	for i := range s.members {
		if s.members[i].UserID == userID.String() {
			s.members[i].RevokedAt = &now
		}
	}
}

// fakeTransport implements Transport as if acting for one specific user's
// device, matching how a real *transport.HTTP is bound to one device's
// credentials.
type fakeTransport struct {
	state  *fakeServerState
	userID string
}

func (f *fakeTransport) ListMembers(context.Context, string) ([]transport.RemoteMember, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	return append([]transport.RemoteMember(nil), f.state.members...), nil
}

func (f *fakeTransport) GetKeyEnvelopes(context.Context, string) ([]transport.RemoteKeyEnvelope, []transport.RemoteKeyTransition, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	var envelopes []transport.RemoteKeyEnvelope
	for _, envelope := range f.state.envelopes {
		if envelope.UserID == f.userID {
			envelopes = append(envelopes, envelope)
		}
	}
	return envelopes, append([]transport.RemoteKeyTransition(nil), f.state.transitions...), nil
}

func (f *fakeTransport) RotateWorkspaceKey(_ context.Context, workspaceID string, keyID []byte, envelopes []transport.RotationEnvelope, signature []byte) error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	keyIDHex := hex.EncodeToString(keyID)
	recipientIDs := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		f.state.envelopes = append(f.state.envelopes, transport.RemoteKeyEnvelope{
			WorkspaceID: workspaceID, UserID: envelope.UserID, KeyID: keyIDHex, Envelope: envelope.Envelope, CreatedAt: f.state.tick(),
		})
		recipientIDs = append(recipientIDs, envelope.UserID)
	}
	sort.Strings(recipientIDs)
	f.state.transitions = append(f.state.transitions, transport.RemoteKeyTransition{
		WorkspaceID: workspaceID, KeyID: keyIDHex, Signature: signature, RecipientUserIDs: recipientIDs, CreatedAt: f.state.tick(),
	})
	return nil
}

var _ Transport = (*fakeTransport)(nil)

// setUpSharedWorkspace creates alice (owner), bob, and carol, shares
// alice's workspace with both, and seeds a fakeServerState with matching
// active memberships - the state every test in this file starts from.
func setUpSharedWorkspace(t *testing.T) (alice, bob, carol *account.Account, workspaceID model.ID, state *fakeServerState) {
	t.Helper()
	alice = newTestAccount(t)
	bob = newTestAccount(t)
	carol = newTestAccount(t)
	workspaceID = onlyWorkspace(t, alice)
	ctx := context.Background()

	for _, recipient := range []*account.Account{bob, carol} {
		invitation, err := alice.ShareWorkspace(workspaceID, recipient.ID, recipient.IdentityPublicKey)
		if err != nil {
			t.Fatalf("ShareWorkspace: %v", err)
		}
		if err := recipient.AcceptWorkspaceShare(ctx, workspaceID, invitation.KeyID, invitation.Envelope, alice.AuthorityPublicKey, invitation.Signature); err != nil {
			t.Fatalf("AcceptWorkspaceShare: %v", err)
		}
	}

	state = newFakeServerState()
	state.members = []transport.RemoteMember{
		{UserID: alice.ID.String(), Role: "owner", IdentityPublic: alice.IdentityPublicKey, AuthorityPublic: alice.AuthorityPublicKey},
		{UserID: bob.ID.String(), Role: "member", IdentityPublic: bob.IdentityPublicKey, AuthorityPublic: bob.AuthorityPublicKey},
		{UserID: carol.ID.String(), Role: "member", IdentityPublic: carol.IdentityPublicKey, AuthorityPublic: carol.AuthorityPublicKey},
	}
	return alice, bob, carol, workspaceID, state
}

func currentKeyIDHex(t *testing.T, acc *account.Account, workspaceID model.ID) string {
	t.Helper()
	_, keyID, err := acc.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(keyID)
}

func TestTriggerAfterRevocationRotatesAndCarolReconciles(t *testing.T) {
	alice, bob, carol, workspaceID, state := setUpSharedWorkspace(t)
	ctx := context.Background()

	preRotationKeyID := currentKeyIDHex(t, alice, workspaceID)

	// Simulate the server-side revocation that already happened before
	// TriggerAfterRevocation is called (see desktop/workspaces.go's
	// RevokeWorkspaceMember, which calls RevokeMember first).
	state.revoke(bob.ID)

	aliceTransport := &fakeTransport{state: state, userID: alice.ID.String()}
	if err := TriggerAfterRevocation(ctx, alice, aliceTransport, workspaceID); err != nil {
		t.Fatalf("TriggerAfterRevocation: %v", err)
	}

	if pending, err := aliceStorePending(t, alice, workspaceID); err != nil || pending {
		t.Fatalf("pending marker after successful rotation = %v, %v, want false, nil", pending, err)
	}
	newKeyID := currentKeyIDHex(t, alice, workspaceID)
	if newKeyID == preRotationKeyID {
		t.Fatal("alice's own key must advance immediately as part of TriggerAfterRevocation")
	}

	// Carol - a fellow active member who did not initiate the rotation -
	// picks it up reactively on her next sync-cycle Reconcile call.
	carolTransport := &fakeTransport{state: state, userID: carol.ID.String()}
	if err := Reconcile(ctx, carol, carolTransport, workspaceID); err != nil {
		t.Fatalf("carol Reconcile: %v", err)
	}
	if got := currentKeyIDHex(t, carol, workspaceID); got != newKeyID {
		t.Fatalf("carol's key after Reconcile = %s, want %s (alice's rotated key)", got, newKeyID)
	}

	// Reconcile must be idempotent: calling it again is a silent no-op.
	if err := Reconcile(ctx, carol, carolTransport, workspaceID); err != nil {
		t.Fatalf("second carol Reconcile: %v", err)
	}
	if got := currentKeyIDHex(t, carol, workspaceID); got != newKeyID {
		t.Fatalf("carol's key changed on a redundant Reconcile: got %s, want %s", got, newKeyID)
	}
}

func TestReconcileRejectsTamperedTransitionSignature(t *testing.T) {
	alice, bob, carol, workspaceID, state := setUpSharedWorkspace(t)
	ctx := context.Background()
	preRotationKeyID := currentKeyIDHex(t, carol, workspaceID)

	state.revoke(bob.ID)
	aliceTransport := &fakeTransport{state: state, userID: alice.ID.String()}
	if err := TriggerAfterRevocation(ctx, alice, aliceTransport, workspaceID); err != nil {
		t.Fatalf("TriggerAfterRevocation: %v", err)
	}

	// Tamper with the recorded transition signature, simulating either
	// server-side tampering or an unrelated bit flip in transit.
	state.mu.Lock()
	for i := range state.transitions {
		state.transitions[i].Signature[0] ^= 0xFF
	}
	state.mu.Unlock()

	carolTransport := &fakeTransport{state: state, userID: carol.ID.String()}
	if err := Reconcile(ctx, carol, carolTransport, workspaceID); err == nil {
		t.Fatal("expected Reconcile to reject a tampered key-transition signature")
	}
	if got := currentKeyIDHex(t, carol, workspaceID); got != preRotationKeyID {
		t.Fatal("carol's key must not change when the transition signature fails verification")
	}
}

func TestReconcileFailsClosedWhenTransitionRecordIsMissing(t *testing.T) {
	alice, bob, carol, workspaceID, state := setUpSharedWorkspace(t)
	ctx := context.Background()
	preRotationKeyID := currentKeyIDHex(t, carol, workspaceID)

	state.revoke(bob.ID)
	aliceTransport := &fakeTransport{state: state, userID: alice.ID.String()}
	if err := TriggerAfterRevocation(ctx, alice, aliceTransport, workspaceID); err != nil {
		t.Fatalf("TriggerAfterRevocation: %v", err)
	}

	// Simulate either a server that predates the key_transitions table, or
	// a hostile one selectively withholding this one rotation's record: the
	// client cannot tell the two apart, so it must fail closed in both
	// rather than silently skip verification (which would let a hostile
	// server bypass it entirely by simply never writing the record).
	state.mu.Lock()
	state.transitions = nil
	state.mu.Unlock()

	carolTransport := &fakeTransport{state: state, userID: carol.ID.String()}
	if err := Reconcile(ctx, carol, carolTransport, workspaceID); err == nil {
		t.Fatal("expected Reconcile to fail closed when no key-transition record is found")
	}
	if got := currentKeyIDHex(t, carol, workspaceID); got != preRotationKeyID {
		t.Fatal("carol's key must not change when no key-transition record is found")
	}
}

func aliceStorePending(t *testing.T, acc *account.Account, workspaceID model.ID) (bool, error) {
	t.Helper()
	return store.KeyRotationPending(context.Background(), acc.DB(), workspaceID)
}
