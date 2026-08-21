package account

import (
	"context"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func newTestAccount(t *testing.T) *Account {
	t.Helper()
	acct, err := Create(context.Background(), CreateOptions{
		DatabasePath: tempDBPath(t), Passphrase: []byte("correct horse battery staple"),
		Wrapper: newFakeWrapper(), KDFOptions: fastKDF(),
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	t.Cleanup(func() { acct.Lock() })
	return acct
}

func onlyWorkspace(t *testing.T, a *Account) model.ID {
	t.Helper()
	workspaces, err := a.Workspaces()
	if err != nil || len(workspaces) != 1 {
		t.Fatalf("expected exactly one workspace, got %v (err=%v)", workspaces, err)
	}
	return workspaces[0]
}

func TestShareWorkspaceRoundTripsToRecipient(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	invitation, err := alice.ShareWorkspace(workspaceID, bob.ID, bob.IdentityPublicKey)
	if err != nil {
		t.Fatalf("ShareWorkspace: %v", err)
	}
	if invitation.WorkspaceID != workspaceID || invitation.RecipientID != bob.ID {
		t.Fatalf("unexpected invitation identifiers: %+v", invitation)
	}

	if err := bob.AcceptWorkspaceShare(context.Background(), invitation.WorkspaceID, invitation.KeyID, invitation.Envelope, alice.AuthorityPublicKey, invitation.Signature); err != nil {
		t.Fatalf("AcceptWorkspaceShare: %v", err)
	}

	aliceKey, aliceKeyID, err := alice.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	bobKey, bobKeyID, err := bob.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(aliceKeyID, bobKeyID) {
		t.Fatal("recipient's key ID does not match the sender's")
	}
	var aliceRaw, bobRaw []byte
	aliceKey.Use(func(b []byte) error { aliceRaw = append([]byte(nil), b...); return nil })
	bobKey.Use(func(b []byte) error { bobRaw = append([]byte(nil), b...); return nil })
	if !bytesEqual(aliceRaw, bobRaw) {
		t.Fatal("recipient derived a different workspace key than the sender's")
	}

	// Accepting the identical share again is idempotent.
	if err := bob.AcceptWorkspaceShare(context.Background(), invitation.WorkspaceID, invitation.KeyID, invitation.Envelope, alice.AuthorityPublicKey, invitation.Signature); err != nil {
		t.Fatalf("re-accepting the same share should be idempotent: %v", err)
	}
}

func TestAcceptWorkspaceShareRejectsUnrelatedRecipient(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	carol := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	invitation, err := alice.ShareWorkspace(workspaceID, bob.ID, bob.IdentityPublicKey)
	if err != nil {
		t.Fatal(err)
	}

	// Carol never had the envelope sealed to her identity key, so opening it
	// under her own account must fail authentication - not silently decrypt
	// to a wrong key. This is the "unrelated user" opacity requirement.
	if err := carol.AcceptWorkspaceShare(context.Background(), invitation.WorkspaceID, invitation.KeyID, invitation.Envelope, alice.AuthorityPublicKey, invitation.Signature); err == nil {
		t.Fatal("expected an unrelated recipient to fail to open the sealed envelope")
	}
	if _, err := carol.Workspaces(); err != nil {
		t.Fatal(err)
	}
	if workspaces, _ := carol.Workspaces(); len(workspaces) != 1 {
		t.Fatalf("unrelated recipient must not gain a new workspace: %v", workspaces)
	}
}

func TestAcceptWorkspaceShareRejectsTamperedSignature(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	invitation, err := alice.ShareWorkspace(workspaceID, bob.ID, bob.IdentityPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), invitation.Signature...)
	tampered[0] ^= 0xFF

	if err := bob.AcceptWorkspaceShare(context.Background(), invitation.WorkspaceID, invitation.KeyID, invitation.Envelope, alice.AuthorityPublicKey, tampered); err == nil {
		t.Fatal("expected a tampered membership signature to be rejected")
	}
	if workspaces, _ := bob.Workspaces(); len(workspaces) != 1 {
		t.Fatalf("a rejected share must not be applied: %v", workspaces)
	}
}

func TestAcceptWorkspaceShareRejectsWrongSigner(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	mallory := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	invitation, err := alice.ShareWorkspace(workspaceID, bob.ID, bob.IdentityPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	// Mallory's authority key did not sign this grant; verification against
	// it must fail even though the envelope itself decrypts fine.
	if err := bob.AcceptWorkspaceShare(context.Background(), invitation.WorkspaceID, invitation.KeyID, invitation.Envelope, mallory.AuthorityPublicKey, invitation.Signature); err == nil {
		t.Fatal("expected verification against the wrong signer to fail")
	}
}

func TestShareWorkspaceRejectsUnknownOrNonCurrentWorkspace(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	unknown, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.ShareWorkspace(unknown, bob.ID, bob.IdentityPublicKey); err != ErrUnknownWorkspace {
		t.Fatalf("expected ErrUnknownWorkspace, got %v", err)
	}
}

func TestSealedEnvelopeDoesNotExposeRawKeyBytes(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	invitation, err := alice.ShareWorkspace(workspaceID, bob.ID, bob.IdentityPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, _, err := alice.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	aliceKey.Use(func(b []byte) error { raw = append([]byte(nil), b...); return nil })

	// The server (and any observer of the wire envelope) sees only sealed
	// ciphertext, never the raw workspace key bytes.
	if len(invitation.Envelope) <= len(raw) {
		t.Fatalf("envelope is not larger than plaintext (missing seal overhead): envelope=%d raw=%d", len(invitation.Envelope), len(raw))
	}
	for i := 0; i+len(raw) <= len(invitation.Envelope); i++ {
		if bytesEqual(invitation.Envelope[i:i+len(raw)], raw) {
			t.Fatal("sealed envelope contains the raw workspace key as a byte substring")
		}
	}
}
