package account

import (
	"context"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func TestWorkspaceKeyRotationRotatesForwardAndRetainsHistoricalReads(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	// Bob joins before rotation.
	invitation, err := alice.ShareWorkspace(workspaceID, bob.ID, bob.IdentityPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.AcceptWorkspaceShare(context.Background(), invitation.WorkspaceID, invitation.KeyID, invitation.Envelope, alice.AuthorityPublicKey, invitation.Signature); err != nil {
		t.Fatal(err)
	}

	oldKey, oldKeyID, err := alice.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var oldRaw []byte
	oldKey.Use(func(b []byte) error { oldRaw = append([]byte(nil), b...); return nil })

	recipients := map[model.ID][]byte{alice.ID: alice.IdentityPublicKey, bob.ID: bob.IdentityPublicKey}
	rotation, err := alice.BeginWorkspaceKeyRotation(workspaceID, recipients)
	if err != nil {
		t.Fatalf("BeginWorkspaceKeyRotation: %v", err)
	}
	if bytesEqual(rotation.KeyID, oldKeyID) {
		t.Fatal("rotation must generate a fresh key ID")
	}
	if len(rotation.Recipients) != 2 {
		t.Fatalf("expected 2 sealed recipients, got %d", len(rotation.Recipients))
	}

	envelopeFor := func(userID model.ID) []byte {
		for _, r := range rotation.Recipients {
			if r.UserID == userID {
				return r.Envelope
			}
		}
		t.Fatalf("no envelope for %s", userID)
		return nil
	}
	recipientIDs := []model.ID{alice.ID, bob.ID}

	// Alice applies her own rotated envelope, exactly like any recipient.
	if err := alice.AcceptWorkspaceKeyRotation(context.Background(), workspaceID, rotation.KeyID, envelopeFor(alice.ID), alice.AuthorityPublicKey, rotation.Signature, recipientIDs); err != nil {
		t.Fatalf("alice AcceptWorkspaceKeyRotation: %v", err)
	}
	if err := bob.AcceptWorkspaceKeyRotation(context.Background(), workspaceID, rotation.KeyID, envelopeFor(bob.ID), alice.AuthorityPublicKey, rotation.Signature, recipientIDs); err != nil {
		t.Fatalf("bob AcceptWorkspaceKeyRotation: %v", err)
	}

	newAliceKey, newAliceKeyID, err := alice.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	newBobKey, newBobKeyID, err := bob.WorkspaceKey(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqual(newAliceKeyID, rotation.KeyID) || !bytesEqual(newBobKeyID, rotation.KeyID) {
		t.Fatal("both accounts should now report the rotated key as current")
	}
	var newAliceRaw, newBobRaw []byte
	newAliceKey.Use(func(b []byte) error { newAliceRaw = append([]byte(nil), b...); return nil })
	newBobKey.Use(func(b []byte) error { newBobRaw = append([]byte(nil), b...); return nil })
	if !bytesEqual(newAliceRaw, newBobRaw) {
		t.Fatal("both accounts should converge on the identical new key")
	}
	if bytesEqual(newAliceRaw, oldRaw) {
		t.Fatal("the rotated key must differ from the old key")
	}

	// Historical-key reads: the old key is still retrievable by its ID for
	// decrypting content written before the rotation.
	historical, ok := alice.workspaceKeyByID(workspaceID, oldKeyID)
	if !ok {
		t.Fatal("expected the old key to remain available for historical reads")
	}
	var historicalRaw []byte
	historical.Key.Use(func(b []byte) error { historicalRaw = append([]byte(nil), b...); return nil })
	if !bytesEqual(historicalRaw, oldRaw) {
		t.Fatal("historical key lookup returned the wrong key material")
	}

	// Idempotent re-application.
	if err := alice.AcceptWorkspaceKeyRotation(context.Background(), workspaceID, rotation.KeyID, envelopeFor(alice.ID), alice.AuthorityPublicKey, rotation.Signature, recipientIDs); err != nil {
		t.Fatalf("re-applying the same rotation should be idempotent: %v", err)
	}
}

func TestWorkspaceKeyRotationRejectsTamperedSignature(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	invitation, err := alice.ShareWorkspace(workspaceID, bob.ID, bob.IdentityPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.AcceptWorkspaceShare(context.Background(), invitation.WorkspaceID, invitation.KeyID, invitation.Envelope, alice.AuthorityPublicKey, invitation.Signature); err != nil {
		t.Fatal(err)
	}

	recipients := map[model.ID][]byte{alice.ID: alice.IdentityPublicKey, bob.ID: bob.IdentityPublicKey}
	rotation, err := alice.BeginWorkspaceKeyRotation(workspaceID, recipients)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), rotation.Signature...)
	tampered[0] ^= 0xFF

	var bobEnvelope []byte
	for _, r := range rotation.Recipients {
		if r.UserID == bob.ID {
			bobEnvelope = r.Envelope
		}
	}
	if err := bob.AcceptWorkspaceKeyRotation(context.Background(), workspaceID, rotation.KeyID, bobEnvelope, alice.AuthorityPublicKey, tampered, []model.ID{alice.ID, bob.ID}); err == nil {
		t.Fatal("expected a tampered key-transition signature to be rejected")
	}
	if _, keyID, err := bob.WorkspaceKey(workspaceID); err != nil || bytesEqual(keyID, rotation.KeyID) {
		t.Fatal("a rejected rotation must not be applied")
	}
}

func TestBeginWorkspaceKeyRotationRejectsUnknownWorkspace(t *testing.T) {
	alice := newTestAccount(t)
	unknown, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.BeginWorkspaceKeyRotation(unknown, map[model.ID][]byte{alice.ID: alice.IdentityPublicKey}); err != ErrUnknownWorkspace {
		t.Fatalf("expected ErrUnknownWorkspace, got %v", err)
	}
}
