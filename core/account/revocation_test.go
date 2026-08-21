package account

import (
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func TestSignDeviceRevocationVerifies(t *testing.T) {
	alice := newTestAccount(t)
	record, err := alice.SignDeviceRevocation(alice.DeviceID)
	if err != nil {
		t.Fatalf("SignDeviceRevocation: %v", err)
	}
	if record.Kind != RevocationKindDevice || record.TargetID != alice.DeviceID {
		t.Fatalf("unexpected record: %+v", record)
	}
	if err := VerifyRevocation(alice.AuthorityPublicKey, record); err != nil {
		t.Fatalf("VerifyRevocation: %v", err)
	}
}

func TestSignMemberRevocationVerifies(t *testing.T) {
	alice := newTestAccount(t)
	bob := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)

	record, err := alice.SignMemberRevocation(workspaceID, bob.ID)
	if err != nil {
		t.Fatalf("SignMemberRevocation: %v", err)
	}
	if record.Kind != RevocationKindMember || record.TargetID != bob.ID || record.WorkspaceID != workspaceID {
		t.Fatalf("unexpected record: %+v", record)
	}
	if err := VerifyRevocation(alice.AuthorityPublicKey, record); err != nil {
		t.Fatalf("VerifyRevocation: %v", err)
	}
}

func TestVerifyRevocationRejectsTamperingAndWrongSigner(t *testing.T) {
	alice := newTestAccount(t)
	mallory := newTestAccount(t)
	record, err := alice.SignDeviceRevocation(alice.DeviceID)
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifyRevocation(mallory.AuthorityPublicKey, record); err == nil {
		t.Fatal("expected verification against the wrong signer to fail")
	}

	tampered := record
	tampered.TargetID = mallory.DeviceID
	if err := VerifyRevocation(alice.AuthorityPublicKey, tampered); err == nil {
		t.Fatal("expected verification of a modified target to fail")
	}

	tamperedSig := record
	tamperedSig.Signature = append([]byte(nil), record.Signature...)
	tamperedSig.Signature[0] ^= 0xFF
	if err := VerifyRevocation(alice.AuthorityPublicKey, tamperedSig); err == nil {
		t.Fatal("expected a tampered signature to fail verification")
	}
}

func TestRevocationKindDistinguishesSignatureNamespace(t *testing.T) {
	alice := newTestAccount(t)
	workspaceID := onlyWorkspace(t, alice)
	target, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}

	deviceRecord, err := alice.SignDeviceRevocation(target)
	if err != nil {
		t.Fatal(err)
	}
	// Re-labeling the same signed device revocation as a member revocation
	// over the same workspace/target must not verify: kind is part of the
	// signed payload.
	relabeled := deviceRecord
	relabeled.Kind = RevocationKindMember
	relabeled.WorkspaceID = workspaceID
	if err := VerifyRevocation(alice.AuthorityPublicKey, relabeled); err == nil {
		t.Fatal("expected relabeling a revocation's kind to invalidate its signature")
	}
}
