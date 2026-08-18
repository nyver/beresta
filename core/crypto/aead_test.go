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

func TestKeybagEncryptionRoundTripAndUniformUnlockError(t *testing.T) {
	params := testArgon2idParams()
	header, err := NewKeybagHeader(bytes.Repeat([]byte{0x11}, AccountIDBytes), 7, params)
	if err != nil {
		t.Fatal(err)
	}
	rootKey := takeTestSecret(t, bytes.Repeat([]byte{0x42}, RootKeyBytes))
	defer rootKey.Close()
	plaintextBytes := []byte("canonical keybag plaintext")
	plaintext := takeTestSecret(t, plaintextBytes)
	defer plaintext.Close()

	envelope, err := encryptKeybag(rootKey, header, plaintext, bytes.NewReader(sequentialBytes(81, XChaCha20NonceBytes)))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenKeybag(rootKey, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if got := copySecret(t, opened); !bytes.Equal(got, plaintextBytes) {
		t.Fatalf("opened keybag = %x, want %x", got, plaintextBytes)
	}
	opened.Close()

	wrongRoot := takeTestSecret(t, bytes.Repeat([]byte{0x43}, RootKeyBytes))
	defer wrongRoot.Close()
	if opened, err = OpenKeybag(wrongRoot, envelope); !errors.Is(err, ErrKeybagUnlock) || opened != nil {
		t.Fatalf("wrong root result = %v, error = %v", opened, err)
	}
	tampered := cloneEncryptedKeybag(envelope)
	tampered.Ciphertext[len(tampered.Ciphertext)-1] ^= 1
	if opened, err = OpenKeybag(rootKey, tampered); !errors.Is(err, ErrKeybagUnlock) || opened != nil {
		t.Fatalf("tampered keybag result = %v, error = %v", opened, err)
	}
	tamperedNonce := cloneEncryptedKeybag(envelope)
	tamperedNonce.Nonce[0] ^= 1
	if opened, err = OpenKeybag(rootKey, tamperedNonce); !errors.Is(err, ErrKeybagUnlock) || opened != nil {
		t.Fatalf("tampered nonce result = %v, error = %v", opened, err)
	}
	stale := cloneEncryptedKeybag(envelope)
	stale.Header.KeybagVersion++
	if opened, err = OpenKeybag(rootKey, stale); !errors.Is(err, ErrKeybagUnlock) || opened != nil {
		t.Fatalf("substituted version result = %v, error = %v", opened, err)
	}
	if !bytes.Equal(copySecret(t, plaintext), plaintextBytes) {
		t.Fatal("keybag encryption mutated caller plaintext")
	}
}

func TestUnlockKeybagDoesNotUsePasswordVerifier(t *testing.T) {
	params := testArgon2idParams()
	header, err := NewKeybagHeader(bytes.Repeat([]byte{0x21}, AccountIDBytes), 1, params)
	if err != nil {
		t.Fatal(err)
	}
	correctPassphrase := []byte("correct horse battery staple")
	rootKey, err := DeriveRootKey(context.Background(), correctPassphrase, params)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := takeTestSecret(t, []byte("keybag"))
	defer plaintext.Close()
	envelope, err := encryptKeybag(rootKey, header, plaintext, bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes)))
	rootKey.Close()
	if err != nil {
		t.Fatal(err)
	}

	opened, err := UnlockKeybag(context.Background(), correctPassphrase, envelope)
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
	if opened, err = UnlockKeybag(context.Background(), []byte("wrong passphrase"), envelope); !errors.Is(err, ErrKeybagUnlock) || opened != nil {
		t.Fatalf("wrong passphrase result = %v, error = %v", opened, err)
	}
}

func TestObjectEncryptionRejectsTamperingAndCrossObjectSubstitution(t *testing.T) {
	workspaceKey := takeTestSecret(t, bytes.Repeat([]byte{0x5a}, HKDFKeyBytes))
	defer workspaceKey.Close()
	metadata := testObjectMetadata()
	plaintextBytes := []byte("encrypted note revision")
	plaintext := takeTestSecret(t, plaintextBytes)
	defer plaintext.Close()
	envelope, err := encryptObject(workspaceKey, metadata, plaintext, bytes.NewReader(sequentialBytes(31, XChaCha20NonceBytes)))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenObject(workspaceKey, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if got := copySecret(t, opened); !bytes.Equal(got, plaintextBytes) {
		t.Fatalf("opened object = %x, want %x", got, plaintextBytes)
	}
	opened.Close()

	substitutions := []struct {
		name   string
		mutate func(*EncryptedObject)
	}{
		{name: "workspace", mutate: func(candidate *EncryptedObject) { candidate.Metadata.WorkspaceID[0] ^= 1 }},
		{name: "object", mutate: func(candidate *EncryptedObject) { candidate.Metadata.ObjectID[0] ^= 1 }},
		{name: "type", mutate: func(candidate *EncryptedObject) { candidate.Metadata.ObjectType = ObjectTypeNoteSnapshot }},
		{name: "key ID", mutate: func(candidate *EncryptedObject) { candidate.Metadata.KeyID[0] ^= 1 }},
		{name: "nonce", mutate: func(candidate *EncryptedObject) { candidate.Nonce[0] ^= 1 }},
		{name: "ciphertext", mutate: func(candidate *EncryptedObject) { candidate.Ciphertext[0] ^= 1 }},
	}
	for _, test := range substitutions {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneEncryptedObject(envelope)
			test.mutate(&candidate)
			opened, err := OpenObject(workspaceKey, candidate)
			if !errors.Is(err, ErrAuthentication) || opened != nil {
				t.Fatalf("substitution result = %v, error = %v", opened, err)
			}
		})
	}
}

