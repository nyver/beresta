package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

func TestCanonicalSnapshotAndAcknowledgementSignatureInputs(t *testing.T) {
	deviceID := bytes.Repeat([]byte{0x33}, DeviceIDBytes)
	snapshotFields := SnapshotSignatureFields{
		SnapshotID: bytes.Repeat([]byte{0x11}, SnapshotIDBytes), WorkspaceID: bytes.Repeat([]byte{0x22}, WorkspaceIDBytes),
		BaseSequence: 7, CursorEpoch: 1, KeyID: bytes.Repeat([]byte{0x44}, KeyIDBytes),
		CreatorDeviceID: deviceID, HLCPhysicalMS: 1234, HLCLogical: 2, HLCDeviceID: deviceID,
		Nonce: bytes.Repeat([]byte{0x55}, XChaCha20NonceBytes), CiphertextHash: bytes.Repeat([]byte{0x66}, 32),
		Ciphertext: bytes.Repeat([]byte{0x77}, AEADTagBytes),
	}
	payload, err := CanonicalSnapshotSignatureInput(snapshotFields)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := CanonicalSnapshotSignatureInput(snapshotFields)
	if err != nil || !bytes.Equal(payload, repeated) {
		t.Fatalf("canonical snapshot encoding is not deterministic: %v", err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed := appendLengthPrefixed(nil, []byte(SignatureDomainSnapshot))
	signed = append(signed, payload...)
	signature := ed25519.Sign(privateKey, signed)
	if err := VerifyCanonical(CryptoProfileV1, publicKey, SignatureDomainSnapshot, payload, signature); err != nil {
		t.Fatal(err)
	}

	ack, err := CanonicalSnapshotAckSignatureInput(SnapshotAckSignatureFields{
		SnapshotID: snapshotFields.SnapshotID, WorkspaceID: snapshotFields.WorkspaceID,
		DeviceID: deviceID, BaseSequence: snapshotFields.BaseSequence, CiphertextHash: snapshotFields.CiphertextHash,
	})
	if err != nil || bytes.Equal(payload, ack) {
		t.Fatalf("snapshot acknowledgement encoding error=%v", err)
	}
	if _, err := CanonicalSnapshotSignatureInput(SnapshotSignatureFields{}); !errors.Is(err, ErrInvalidSnapshotFields) {
		t.Fatalf("invalid snapshot fields error = %v", err)
	}
}
