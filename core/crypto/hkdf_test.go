package crypto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"golang.org/x/crypto/hkdf"
)

func TestHKDFInfoUsesCanonicalLengthPrefixes(t *testing.T) {
	info := buildHKDFInfo(CryptoProfileV1, HKDFDomainKeybag, []byte{0xaa, 0xbb})
	want, err := hex.DecodeString(
		"00000011626572657374612e63727970746f2e7631" +
			"000000066b6579626167" +
			"00000002aabb",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(info, want) {
		t.Fatalf("info = %x, want %x", info, want)
	}

	boundary := buildHKDFInfo(CryptoProfileV1, HKDFDomainKeybag, make([]byte, 256))
	if !bytes.Contains(boundary, []byte{0, 0, 1, 0}) {
		t.Fatal("256-byte context did not use a four-byte big-endian length")
	}
}

func TestHKDFDerivationsMatchReferenceAndSeparateDomains(t *testing.T) {
	keyBytes := bytes.Repeat([]byte{0x5a}, HKDFKeyBytes)
	accountID := bytes.Repeat([]byte{0x11}, AccountIDBytes)
	workspaceID := bytes.Repeat([]byte{0x22}, WorkspaceIDBytes)
	objectID := bytes.Repeat([]byte{0x33}, ObjectIDBytes)
	blobID := bytes.Repeat([]byte{0x44}, BlobIDBytes)
	backupID := bytes.Repeat([]byte{0x55}, BackupIDBytes)
	sessionID := bytes.Repeat([]byte{0x66}, SessionIDBytes)
	transcript := bytes.Repeat([]byte{0x77}, TranscriptBytes)

	tests := []struct {
		name   string
		domain HKDFDomain
		parts  [][]byte
		derive func(*Secret) (*Secret, error)
	}{
		{name: "keybag", domain: HKDFDomainKeybag, parts: [][]byte{accountID}, derive: func(key *Secret) (*Secret, error) { return DeriveKeybagKey(CryptoProfileV1, key, accountID) }},
		{name: "workspace object", domain: HKDFDomainWorkspaceObject, parts: [][]byte{objectID, []byte(ObjectTypeRevision), {0, 0, 0, 1}}, derive: func(key *Secret) (*Secret, error) {
			return DeriveWorkspaceObjectKey(CryptoProfileV1, key, objectID, ObjectTypeRevision, SchemaVersionV1)
		}},
		{name: "blob id", domain: HKDFDomainBlobID, parts: [][]byte{workspaceID}, derive: func(key *Secret) (*Secret, error) { return DeriveBlobIDKey(CryptoProfileV1, key, workspaceID) }},
		{name: "blob manifest", domain: HKDFDomainBlobManifest, parts: [][]byte{blobID}, derive: func(key *Secret) (*Secret, error) { return DeriveBlobManifestKey(CryptoProfileV1, key, blobID) }},
		{name: "blob chunk", domain: HKDFDomainBlobChunk, parts: [][]byte{blobID, {0, 0, 0, 0, 0, 0, 0, 7}}, derive: func(key *Secret) (*Secret, error) { return DeriveBlobChunkKey(CryptoProfileV1, key, blobID, 7) }},
		{name: "backup", domain: HKDFDomainBackup, parts: [][]byte{accountID, backupID}, derive: func(key *Secret) (*Secret, error) { return DeriveBackupKey(CryptoProfileV1, key, accountID, backupID) }},
		{name: "pairing export", domain: HKDFDomainPairingExport, parts: [][]byte{sessionID, transcript}, derive: func(key *Secret) (*Secret, error) {
			return DerivePairingExportKey(CryptoProfileV1, key, sessionID, transcript)
		}},
	}

	outputs := make(map[string]struct{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := takeTestSecret(t, keyBytes)
			defer input.Close()
			output, err := test.derive(input)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			got := copySecret(t, output)
			want := referenceHKDF(t, keyBytes, buildHKDFInfo(CryptoProfileV1, test.domain, test.parts...))
			if !bytes.Equal(got, want) {
				t.Fatalf("derived key = %x, want %x", got, want)
			}
			outputs[hex.EncodeToString(got)] = struct{}{}
			if !bytes.Equal(copySecret(t, input), keyBytes) {
				t.Fatal("derivation mutated the input key")
			}
		})
	}
	if len(outputs) != len(tests) {
		t.Fatalf("domain separation produced %d unique outputs for %d domains", len(outputs), len(tests))
	}
}