func TestAEADValidationRandomnessAndOwnership(t *testing.T) {
	params := testArgon2idParams()
	header, err := NewKeybagHeader(bytes.Repeat([]byte{0x31}, AccountIDBytes), 1, params)
	if err != nil {
		t.Fatal(err)
	}
	rootKey := takeTestSecret(t, bytes.Repeat([]byte{0x32}, RootKeyBytes))
	defer rootKey.Close()
	plaintext := takeTestSecret(t, []byte("payload"))
	defer plaintext.Close()
	if envelope, err := encryptKeybag(rootKey, header, plaintext, failingIdentityReader{}); !errors.Is(err, ErrRandomSource) || envelope.Ciphertext != nil {
		t.Fatalf("random failure envelope = %+v, error = %v", envelope, err)
	}

	future := header
	future.CryptoProfile = "beresta.crypto.v2"
	if envelope, err := EncryptKeybag(rootKey, future, plaintext); !errors.Is(err, ErrUnsupportedCryptoProfile) || envelope.Ciphertext != nil {
		t.Fatalf("future profile envelope = %+v, error = %v", envelope, err)
	}
	if rootKey.Len() != RootKeyBytes {
		t.Fatal("metadata rejection used or wiped the root key")
	}
	futureKDF := header
	futureKDF.KDF = futureKDF.KDF.Clone()
	futureKDF.KDF.CryptoProfile = "beresta.crypto.v2"
	if envelope, err := EncryptKeybag(rootKey, futureKDF, plaintext); !errors.Is(err, ErrUnsupportedCryptoProfile) || envelope.Ciphertext != nil {
		t.Fatalf("future KDF profile envelope = %+v, error = %v", envelope, err)
	}

	randomStream := bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes*2))
	first, err := encryptKeybag(rootKey, header, plaintext, randomStream)
	if err != nil {
		t.Fatal(err)
	}
	second, err := encryptKeybag(rootKey, header, plaintext, randomStream)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("successive encryptions reused a nonce")
	}

	metadata := testObjectMetadata()
	envelope, err := encryptObject(rootKey, metadata, plaintext, bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes)))
	if err != nil {
		t.Fatal(err)
	}
	metadata.ObjectID[0] ^= 0xff
	metadata.WorkspaceID[0] ^= 0xff
	metadata.KeyID[0] ^= 0xff
	if bytes.Equal(envelope.Metadata.ObjectID, metadata.ObjectID) || bytes.Equal(envelope.Metadata.WorkspaceID, metadata.WorkspaceID) || bytes.Equal(envelope.Metadata.KeyID, metadata.KeyID) {
		t.Fatal("encrypted object retained caller-owned metadata buffers")
	}
}

