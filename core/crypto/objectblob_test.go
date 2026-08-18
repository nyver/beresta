package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncryptAndPackObjectRoundTrip(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x11}, HKDFKeyBytes))
	defer workspaceKey.Close()

	metadata := ObjectMetadata{
		SchemaVersion: SchemaVersionV1,
		CryptoProfile: CryptoProfileV1,
		WorkspaceID:   bytes.Repeat([]byte{0x22}, WorkspaceIDBytes),
		ObjectID:      bytes.Repeat([]byte{0x33}, ObjectIDBytes),
		ObjectType:    ObjectTypeNoteSnapshot,
		KeyID:         bytes.Repeat([]byte{0x44}, KeyIDBytes),
	}
	plaintextBytes := []byte("canonical CRDT snapshot bytes")
	plaintext := takeTestSecret(t, plaintextBytes)
	defer plaintext.Close()

	blob, err := EncryptAndPackObject(workspaceKey, metadata, plaintext)
	if err != nil {
		t.Fatalf("EncryptAndPackObject: %v", err)
	}
	if len(blob) != KeyIDBytes+XChaCha20NonceBytes+len(plaintextBytes)+AEADTagBytes {
		t.Fatalf("blob length = %d", len(blob))
	}
	if !bytes.Equal(blob[:KeyIDBytes], metadata.KeyID) {
		t.Fatal("blob does not lead with the key ID")
	}

	opened, err := UnpackAndOpenObject(workspaceKey, metadata, blob)
	if err != nil {
		t.Fatalf("UnpackAndOpenObject: %v", err)
	}
	defer opened.Close()
	if got := copySecret(t, opened); !bytes.Equal(got, plaintextBytes) {
		t.Fatalf("opened = %q, want %q", got, plaintextBytes)
	}
}

func TestUnpackAndOpenObjectRejectsWrongContextAndTampering(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x11}, HKDFKeyBytes))
	defer workspaceKey.Close()
	metadata := ObjectMetadata{
		SchemaVersion: SchemaVersionV1,
		CryptoProfile: CryptoProfileV1,
		WorkspaceID:   bytes.Repeat([]byte{0x22}, WorkspaceIDBytes),
		ObjectID:      bytes.Repeat([]byte{0x33}, ObjectIDBytes),
		ObjectType:    ObjectTypeRevision,
		KeyID:         bytes.Repeat([]byte{0x44}, KeyIDBytes),
	}
	plaintext := takeTestSecret(t, []byte("revision delta"))
	defer plaintext.Close()
	blob, err := EncryptAndPackObject(workspaceKey, metadata, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := UnpackAndOpenObject(workspaceKey, metadata, blob[:KeyIDBytes+XChaCha20NonceBytes-1]); !errors.Is(err, ErrMalformedEncryptedBlob) {
		t.Fatalf("truncated blob error = %v", err)
	}

	wrongObjectID := metadata
	wrongObjectID.ObjectID = bytes.Repeat([]byte{0x99}, ObjectIDBytes)
	if _, err := UnpackAndOpenObject(workspaceKey, wrongObjectID, blob); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("wrong object ID error = %v", err)
	}

	wrongType := metadata
	wrongType.ObjectType = ObjectTypeNoteSnapshot
	if _, err := UnpackAndOpenObject(workspaceKey, wrongType, blob); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("cross-type substitution error = %v", err)
	}

	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 1
	if _, err := UnpackAndOpenObject(workspaceKey, metadata, tampered); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("tampered ciphertext error = %v", err)
	}
}

func TestCanonicalOperationSignatureInputRoundTripsThroughSignVerify(t *testing.T) {
	fields := OperationSignatureFields{
		OpID:          bytes.Repeat([]byte{0x01}, OpIDBytes),
		WorkspaceID:   bytes.Repeat([]byte{0x02}, WorkspaceIDBytes),
		DeviceID:      bytes.Repeat([]byte{0x03}, DeviceIDBytes),
		HLCPhysicalMS: 1_700_000_000_000,
		HLCLogical:    7,
		HLCDeviceID:   bytes.Repeat([]byte{0x03}, DeviceIDBytes),
		KeyID:         bytes.Repeat([]byte{0x04}, KeyIDBytes),
		Nonce:         bytes.Repeat([]byte{0x05}, XChaCha20NonceBytes),
		Ciphertext:    bytes.Repeat([]byte{0x06}, 32),
	}
	input, err := CanonicalOperationSignatureInput(fields)
	if err != nil {
		t.Fatalf("canonical input: %v", err)
	}
	realPublic, realPrivate, err := GenerateEd25519Key()
	if err != nil {
		t.Fatal(err)
	}
	defer realPrivate.Close()

	sig, err := SignCanonical(CryptoProfileV1, realPrivate, SignatureDomainOperation, input)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := VerifyCanonical(CryptoProfileV1, realPublic, SignatureDomainOperation, input, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}

	tamperedFields := fields
	tamperedFields.HLCLogical++
	tamperedInput, err := CanonicalOperationSignatureInput(tamperedFields)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCanonical(CryptoProfileV1, realPublic, SignatureDomainOperation, tamperedInput, sig); !errors.Is(err, ErrSignatureVerification) {
		t.Fatalf("tampered field verify error = %v", err)
	}
}

func TestCanonicalOperationSignatureInputRejectsMalformedFields(t *testing.T) {
	base := OperationSignatureFields{
		OpID:          bytes.Repeat([]byte{0x01}, OpIDBytes),
		WorkspaceID:   bytes.Repeat([]byte{0x02}, WorkspaceIDBytes),
		DeviceID:      bytes.Repeat([]byte{0x03}, DeviceIDBytes),
		HLCPhysicalMS: 1,
		HLCLogical:    0,
		HLCDeviceID:   bytes.Repeat([]byte{0x03}, DeviceIDBytes),
		KeyID:         bytes.Repeat([]byte{0x04}, KeyIDBytes),
		Nonce:         bytes.Repeat([]byte{0x05}, XChaCha20NonceBytes),
		Ciphertext:    bytes.Repeat([]byte{0x06}, 32),
	}
	if _, err := CanonicalOperationSignatureInput(base); err != nil {
		t.Fatalf("valid fields rejected: %v", err)
	}

	mismatchedHLCDevice := base
	mismatchedHLCDevice.HLCDeviceID = bytes.Repeat([]byte{0x09}, DeviceIDBytes)
	if _, err := CanonicalOperationSignatureInput(mismatchedHLCDevice); !errors.Is(err, ErrInvalidOperationFields) {
		t.Fatalf("mismatched HLC device error = %v", err)
	}

	shortOpID := base
	shortOpID.OpID = shortOpID.OpID[:15]
	if _, err := CanonicalOperationSignatureInput(shortOpID); !errors.Is(err, ErrInvalidOperationFields) {
		t.Fatalf("short op ID error = %v", err)
	}

	oversizedCiphertext := base
	oversizedCiphertext.Ciphertext = make([]byte, MaxOperationCiphertextBytes+1)
	if _, err := CanonicalOperationSignatureInput(oversizedCiphertext); !errors.Is(err, ErrInvalidOperationFields) {
		t.Fatalf("oversized ciphertext error = %v", err)
	}
}