func TestHKDFRejectsFutureProfilesAndInvalidContexts(t *testing.T) {
	keyBytes := bytes.Repeat([]byte{0x21}, HKDFKeyBytes)
	accountID := bytes.Repeat([]byte{0x31}, AccountIDBytes)
	key := takeTestSecret(t, keyBytes)
	defer key.Close()

	if result, err := DeriveKeybagKey("beresta.crypto.v2", key, accountID); !errors.Is(err, ErrUnsupportedCryptoProfile) || result != nil {
		t.Fatalf("future profile result = %v, error = %v", result, err)
	}
	if !bytes.Equal(copySecret(t, key), keyBytes) {
		t.Fatal("profile validation wiped a valid input key")
	}

	invalidUTF8 := ObjectType(string([]byte{0xff}))
	tests := []func() (*Secret, error){
		func() (*Secret, error) { return DeriveKeybagKey(CryptoProfileV1, key, accountID[:15]) },
		func() (*Secret, error) {
			return DeriveWorkspaceObjectKey(CryptoProfileV1, key, make([]byte, ObjectIDBytes), "", SchemaVersionV1)
		},
		func() (*Secret, error) {
			return DeriveWorkspaceObjectKey(CryptoProfileV1, key, make([]byte, ObjectIDBytes), invalidUTF8, SchemaVersionV1)
		},
		func() (*Secret, error) {
			return DeriveWorkspaceObjectKey(CryptoProfileV1, key, make([]byte, ObjectIDBytes), ObjectType("unknown"), SchemaVersionV1)
		},
		func() (*Secret, error) {
			return DeriveWorkspaceObjectKey(CryptoProfileV1, key, make([]byte, ObjectIDBytes), ObjectTypeRevision, SchemaVersionV1+1)
		},
		func() (*Secret, error) {
			return DeriveBlobIDKey(CryptoProfileV1, key, make([]byte, WorkspaceIDBytes-1))
		},
		func() (*Secret, error) {
			return DeriveBlobManifestKey(CryptoProfileV1, key, make([]byte, BlobIDBytes-1))
		},
		func() (*Secret, error) {
			return DeriveBlobChunkKey(CryptoProfileV1, key, make([]byte, BlobIDBytes-1), 0)
		},
		func() (*Secret, error) {
			return DeriveBackupKey(CryptoProfileV1, key, make([]byte, AccountIDBytes), make([]byte, BackupIDBytes-1))
		},
		func() (*Secret, error) {
			return DerivePairingExportKey(CryptoProfileV1, key, make([]byte, SessionIDBytes), make([]byte, TranscriptBytes-1))
		},
	}
	for index, derive := range tests {
		if result, err := derive(); err == nil || result != nil {
			t.Fatalf("case %d result = %v, error = %v", index, result, err)
		}
	}
}

func TestHKDFRejectsClosedAndWrongSizedKeys(t *testing.T) {
	accountID := make([]byte, AccountIDBytes)
	closed := takeTestSecret(t, make([]byte, HKDFKeyBytes))
	closed.Close()
	if result, err := DeriveKeybagKey(CryptoProfileV1, closed, accountID); !errors.Is(err, ErrSecretClosed) || result != nil {
		t.Fatalf("closed result = %v, error = %v", result, err)
	}

	wrongSize := takeTestSecret(t, make([]byte, HKDFKeyBytes-1))
	if result, err := DeriveKeybagKey(CryptoProfileV1, wrongSize, accountID); !errors.Is(err, ErrInvalidDerivationKey) || result != nil {
		t.Fatalf("wrong-size result = %v, error = %v", result, err)
	}
	if wrongSize.Len() != 0 {
		t.Fatal("wrong-sized key was not wiped on the derivation error path")
	}
}

func referenceHKDF(t *testing.T, key, info []byte) []byte {
	t.Helper()
	result := make([]byte, HKDFKeyBytes)
	if _, err := io.ReadFull(hkdf.New(sha256.New, key, nil, info), result); err != nil {
		t.Fatal(err)
	}
	return result
}

func takeTestSecret(t *testing.T, value []byte) *Secret {
	t.Helper()
	secret, err := TakeSecret(append([]byte(nil), value...))
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func copySecret(t *testing.T, secret *Secret) []byte {
	t.Helper()
	var result []byte
	if err := secret.Use(func(value []byte) error {
		result = append([]byte(nil), value...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
