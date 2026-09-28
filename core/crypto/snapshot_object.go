package crypto

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
)

// ErrSnapshotResourceLimit reports a workspace snapshot archive whose
// ciphertext would exceed MaxSnapshotCiphertextBytes.
var ErrSnapshotResourceLimit = errors.New("crypto: workspace snapshot exceeds size limit")

// EncryptSnapshotObject seals a workspace snapshot archive. It produces the
// same envelope as EncryptObject (identical key derivation, AAD, and nonce
// format), but takes a caller-owned byte slice bounded by
// MaxSnapshotCiphertextBytes instead of a Secret, because a full workspace
// replay archive routinely exceeds the live-key MaxSecretBytes bound.
// plaintext remains owned by the caller, which should wipe it afterwards.
func EncryptSnapshotObject(workspaceKey *Secret, metadata ObjectMetadata, plaintext []byte) (EncryptedObject, error) {
	return encryptSnapshotObject(workspaceKey, metadata, plaintext, cryptorand.Reader)
}

func encryptSnapshotObject(workspaceKey *Secret, metadata ObjectMetadata, plaintext []byte, random io.Reader) (EncryptedObject, error) {
	if err := validateSnapshotMetadata(metadata); err != nil {
		return EncryptedObject{}, err
	}
	if len(plaintext) == 0 {
		return EncryptedObject{}, ErrInvalidSecretSize
	}
	if len(plaintext) > MaxSnapshotCiphertextBytes-AEADTagBytes {
		return EncryptedObject{}, ErrSnapshotResourceLimit
	}
	if random == nil {
		return EncryptedObject{}, ErrRandomSource
	}
	aad, err := CanonicalObjectAAD(metadata)
	if err != nil {
		return EncryptedObject{}, err
	}
	key, err := DeriveWorkspaceObjectKey(metadata.CryptoProfile, workspaceKey, metadata.ObjectID, metadata.ObjectType, metadata.SchemaVersion)
	if err != nil {
		return EncryptedObject{}, err
	}
	defer key.Close()
	nonce := make([]byte, XChaCha20NonceBytes)
	if _, err := io.ReadFull(random, nonce); err != nil {
		return EncryptedObject{}, fmt.Errorf("%w: XChaCha20 nonce generation", ErrRandomSource)
	}
	ciphertext, err := sealXChaChaWithNonce(key, plaintext, nonce, aad)
	if err != nil {
		return EncryptedObject{}, err
	}
	return EncryptedObject{Metadata: cloneObjectMetadata(metadata), Nonce: nonce, Ciphertext: ciphertext}, nil
}

// OpenSnapshotObject authenticates a workspace snapshot envelope and returns
// the caller-owned plaintext archive. It accepts every envelope produced by
// either EncryptObject or EncryptSnapshotObject for a snapshot object.
func OpenSnapshotObject(workspaceKey *Secret, envelope EncryptedObject) ([]byte, error) {
	if err := validateSnapshotMetadata(envelope.Metadata); err != nil {
		return nil, err
	}
	aad, err := CanonicalObjectAAD(envelope.Metadata)
	if err != nil {
		return nil, err
	}
	key, err := DeriveWorkspaceObjectKey(envelope.Metadata.CryptoProfile, workspaceKey, envelope.Metadata.ObjectID, envelope.Metadata.ObjectType, envelope.Metadata.SchemaVersion)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	return openXChaChaBytes(key, envelope.Nonce, envelope.Ciphertext, aad, MaxSnapshotCiphertextBytes)
}

func validateSnapshotMetadata(metadata ObjectMetadata) error {
	if err := metadata.validate(); err != nil {
		return err
	}
	if metadata.ObjectType != ObjectTypeWorkspaceSnapshot {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}
