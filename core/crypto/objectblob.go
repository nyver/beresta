package crypto

import "errors"

// ErrMalformedEncryptedBlob reports a stored encrypted-object blob too short
// to contain a key ID and nonce.
var ErrMalformedEncryptedBlob = errors.New("crypto: malformed encrypted blob")

// EncryptAndPackObject encrypts plaintext for one workspace object and packs
// the result into a single storage blob: key_id(16) || nonce(24) ||
// ciphertext. This is the on-disk shape of a one-column encrypted field
// (e.g. crdt_states.snapshot, revisions.data). workspace_id, object_id,
// object_type, and schema_version are not repeated in the blob: the caller's
// own storage context (which table, which row) already fixes them, and
// re-supplying the same values at decrypt time is exactly what proves the
// row has not been moved or substituted (see ObjectMetadata's canonical
// AAD). key_id is packed because a future workspace key rotation must still
// be able to read objects encrypted under a retired key.
func EncryptAndPackObject(workspaceKey *Secret, metadata ObjectMetadata, plaintext *Secret) ([]byte, error) {
	encrypted, err := EncryptObject(workspaceKey, metadata, plaintext)
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(metadata.KeyID)+len(encrypted.Nonce)+len(encrypted.Ciphertext))
	blob = append(blob, metadata.KeyID...)
	blob = append(blob, encrypted.Nonce...)
	blob = append(blob, encrypted.Ciphertext...)
	return blob, nil
}

// UnpackAndOpenObject splits a blob produced by EncryptAndPackObject and
// authenticates it. metadata must carry the caller's expected workspace_id,
// object_id, object_type, and schema_version; its KeyID field is ignored and
// overwritten from the blob, since the caller does not need to know which
// key was current at encryption time.
func UnpackAndOpenObject(workspaceKey *Secret, metadata ObjectMetadata, blob []byte) (*Secret, error) {
	if len(blob) < KeyIDBytes+XChaCha20NonceBytes+AEADTagBytes {
		return nil, ErrMalformedEncryptedBlob
	}
	metadata.KeyID = append([]byte(nil), blob[:KeyIDBytes]...)
	nonce := append([]byte(nil), blob[KeyIDBytes:KeyIDBytes+XChaCha20NonceBytes]...)
	ciphertext := append([]byte(nil), blob[KeyIDBytes+XChaCha20NonceBytes:]...)
	return OpenObject(workspaceKey, EncryptedObject{
		Metadata:   metadata,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	})
}
