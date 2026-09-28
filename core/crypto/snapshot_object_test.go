package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func testSnapshotMetadata() ObjectMetadata {
	metadata := testObjectMetadata()
	metadata.ObjectType = ObjectTypeWorkspaceSnapshot
	return metadata
}

// A full workspace replay archive routinely exceeds the live-key Secret
// bound; publishing it must not fail with ErrInvalidSecretSize.
func TestSnapshotObjectRoundTripsArchivesLargerThanSecretLimit(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x5a}, HKDFKeyBytes))
	defer workspaceKey.Close()
	plaintext := bytes.Repeat([]byte("snapshot archive "), (3*MaxSecretBytes)/17)
	envelope, err := EncryptSnapshotObject(workspaceKey, testSnapshotMetadata(), plaintext)
	if err != nil {
		t.Fatalf("EncryptSnapshotObject(%d bytes) error = %v", len(plaintext), err)
	}
	if len(envelope.Ciphertext) != len(plaintext)+AEADTagBytes {
		t.Fatalf("ciphertext length = %d, want %d", len(envelope.Ciphertext), len(plaintext)+AEADTagBytes)
	}
	opened, err := OpenSnapshotObject(workspaceKey, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatal("opened snapshot archive differs from plaintext")
	}

	tampered := cloneEncryptedObject(envelope)
	tampered.Ciphertext[len(tampered.Ciphertext)/2] ^= 1
	if opened, err := OpenSnapshotObject(workspaceKey, tampered); !errors.Is(err, ErrAuthentication) || opened != nil {
		t.Fatalf("tampered snapshot result = %d bytes, error = %v", len(opened), err)
	}
}

// Snapshots published by older clients through EncryptObject must still
// open, and small new snapshots must still open on older clients.
func TestSnapshotObjectIsWireCompatibleWithObjectEnvelope(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x5a}, HKDFKeyBytes))
	defer workspaceKey.Close()
	metadata := testSnapshotMetadata()
	plaintextBytes := []byte("small snapshot archive")
	nonce := sequentialBytes(7, XChaCha20NonceBytes)

	plaintext := takeTestSecret(t, plaintextBytes)
	defer plaintext.Close()
	legacy, err := encryptObject(workspaceKey, metadata, plaintext, bytes.NewReader(nonce))
	if err != nil {
		t.Fatal(err)
	}
	current, err := encryptSnapshotObject(workspaceKey, metadata, plaintextBytes, bytes.NewReader(nonce))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy.Nonce, current.Nonce) || !bytes.Equal(legacy.Ciphertext, current.Ciphertext) {
		t.Fatal("snapshot envelope differs from the generic object envelope")
	}
	opened, err := OpenSnapshotObject(workspaceKey, legacy)
	if err != nil || !bytes.Equal(opened, plaintextBytes) {
		t.Fatalf("OpenSnapshotObject(legacy) = %q, %v", opened, err)
	}
	openedSecret, err := OpenObject(workspaceKey, current)
	if err != nil {
		t.Fatal(err)
	}
	defer openedSecret.Close()
	if got := copySecret(t, openedSecret); !bytes.Equal(got, plaintextBytes) {
		t.Fatalf("OpenObject(current) = %q, want %q", got, plaintextBytes)
	}
}

func TestSnapshotObjectValidation(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x5a}, HKDFKeyBytes))
	defer workspaceKey.Close()

	if _, err := EncryptSnapshotObject(workspaceKey, testObjectMetadata(), []byte("x")); !errors.Is(err, ErrInvalidEncryptionMetadata) {
		t.Fatalf("non-snapshot object type error = %v", err)
	}
	if _, err := EncryptSnapshotObject(workspaceKey, testSnapshotMetadata(), nil); !errors.Is(err, ErrInvalidSecretSize) {
		t.Fatalf("empty archive error = %v", err)
	}
	oversized := make([]byte, MaxSnapshotCiphertextBytes-AEADTagBytes+1)
	if _, err := EncryptSnapshotObject(workspaceKey, testSnapshotMetadata(), oversized); !errors.Is(err, ErrSnapshotResourceLimit) {
		t.Fatalf("oversized archive error = %v", err)
	}
	if _, err := encryptSnapshotObject(workspaceKey, testSnapshotMetadata(), []byte("x"), nil); !errors.Is(err, ErrRandomSource) {
		t.Fatalf("nil random source error = %v", err)
	}

	envelope, err := EncryptSnapshotObject(workspaceKey, testSnapshotMetadata(), []byte("archive"))
	if err != nil {
		t.Fatal(err)
	}
	retyped := cloneEncryptedObject(envelope)
	retyped.Metadata.ObjectType = ObjectTypeRevision
	if _, err := OpenSnapshotObject(workspaceKey, retyped); !errors.Is(err, ErrInvalidEncryptionMetadata) {
		t.Fatalf("retyped envelope error = %v", err)
	}
	truncated := cloneEncryptedObject(envelope)
	truncated.Ciphertext = truncated.Ciphertext[:AEADTagBytes-1]
	if _, err := OpenSnapshotObject(workspaceKey, truncated); !errors.Is(err, ErrInvalidEncryptionMetadata) {
		t.Fatalf("truncated envelope error = %v", err)
	}
}