func TestCanonicalAADVectorValues(t *testing.T) {
	var vector aeadCompatibilityVector
	fixturePath := filepath.Join("..", "..", "schema", "testdata", "v1", "crypto-aead.json")
	encoded, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &vector); err != nil {
		t.Fatal(err)
	}
	if vector.FixtureFormat != 1 || vector.CryptoProfile != CryptoProfileV1 {
		t.Fatalf("unsupported AEAD fixture header: format=%d profile=%q", vector.FixtureFormat, vector.CryptoProfile)
	}
	params := Argon2idParams{
		CryptoProfile:   vector.CryptoProfile,
		Algorithm:       vector.Keybag.KDF.Algorithm,
		Salt:            decodeVectorHex(t, vector.Keybag.KDF.SaltHex),
		MemoryKiB:       vector.Keybag.KDF.MemoryKiB,
		TimeCost:        vector.Keybag.KDF.TimeCost,
		Parallelism:     vector.Keybag.KDF.Parallelism,
		DerivedKeyBytes: vector.Keybag.KDF.DerivedKeyBytes,
	}
	header, err := NewKeybagHeader(decodeVectorHex(t, vector.Keybag.AccountIDHex), vector.Keybag.KeybagVersion, params)
	if err != nil {
		t.Fatal(err)
	}
	header.Magic = decodeVectorHex(t, vector.Keybag.MagicHex)
	header.FormatVersion = vector.Keybag.FormatVersion
	keybagAAD, err := CanonicalKeybagAAD(header)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keybagAAD, decodeVectorHex(t, vector.Keybag.AADHex)) {
		t.Fatalf("keybag AAD = %x", keybagAAD)
	}
	metadata := ObjectMetadata{
		SchemaVersion: vector.Object.SchemaVersion,
		CryptoProfile: vector.CryptoProfile,
		WorkspaceID:   decodeVectorHex(t, vector.Object.WorkspaceIDHex),
		ObjectID:      decodeVectorHex(t, vector.Object.ObjectIDHex),
		ObjectType:    ObjectType(vector.Object.ObjectType),
		KeyID:         decodeVectorHex(t, vector.Object.KeyIDHex),
	}
	objectAAD, err := CanonicalObjectAAD(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(objectAAD, decodeVectorHex(t, vector.Object.AADHex)) {
		t.Fatalf("object AAD = %x", objectAAD)
	}

	rootKey := takeTestSecret(t, decodeVectorHex(t, vector.Keybag.RootKeyHex))
	defer rootKey.Close()
	keybagPlaintext := takeTestSecret(t, decodeVectorHex(t, vector.Keybag.PlaintextHex))
	defer keybagPlaintext.Close()
	keybagEnvelope, err := encryptKeybag(rootKey, header, keybagPlaintext, bytes.NewReader(decodeVectorHex(t, vector.Keybag.NonceHex)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keybagEnvelope.Nonce, decodeVectorHex(t, vector.Keybag.NonceHex)) || !bytes.Equal(keybagEnvelope.Ciphertext, decodeVectorHex(t, vector.Keybag.CiphertextHex)) {
		t.Fatalf("keybag nonce/ciphertext = %x/%x", keybagEnvelope.Nonce, keybagEnvelope.Ciphertext)
	}
	workspaceKey := takeTestSecret(t, decodeVectorHex(t, vector.Object.WorkspaceKeyHex))
	defer workspaceKey.Close()
	objectPlaintext := takeTestSecret(t, decodeVectorHex(t, vector.Object.PlaintextHex))
	defer objectPlaintext.Close()
	objectEnvelope, err := encryptObject(workspaceKey, metadata, objectPlaintext, bytes.NewReader(decodeVectorHex(t, vector.Object.NonceHex)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(objectEnvelope.Nonce, decodeVectorHex(t, vector.Object.NonceHex)) || !bytes.Equal(objectEnvelope.Ciphertext, decodeVectorHex(t, vector.Object.CiphertextHex)) {
		t.Fatalf("object nonce/ciphertext = %x/%x", objectEnvelope.Nonce, objectEnvelope.Ciphertext)
	}
}

type aeadCompatibilityVector struct {
	FixtureFormat int    `json:"fixture_format"`
	CryptoProfile string `json:"crypto_profile"`
	Keybag        struct {
		MagicHex      string `json:"magic_hex"`
		FormatVersion uint32 `json:"format_version"`
		AccountIDHex  string `json:"account_id_hex"`
		KeybagVersion uint64 `json:"keybag_version"`
		RootKeyHex    string `json:"root_key_hex"`
		PlaintextHex  string `json:"plaintext_hex"`
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
	} `json:"keybag"`
	Object struct {
		SchemaVersion   uint32 `json:"schema_version"`
		WorkspaceIDHex  string `json:"workspace_id_hex"`
		ObjectIDHex     string `json:"object_id_hex"`
		ObjectType      string `json:"object_type"`
		KeyIDHex        string `json:"key_id_hex"`
		WorkspaceKeyHex string `json:"workspace_key_hex"`
		PlaintextHex    string `json:"plaintext_hex"`
		AADHex          string `json:"aad_hex"`
		NonceHex        string `json:"nonce_hex"`
		CiphertextHex   string `json:"ciphertext_hex"`
	} `json:"object"`
}

func testObjectMetadata() ObjectMetadata {
	return ObjectMetadata{
		SchemaVersion: SchemaVersionV1,
		CryptoProfile: CryptoProfileV1,
		WorkspaceID:   bytes.Repeat([]byte{0x22}, WorkspaceIDBytes),
		ObjectID:      bytes.Repeat([]byte{0x33}, ObjectIDBytes),
		ObjectType:    ObjectTypeRevision,
		KeyID:         bytes.Repeat([]byte{0x44}, KeyIDBytes),
	}
}

func cloneEncryptedKeybag(envelope EncryptedKeybag) EncryptedKeybag {
	envelope.Header = cloneKeybagHeader(envelope.Header)
	envelope.Nonce = append([]byte(nil), envelope.Nonce...)
	envelope.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	return envelope
}

func cloneEncryptedObject(envelope EncryptedObject) EncryptedObject {
	envelope.Metadata = cloneObjectMetadata(envelope.Metadata)
	envelope.Nonce = append([]byte(nil), envelope.Nonce...)
	envelope.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	return envelope
}
