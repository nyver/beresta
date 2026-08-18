package account

import (
	"bytes"
	"errors"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

func newTestSecret(t *testing.T, seed byte, length int) *corecrypto.Secret {
	t.Helper()
	raw := make([]byte, length)
	for i := range raw {
		raw[i] = seed
	}
	secret, err := corecrypto.TakeSecret(raw)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func testModelID(t *testing.T, seed byte) model.ID {
	t.Helper()
	var raw [16]byte
	for i := range raw {
		raw[i] = seed
	}
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	id, err := model.ParseID(raw[:])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func buildTestPayload(t *testing.T) keybagPlaintext {
	t.Helper()
	return keybagPlaintext{
		IdentityPublicKey:   bytes.Repeat([]byte{0x11}, corecrypto.X25519PublicKeyBytes),
		IdentityPrivateKey:  newTestSecret(t, 0x22, corecrypto.X25519PrivateKeyBytes),
		AuthorityPublicKey:  bytes.Repeat([]byte{0x33}, corecrypto.Ed25519PublicKeyBytes),
		AuthorityPrivateKey: newTestSecret(t, 0x44, corecrypto.Ed25519PrivateKeyBytes),
		WorkspaceKeys: []keybagWorkspaceKey{
			{
				WorkspaceID: testModelID(t, 0x55),
				KeyID:       bytes.Repeat([]byte{0x66}, workspaceKeyIDBytes),
				Key:         newTestSecret(t, 0x77, workspaceKeyBytes),
				State:       workspaceKeyStateCurrent,
			},
			{
				WorkspaceID: testModelID(t, 0x88),
				KeyID:       bytes.Repeat([]byte{0x99}, workspaceKeyIDBytes),
				Key:         newTestSecret(t, 0xaa, workspaceKeyBytes),
				State:       workspaceKeyStateHistorical,
			},
		},
	}
}

func secretContentEqual(t *testing.T, a, b *corecrypto.Secret) bool {
	t.Helper()
	var av, bv []byte
	if err := a.Use(func(v []byte) error { av = append([]byte(nil), v...); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := b.Use(func(v []byte) error { bv = append([]byte(nil), v...); return nil }); err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(av, bv)
}

func TestKeybagPlaintextRoundTrip(t *testing.T) {
	original := buildTestPayload(t)
	defer original.close()

	encoded, err := encodeKeybagPlaintext(original)
	if err != nil {
		t.Fatalf("encodeKeybagPlaintext() error = %v", err)
	}
	decoded, err := decodeKeybagPlaintext(encoded)
	encoded.Close()
	if err != nil {
		t.Fatalf("decodeKeybagPlaintext() error = %v", err)
	}
	defer decoded.close()

	if !bytes.Equal(decoded.IdentityPublicKey, original.IdentityPublicKey) {
		t.Fatal("identity public key mismatch")
	}
	if !bytes.Equal(decoded.AuthorityPublicKey, original.AuthorityPublicKey) {
		t.Fatal("authority public key mismatch")
	}
	if !secretContentEqual(t, decoded.IdentityPrivateKey, original.IdentityPrivateKey) {
		t.Fatal("identity private key mismatch")
	}
	if !secretContentEqual(t, decoded.AuthorityPrivateKey, original.AuthorityPrivateKey) {
		t.Fatal("authority private key mismatch")
	}
	if len(decoded.WorkspaceKeys) != len(original.WorkspaceKeys) {
		t.Fatalf("workspace key count = %d, want %d", len(decoded.WorkspaceKeys), len(original.WorkspaceKeys))
	}
	for i, want := range original.WorkspaceKeys {
		got := decoded.WorkspaceKeys[i]
		if got.WorkspaceID != want.WorkspaceID {
			t.Fatalf("workspace[%d] ID = %s, want %s", i, got.WorkspaceID, want.WorkspaceID)
		}
		if !bytes.Equal(got.KeyID, want.KeyID) {
			t.Fatalf("workspace[%d] key ID mismatch", i)
		}
		if got.State != want.State {
			t.Fatalf("workspace[%d] state = %d, want %d", i, got.State, want.State)
		}
		if !secretContentEqual(t, got.Key, want.Key) {
			t.Fatalf("workspace[%d] key mismatch", i)
		}
	}
}

func TestDecodeKeybagPlaintextRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"wrong magic", bytes.Repeat([]byte{0x00}, 32)},
		{"truncated after magic", []byte(keybagPayloadMagic)},
		{"wrong version", append([]byte(keybagPayloadMagic), 0x02)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := corecrypto.TakeSecret(append([]byte(nil), tt.data...))
			if err != nil {
				t.Fatal(err)
			}
			defer secret.Close()
			if _, err := decodeKeybagPlaintext(secret); !errors.Is(err, ErrInvalidKeybagPayload) {
				t.Fatalf("decodeKeybagPlaintext(%q) error = %v, want ErrInvalidKeybagPayload", tt.name, err)
			}
		})
	}
}

func TestDecodeKeybagPlaintextRejectsTruncatedWorkspaceKeys(t *testing.T) {
	original := buildTestPayload(t)
	defer original.close()
	encoded, err := encodeKeybagPlaintext(original)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := encoded.Use(func(v []byte) error { raw = append([]byte(nil), v...); return nil }); err != nil {
		t.Fatal(err)
	}
	encoded.Close()

	truncated, err := corecrypto.TakeSecret(raw[:len(raw)-1])
	if err != nil {
		t.Fatal(err)
	}
	defer truncated.Close()
	if _, err := decodeKeybagPlaintext(truncated); !errors.Is(err, ErrInvalidKeybagPayload) {
		t.Fatalf("decodeKeybagPlaintext(truncated) error = %v, want ErrInvalidKeybagPayload", err)
	}
}

func TestDecodeKeybagPlaintextRejectsNilSecret(t *testing.T) {
	if _, err := decodeKeybagPlaintext(nil); !errors.Is(err, corecrypto.ErrSecretClosed) {
		t.Fatalf("decodeKeybagPlaintext(nil) error = %v, want ErrSecretClosed", err)
	}
}
