package crypto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupEnvelopeRoundTripAndAuthenticatedHeader(t *testing.T) {
	metadata := testBackupMetadata(t)
	rootKey := takeTestSecret(t, bytes.Repeat([]byte{0x51}, RootKeyBytes))
	defer rootKey.Close()
	archive := []byte("zstd backup archive fixture")
	envelope, err := encryptBackup(rootKey, metadata, archive, bytes.NewReader(sequentialBytes(91, XChaCha20NonceBytes)))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenBackup(rootKey, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(opened, archive) {
		t.Fatalf("opened backup = %x, want %x", opened, archive)
	}
	wipe(opened)

	mutations := []func(*EncryptedBackup){
		func(candidate *EncryptedBackup) { candidate.Header.AccountID[0] ^= 1 },
		func(candidate *EncryptedBackup) { candidate.Header.BackupID[0] ^= 1 },
		func(candidate *EncryptedBackup) { candidate.Header.CreatedUnixMS++ },
		func(candidate *EncryptedBackup) { candidate.Header.KDF.Salt[0] ^= 1 },
		func(candidate *EncryptedBackup) { candidate.Header.Nonce[0] ^= 1 },
		func(candidate *EncryptedBackup) { candidate.Ciphertext[0] ^= 1 },
	}
	for _, mutate := range mutations {
		candidate := cloneEncryptedBackup(envelope)
		mutate(&candidate)
		opened, err := OpenBackup(rootKey, candidate)
		if !errors.Is(err, ErrAuthentication) || opened != nil {
			t.Fatalf("tampered backup result = %x, error = %v", opened, err)
		}
	}
}

func TestBackupUnlockAndValidationFailures(t *testing.T) {
	metadata := testBackupMetadata(t)
	passphrase := []byte("backup passphrase")
	rootKey, err := DeriveRootKey(context.Background(), passphrase, metadata.KDF)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := encryptBackup(rootKey, metadata, []byte("archive"), bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes)))
	rootKey.Close()
	if err != nil {
		t.Fatal(err)
	}
	opened, err := UnlockBackup(context.Background(), passphrase, envelope)
	if err != nil {
		t.Fatal(err)
	}
	wipe(opened)
	if opened, err = UnlockBackup(context.Background(), []byte("wrong"), envelope); !errors.Is(err, ErrAuthentication) || opened != nil {
		t.Fatalf("wrong passphrase result = %x, error = %v", opened, err)
	}

	invalidSize := cloneEncryptedBackup(envelope)
	invalidSize.Header.CiphertextSize++
	invalidRoot := takeTestSecret(t, bytes.Repeat([]byte{0x11}, RootKeyBytes))
	defer invalidRoot.Close()
	if opened, err = OpenBackup(invalidRoot, invalidSize); !errors.Is(err, ErrInvalidEncryptionMetadata) || opened != nil {
		t.Fatalf("invalid size result = %x, error = %v", opened, err)
	}
	future := metadata
	future.CryptoProfile = "beresta.crypto.v2"
	validRoot := takeTestSecret(t, bytes.Repeat([]byte{0x12}, RootKeyBytes))
	defer validRoot.Close()
	if encrypted, err := EncryptBackup(validRoot, future, nil); !errors.Is(err, ErrUnsupportedCryptoProfile) || encrypted.Ciphertext != nil {
		t.Fatalf("future profile result = %+v, error = %v", encrypted, err)
	}
	if validRoot.Len() != RootKeyBytes {
		t.Fatal("future profile validation used or wiped root key")
	}
	if encrypted, err := encryptBackup(validRoot, metadata, nil, failingIdentityReader{}); !errors.Is(err, ErrRandomSource) || encrypted.Ciphertext != nil {
		t.Fatalf("random failure result = %+v, error = %v", encrypted, err)
	}
}

func testBackupMetadata(t *testing.T) BackupMetadata {
	t.Helper()
	metadata, err := NewBackupMetadata(
		bytes.Repeat([]byte{0x11}, AccountIDBytes),
		bytes.Repeat([]byte{0x22}, BackupIDBytes),
		1710000000123,
		testArgon2idParams(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return metadata
}

func TestBackupCompatibilityVectorValues(t *testing.T) {
	var vector backupCompatibilityVector
	fixturePath := filepath.Join("..", "..", "schema", "testdata", "v1", "crypto-backup.json")
	encoded, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.FixtureFormat != 1 || vector.CryptoProfile != CryptoProfileV1 {
		t.Fatalf("unsupported backup fixture header: format=%d profile=%q", vector.FixtureFormat, vector.CryptoProfile)
	}
	params := Argon2idParams{
		CryptoProfile:   vector.CryptoProfile,
		Algorithm:       vector.KDF.Algorithm,
		Salt:            decodeVectorHex(t, vector.KDF.SaltHex),
		MemoryKiB:       vector.KDF.MemoryKiB,
		TimeCost:        vector.KDF.TimeCost,
		Parallelism:     vector.KDF.Parallelism,
		DerivedKeyBytes: vector.KDF.DerivedKeyBytes,
	}
	metadata, err := NewBackupMetadata(decodeVectorHex(t, vector.AccountIDHex), decodeVectorHex(t, vector.BackupIDHex), vector.CreatedUnixMS, params)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Magic = decodeVectorHex(t, vector.MagicHex)
	metadata.FormatVersion = vector.FormatVersion
	rootKey := takeTestSecret(t, decodeVectorHex(t, vector.RootKeyHex))
	defer rootKey.Close()
	envelope, err := encryptBackup(rootKey, metadata, decodeVectorHex(t, vector.ArchiveHex), bytes.NewReader(decodeVectorHex(t, vector.NonceHex)))
	if err != nil {
		t.Fatal(err)
	}
	aad, err := CanonicalBackupAAD(envelope.Header)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aad, decodeVectorHex(t, vector.AADHex)) || !bytes.Equal(envelope.Header.Nonce, decodeVectorHex(t, vector.NonceHex)) || !bytes.Equal(envelope.Ciphertext, decodeVectorHex(t, vector.CiphertextHex)) {
		t.Fatalf("backup vector mismatch: AAD=%x nonce=%x ciphertext=%x", aad, envelope.Header.Nonce, envelope.Ciphertext)
	}
}

type backupCompatibilityVector struct {
	FixtureFormat int    `json:"fixture_format"`
	CryptoProfile string `json:"crypto_profile"`
	MagicHex      string `json:"magic_hex"`
	FormatVersion uint32 `json:"format_version"`
	AccountIDHex  string `json:"account_id_hex"`
	BackupIDHex   string `json:"backup_id_hex"`
	CreatedUnixMS uint64 `json:"created_unix_ms"`
	RootKeyHex    string `json:"root_key_hex"`
	ArchiveHex    string `json:"archive_hex"`
	AADHex        string `json:"aad_hex"`
	NonceHex      string `json:"nonce_hex"`
	CiphertextHex string `json:"ciphertext_hex"`
	KDF           struct {
		Algorithm       string `json:"algorithm"`
		SaltHex         string `json:"salt_hex"`
		MemoryKiB       uint32 `json:"memory_kib"`
		TimeCost        uint32 `json:"time_cost"`
		Parallelism     uint32 `json:"parallelism"`
		DerivedKeyBytes uint32 `json:"derived_key_bytes"`
	} `json:"kdf"`
}
