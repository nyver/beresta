package keystore

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnvelopeRoundTripAndBinding(t *testing.T) {
	metadata := Metadata{KeyID: "database.01", Purpose: "database-key"}
	want := []byte{1, 2, 3, 4}
	encoded, err := SealEnvelope(ProtectionWindowsDPAPI, metadata, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenEnvelope(encoded, ProtectionWindowsDPAPI, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("OpenEnvelope() = %x, want %x", got, want)
	}
	got[0] ^= 0xff
	if bytes.Equal(got, encoded[len(encoded)-len(want):]) {
		t.Fatal("OpenEnvelope returned an alias into the encoded envelope")
	}

	first, err := Binding(ProtectionWindowsDPAPI, metadata)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Binding(ProtectionWindowsDPAPI, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("Binding is not deterministic")
	}
}

func TestEnvelopeRejectsSubstitutionAndMalformedInput(t *testing.T) {
	metadata := Metadata{KeyID: "database.01", Purpose: "database-key"}
	encoded, err := SealEnvelope(ProtectionWindowsHello, metadata, []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		encoded    []byte
		protection Protection
		metadata   Metadata
	}{
		{name: "wrong protection", encoded: encoded, protection: ProtectionWindowsDPAPI, metadata: metadata},
		{name: "wrong key", encoded: encoded, protection: ProtectionWindowsHello, metadata: Metadata{KeyID: "database.02", Purpose: metadata.Purpose}},
		{name: "wrong purpose", encoded: encoded, protection: ProtectionWindowsHello, metadata: Metadata{KeyID: metadata.KeyID, Purpose: "other"}},
		{name: "truncated", encoded: encoded[:len(encoded)-1], protection: ProtectionWindowsHello, metadata: metadata},
		{name: "trailing", encoded: append(bytes.Clone(encoded), 0), protection: ProtectionWindowsHello, metadata: metadata},
		{name: "unknown version", encoded: mutate(encoded, 4, 2), protection: ProtectionWindowsHello, metadata: metadata},
		{name: "unknown protection", encoded: mutate(encoded, 5, 0xff), protection: ProtectionWindowsHello, metadata: metadata},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := OpenEnvelope(test.encoded, test.protection, test.metadata); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("OpenEnvelope() error = %v", err)
			}
		})
	}
}

func TestMetadataValidation(t *testing.T) {
	invalid := []Metadata{
		{},
		{KeyID: "contains space", Purpose: "database-key"},
		{KeyID: "database", Purpose: "../escape"},
		{KeyID: string(bytes.Repeat([]byte{'a'}, MaxIdentifier+1)), Purpose: "database-key"},
	}
	for _, metadata := range invalid {
		if err := metadata.Validate(); !errors.Is(err, ErrInvalidMetadata) {
			t.Fatalf("Validate(%q, %q) error = %v", metadata.KeyID, metadata.Purpose, err)
		}
	}
}

func TestEnvelopeCompatibilityVector(t *testing.T) {
	var vector struct {
		FixtureFormat  int    `json:"fixture_format"`
		Protection     string `json:"protection"`
		ProtectionCode uint8  `json:"protection_code"`
		KeyID          string `json:"key_id"`
		Purpose        string `json:"purpose"`
		WrappedHex     string `json:"wrapped_hex"`
		BindingHex     string `json:"binding_hex"`
		EnvelopeHex    string `json:"envelope_hex"`
	}
	encoded, err := os.ReadFile(filepath.Join("..", "..", "schema", "testdata", "v1", "keystore-envelope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &vector); err != nil {
		t.Fatal(err)
	}
	protection := Protection(vector.ProtectionCode)
	if vector.FixtureFormat != FormatVersion || protection.String() != vector.Protection {
		t.Fatalf("unsupported fixture header: format=%d protection=%q", vector.FixtureFormat, vector.Protection)
	}
	metadata := Metadata{KeyID: vector.KeyID, Purpose: vector.Purpose}
	wrapped := decodeHex(t, vector.WrappedHex)
	wantEnvelope := decodeHex(t, vector.EnvelopeHex)
	gotEnvelope, err := SealEnvelope(protection, metadata, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotEnvelope, wantEnvelope) {
		t.Fatalf("envelope = %x, want %x", gotEnvelope, wantEnvelope)
	}
	gotBinding, err := Binding(protection, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if want := decodeHex(t, vector.BindingHex); !bytes.Equal(gotBinding, want) {
		t.Fatalf("binding = %x, want %x", gotBinding, want)
	}
}

func FuzzOpenEnvelope(f *testing.F) {
	metadata := Metadata{KeyID: "database.01", Purpose: "database-key"}
	valid, err := SealEnvelope(ProtectionAndroidKeystore, metadata, []byte{1, 2, 3, 4})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte("malformed"))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > MaxWrappedBytes+headerBytes+2*MaxIdentifier {
			return
		}
		wrapped, err := OpenEnvelope(encoded, ProtectionAndroidKeystore, metadata)
		if err == nil && len(wrapped) == 0 {
			t.Fatal("successful parse returned empty platform ciphertext")
		}
	})
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mutate(value []byte, offset int, replacement byte) []byte {
	result := bytes.Clone(value)
	result[offset] = replacement
	return result
}
