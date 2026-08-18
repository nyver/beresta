package crypto

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	KeybagMagic         = "BRSTKDF1"
	KeybagFormatVersion = 1
	XChaCha20NonceBytes = chacha20poly1305.NonceSizeX
	AEADTagBytes        = 16
	MaxCiphertextBytes  = MaxSecretBytes + AEADTagBytes
)

var (
	ErrInvalidEncryptionMetadata = errors.New("crypto: invalid encryption metadata")
	ErrInvalidEncryptionKey      = errors.New("crypto: invalid encryption key")
	ErrAuthentication            = errors.New("crypto: authentication failed")
	ErrKeybagUnlock              = errors.New("crypto: unable to unlock keybag")
)

// KeybagHeader is the authenticated cleartext metadata for one encrypted
// account keybag. KDF includes its persisted salt and bounded parameters.
type KeybagHeader struct {
	Magic         []byte         `json:"magic" cbor:"magic"`
	FormatVersion uint32         `json:"format_version" cbor:"format_version"`
	CryptoProfile string         `json:"crypto_profile" cbor:"crypto_profile"`
	AccountID     []byte         `json:"account_id" cbor:"account_id"`
	KeybagVersion uint64         `json:"keybag_version" cbor:"keybag_version"`
	KDF           Argon2idParams `json:"kdf" cbor:"kdf"`
}

// EncryptedKeybag is safe to persist or transport. Its ciphertext includes
// the Poly1305 tag; its nonce is generated internally by the production API.
type EncryptedKeybag struct {
	Header     KeybagHeader `json:"header" cbor:"header"`
	Nonce      []byte       `json:"nonce" cbor:"nonce"`
	Ciphertext []byte       `json:"ciphertext" cbor:"ciphertext"`
}

// ObjectMetadata is the complete canonical associated data for an encrypted
// workspace object.
type ObjectMetadata struct {
	SchemaVersion uint32     `json:"schema_version" cbor:"schema_version"`
	CryptoProfile string     `json:"crypto_profile" cbor:"crypto_profile"`
	WorkspaceID   []byte     `json:"workspace_id" cbor:"workspace_id"`
	ObjectID      []byte     `json:"object_id" cbor:"object_id"`
	ObjectType    ObjectType `json:"object_type" cbor:"object_type"`
	KeyID         []byte     `json:"key_id" cbor:"key_id"`
}

// EncryptedObject is a visible, self-describing workspace object envelope.
type EncryptedObject struct {
	Metadata   ObjectMetadata `json:"metadata" cbor:"metadata"`
	Nonce      []byte         `json:"nonce" cbor:"nonce"`
	Ciphertext []byte         `json:"ciphertext" cbor:"ciphertext"`
}

// NewKeybagHeader constructs authenticated metadata from persisted KDF
// parameters. The returned header owns independent ID and salt buffers.
func NewKeybagHeader(accountID []byte, keybagVersion uint64, params Argon2idParams) (KeybagHeader, error) {
	header := KeybagHeader{
		Magic:         []byte(KeybagMagic),
		FormatVersion: KeybagFormatVersion,
		CryptoProfile: CryptoProfileV1,
		AccountID:     append([]byte(nil), accountID...),
		KeybagVersion: keybagVersion,
		KDF:           params.Clone(),
	}
	if err := header.validate(); err != nil {
		wipe(header.KDF.Salt)
		return KeybagHeader{}, err
	}
	return header, nil
}

// EncryptKeybag derives the account keybag key and encrypts the caller-owned
// canonical keybag plaintext with a fresh random nonce.
func EncryptKeybag(rootKey *Secret, header KeybagHeader, plaintext *Secret) (EncryptedKeybag, error) {
	return encryptKeybag(rootKey, header, plaintext, cryptorand.Reader)
}

