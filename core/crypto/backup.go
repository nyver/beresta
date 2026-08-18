package crypto

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
)

const (
	BackupMagic                  = "BRSTBAK1"
	BackupFormatVersion          = 1
	MaxBackupArchiveBytes uint64 = 64 * 1024 * 1024 * 1024
)

var ErrBackupResourceLimit = errors.New("crypto: backup resource limit exceeded")

// BackupMetadata is the caller-provided immutable portion of a standalone
// encrypted backup header.
type BackupMetadata struct {
	Magic         []byte         `json:"magic" cbor:"magic"`
	FormatVersion uint32         `json:"format_version" cbor:"format_version"`
	CryptoProfile string         `json:"crypto_profile" cbor:"crypto_profile"`
	AccountID     []byte         `json:"account_id" cbor:"account_id"`
	BackupID      []byte         `json:"backup_id" cbor:"backup_id"`
	CreatedUnixMS uint64         `json:"created_unix_ms" cbor:"created_unix_ms"`
	KDF           Argon2idParams `json:"kdf" cbor:"kdf"`
}

// BackupHeader is the complete canonical AAD, including the random nonce and
// exact ciphertext size that follow it in the file container.
type BackupHeader struct {
	BackupMetadata
	Nonce          []byte `json:"nonce" cbor:"nonce"`
	CiphertextSize uint64 `json:"ciphertext_size" cbor:"ciphertext_size"`
}

type EncryptedBackup struct {
	Header     BackupHeader `json:"header" cbor:"header"`
	Ciphertext []byte       `json:"ciphertext" cbor:"ciphertext"`
}

func NewBackupMetadata(accountID, backupID []byte, createdUnixMS uint64, params Argon2idParams) (BackupMetadata, error) {
	metadata := BackupMetadata{
		Magic:         []byte(BackupMagic),
		FormatVersion: BackupFormatVersion,
		CryptoProfile: CryptoProfileV1,
		AccountID:     append([]byte(nil), accountID...),
		BackupID:      append([]byte(nil), backupID...),
		CreatedUnixMS: createdUnixMS,
		KDF:           params.Clone(),
	}
	if err := metadata.validate(); err != nil {
		wipe(metadata.KDF.Salt)
		return BackupMetadata{}, err
	}
	return metadata, nil
}

// EncryptBackup encrypts an already assembled archive. archive remains owned
// by the caller and should be wiped or released after durable publication.
func EncryptBackup(rootKey *Secret, metadata BackupMetadata, archive []byte) (EncryptedBackup, error) {
	return encryptBackup(rootKey, metadata, archive, cryptorand.Reader)
}

func encryptBackup(rootKey *Secret, metadata BackupMetadata, archive []byte, random io.Reader) (EncryptedBackup, error) {
	if err := metadata.validate(); err != nil {
		return EncryptedBackup{}, err
	}
	maxInt := int(^uint(0) >> 1)
	if len(archive) > maxInt-AEADTagBytes || uint64(len(archive)) > MaxBackupArchiveBytes-AEADTagBytes {
		return EncryptedBackup{}, ErrBackupResourceLimit
	}
	if random == nil {
		return EncryptedBackup{}, ErrRandomSource
	}
	nonce := make([]byte, XChaCha20NonceBytes)
	if _, err := io.ReadFull(random, nonce); err != nil {
		wipe(nonce)
		return EncryptedBackup{}, fmt.Errorf("%w: backup nonce generation", ErrRandomSource)
	}
	header := BackupHeader{
		BackupMetadata: cloneBackupMetadata(metadata),
		Nonce:          nonce,
		CiphertextSize: uint64(len(archive) + AEADTagBytes),
	}
	aad, err := CanonicalBackupAAD(header)
	if err != nil {
		wipe(nonce)
		return EncryptedBackup{}, err
	}
	key, err := DeriveBackupKey(metadata.CryptoProfile, rootKey, metadata.AccountID, metadata.BackupID)
	if err != nil {
		wipe(nonce)
		return EncryptedBackup{}, err
	}
	defer key.Close()
	ciphertext, err := sealXChaChaWithNonce(key, archive, nonce, aad)
	if err != nil {
		wipe(nonce)
		return EncryptedBackup{}, err
	}
	return EncryptedBackup{Header: header, Ciphertext: ciphertext}, nil
}

func OpenBackup(rootKey *Secret, envelope EncryptedBackup) ([]byte, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	aad, err := CanonicalBackupAAD(envelope.Header)
	if err != nil {
		return nil, err
	}
	key, err := DeriveBackupKey(envelope.Header.CryptoProfile, rootKey, envelope.Header.AccountID, envelope.Header.BackupID)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	return openXChaChaBytes(key, envelope.Header.Nonce, envelope.Ciphertext, aad, len(envelope.Ciphertext))
}

func UnlockBackup(ctx context.Context, passphrase []byte, envelope EncryptedBackup) ([]byte, error) {
	if err := envelope.validate(); err != nil {
		return nil, err
	}
	rootKey, err := DeriveRootKey(ctx, passphrase, envelope.Header.KDF)
	if err != nil {
		return nil, err
	}
	defer rootKey.Close()
	return OpenBackup(rootKey, envelope)
}

func (metadata BackupMetadata) validate() error {
	if metadata.CryptoProfile != CryptoProfileV1 || metadata.KDF.CryptoProfile != CryptoProfileV1 {
		return ErrUnsupportedCryptoProfile
	}
	if string(metadata.Magic) != BackupMagic || metadata.FormatVersion != BackupFormatVersion {
		return ErrInvalidEncryptionMetadata
	}
	if len(metadata.AccountID) != AccountIDBytes || len(metadata.BackupID) != BackupIDBytes {
		return ErrInvalidEncryptionMetadata
	}
	if err := metadata.KDF.Validate(); err != nil {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func (header BackupHeader) validate() error {
	if err := header.BackupMetadata.validate(); err != nil {
		return err
	}
	if len(header.Nonce) != XChaCha20NonceBytes || header.CiphertextSize < AEADTagBytes || header.CiphertextSize > MaxBackupArchiveBytes {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func (envelope EncryptedBackup) validate() error {
	if err := envelope.Header.validate(); err != nil {
		return err
	}
	if envelope.Header.CiphertextSize != uint64(len(envelope.Ciphertext)) {
		return ErrInvalidEncryptionMetadata
	}
	return nil
}

func cloneBackupMetadata(metadata BackupMetadata) BackupMetadata {
	metadata.Magic = append([]byte(nil), metadata.Magic...)
	metadata.AccountID = append([]byte(nil), metadata.AccountID...)
	metadata.BackupID = append([]byte(nil), metadata.BackupID...)
	metadata.KDF = metadata.KDF.Clone()
	return metadata
}

func cloneEncryptedBackup(envelope EncryptedBackup) EncryptedBackup {
	envelope.Header.BackupMetadata = cloneBackupMetadata(envelope.Header.BackupMetadata)
	envelope.Header.Nonce = append([]byte(nil), envelope.Header.Nonce...)
	envelope.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	return envelope
}