func encryptKeybag(rootKey *Secret, header KeybagHeader, plaintext *Secret, random io.Reader) (EncryptedKeybag, error) {
	if err := header.validate(); err != nil {
		return EncryptedKeybag{}, err
	}
	aad, err := CanonicalKeybagAAD(header)
	if err != nil {
		return EncryptedKeybag{}, err
	}
	key, err := DeriveKeybagKey(header.CryptoProfile, rootKey, header.AccountID)
	if err != nil {
		return EncryptedKeybag{}, err
	}
	defer key.Close()
	nonce, ciphertext, err := sealXChaCha(key, plaintext, aad, random)
	if err != nil {
		return EncryptedKeybag{}, err
	}
	return EncryptedKeybag{
		Header:     cloneKeybagHeader(header),
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

// OpenKeybag authenticates an encrypted keybag with a candidate Root Key.
// Wrong candidates and ciphertext authentication failures are indistinguishable.
func OpenKeybag(rootKey *Secret, envelope EncryptedKeybag) (*Secret, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	aad, err := CanonicalKeybagAAD(envelope.Header)
	if err != nil {
		return nil, err
	}
	key, err := DeriveKeybagKey(envelope.Header.CryptoProfile, rootKey, envelope.Header.AccountID)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	plaintext, err := openXChaCha(key, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			return nil, ErrKeybagUnlock
		}
		return nil, err
	}
	return plaintext, nil
}

// UnlockKeybag derives a candidate Root Key locally and opens the keybag. No
// standalone password verifier is stored or consulted.
func UnlockKeybag(ctx context.Context, passphrase []byte, envelope EncryptedKeybag) (*Secret, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	rootKey, err := DeriveRootKey(ctx, passphrase, envelope.Header.KDF)
	if err != nil {
		return nil, err
	}
	defer rootKey.Close()
	return OpenKeybag(rootKey, envelope)
}

// EncryptObject derives an object-specific key and seals plaintext with all
// visible immutable metadata as canonical associated data.
func EncryptObject(workspaceKey *Secret, metadata ObjectMetadata, plaintext *Secret) (EncryptedObject, error) {
	return encryptObject(workspaceKey, metadata, plaintext, cryptorand.Reader)
}

func encryptObject(workspaceKey *Secret, metadata ObjectMetadata, plaintext *Secret, random io.Reader) (EncryptedObject, error) {
	if err := metadata.validate(); err != nil {
		return EncryptedObject{}, err
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
	nonce, ciphertext, err := sealXChaCha(key, plaintext, aad, random)
	if err != nil {
		return EncryptedObject{}, err
	}
	return EncryptedObject{
		Metadata:   cloneObjectMetadata(metadata),
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

// OpenObject authenticates before returning an owned plaintext Secret.
func OpenObject(workspaceKey *Secret, envelope EncryptedObject) (*Secret, error) {
	if err := envelope.validate(); err != nil {
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
	return openXChaCha(key, envelope.Nonce, envelope.Ciphertext, aad)
}

func sealXChaCha(key, plaintext *Secret, aad []byte, random io.Reader) ([]byte, []byte, error) {
	if key == nil || plaintext == nil {
		return nil, nil, ErrSecretClosed
	}
	if plaintext.Len() == 0 || plaintext.Len() > MaxSecretBytes {
		return nil, nil, ErrInvalidSecretSize
	}
	if random == nil {
		return nil, nil, ErrRandomSource
	}
	nonce := make([]byte, XChaCha20NonceBytes)
	if _, err := io.ReadFull(random, nonce); err != nil {
		wipe(nonce)
		return nil, nil, fmt.Errorf("%w: XChaCha20 nonce generation", ErrRandomSource)
	}

	var ciphertext []byte
	err := key.Use(func(keyBytes []byte) error {
		if len(keyBytes) != chacha20poly1305.KeySize {
			return ErrInvalidEncryptionKey
		}
		aead, err := chacha20poly1305.NewX(keyBytes)
		if err != nil {
			return ErrInvalidEncryptionKey
		}
		return plaintext.Use(func(plaintextBytes []byte) error {
			ciphertext = aead.Seal(nil, nonce, plaintextBytes, aad)
			return nil
		})
	})
	if err != nil {
		wipe(nonce)
		wipe(ciphertext)
		return nil, nil, err
	}
	return nonce, ciphertext, nil
}

func openXChaCha(key *Secret, nonce, ciphertext, aad []byte) (*Secret, error) {
	if key == nil {
		return nil, ErrSecretClosed
	}
	if len(nonce) != XChaCha20NonceBytes || len(ciphertext) < AEADTagBytes || len(ciphertext) > MaxCiphertextBytes {
		return nil, ErrInvalidEncryptionMetadata
	}
	var plaintext []byte
	var openErr error
	err := key.Use(func(keyBytes []byte) error {
		if len(keyBytes) != chacha20poly1305.KeySize {
			return ErrInvalidEncryptionKey
		}
		aead, err := chacha20poly1305.NewX(keyBytes)
		if err != nil {
			return ErrInvalidEncryptionKey
		}
		plaintext, openErr = aead.Open(nil, nonce, ciphertext, aad)
		return nil
	})
	if err != nil {
		wipe(plaintext)
		return nil, err
	}
	if openErr != nil {
		wipe(plaintext)
		return nil, ErrAuthentication
	}
	result, err := TakeSecret(plaintext)
	if err != nil {
		wipe(plaintext)
		return nil, err
	}
	return result, nil
}

func (header KeybagHeader) validate() error {
	if header.CryptoProfile != CryptoProfileV1 {
		return ErrUnsupportedCryptoProfile
	}
	if string(header.Magic) != KeybagMagic || header.FormatVersion != KeybagFormatVersion || header.KeybagVersion == 0 {
		return ErrInvalidEncryptionMetadata
	}
	if header.KDF.CryptoProfile != CryptoProfileV1 {
		return ErrUnsupportedCryptoProfile
	}
	if err := requireBytes("account ID", header.AccountID, AccountIDBytes); err != nil {
		return ErrInvalidEncryptionMetadata
	}
	if err := header.KDF.Validate(); err != nil || header.KDF.CryptoProfile != header.CryptoProfile {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func (envelope EncryptedKeybag) validate() error {
	if err := envelope.Header.validate(); err != nil {
		return err
	}
	if len(envelope.Nonce) != XChaCha20NonceBytes || len(envelope.Ciphertext) < AEADTagBytes || len(envelope.Ciphertext) > MaxCiphertextBytes {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func (metadata ObjectMetadata) validate() error {
	if metadata.CryptoProfile != CryptoProfileV1 || metadata.SchemaVersion != SchemaVersionV1 {
		return ErrUnsupportedCryptoProfile
	}
	if err := requireBytes("workspace ID", metadata.WorkspaceID, WorkspaceIDBytes); err != nil {
		return ErrInvalidEncryptionMetadata
	}
	if err := requireBytes("object ID", metadata.ObjectID, ObjectIDBytes); err != nil {
		return ErrInvalidEncryptionMetadata
	}
	if err := requireBytes("key ID", metadata.KeyID, KeyIDBytes); err != nil {
		return ErrInvalidEncryptionMetadata
	}
	if !validObjectType(metadata.ObjectType) {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func (envelope EncryptedObject) validate() error {
	if err := envelope.Metadata.validate(); err != nil {
		return err
	}
	if len(envelope.Nonce) != XChaCha20NonceBytes || len(envelope.Ciphertext) < AEADTagBytes || len(envelope.Ciphertext) > MaxCiphertextBytes {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func cloneKeybagHeader(header KeybagHeader) KeybagHeader {
	header.Magic = append([]byte(nil), header.Magic...)
	header.AccountID = append([]byte(nil), header.AccountID...)
	header.KDF = header.KDF.Clone()
	return header
}

func cloneObjectMetadata(metadata ObjectMetadata) ObjectMetadata {
	metadata.WorkspaceID = append([]byte(nil), metadata.WorkspaceID...)
	metadata.ObjectID = append([]byte(nil), metadata.ObjectID...)
	metadata.KeyID = append([]byte(nil), metadata.KeyID...)
	return metadata
}
