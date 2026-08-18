package crypto

import (
	"bytes"
	"reflect"
	"testing"
)

func FuzzOpenObjectMalformed(f *testing.F) {
	metadata := testObjectMetadata()
	keyBytes := bytes.Repeat([]byte{0x51}, HKDFKeyBytes)
	key, err := TakeSecret(bytes.Clone(keyBytes))
	if err != nil {
		f.Fatal(err)
	}
	plaintext, err := TakeSecret([]byte("valid fuzz seed"))
	if err != nil {
		f.Fatal(err)
	}
	valid, err := encryptObject(key, metadata, plaintext, bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes)))
	key.Close()
	plaintext.Close()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Nonce, valid.Ciphertext)
	f.Add([]byte{1}, []byte{2})
	f.Fuzz(func(t *testing.T, nonce, ciphertext []byte) {
		if len(nonce) > 128 || len(ciphertext) > 1<<20 {
			return
		}
		key, err := TakeSecret(bytes.Clone(keyBytes))
		if err != nil {
			t.Fatal(err)
		}
		defer key.Close()
		opened, _ := OpenObject(key, EncryptedObject{
			Metadata:   cloneObjectMetadata(metadata),
			Nonce:      bytes.Clone(nonce),
			Ciphertext: bytes.Clone(ciphertext),
		})
		if opened != nil {
			opened.Close()
		}
	})
}

func FuzzOpenKeybagMalformed(f *testing.F) {
	params := testArgon2idParams()
	header, err := NewKeybagHeader(bytes.Repeat([]byte{0x11}, AccountIDBytes), 1, params)
	if err != nil {
		f.Fatal(err)
	}
	keyBytes := bytes.Repeat([]byte{0x42}, RootKeyBytes)
	key, err := TakeSecret(bytes.Clone(keyBytes))
	if err != nil {
		f.Fatal(err)
	}
	plaintext, err := TakeSecret([]byte("valid keybag fuzz seed"))
	if err != nil {
		f.Fatal(err)
	}
	valid, err := encryptKeybag(key, header, plaintext, bytes.NewReader(sequentialBytes(1, XChaCha20NonceBytes)))
	key.Close()
	plaintext.Close()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid.Nonce, valid.Ciphertext)
	f.Add([]byte{}, []byte{})
	f.Fuzz(func(t *testing.T, nonce, ciphertext []byte) {
		if len(nonce) > 128 || len(ciphertext) > 1<<20 {
			return
		}
		key, err := TakeSecret(bytes.Clone(keyBytes))
		if err != nil {
			t.Fatal(err)
		}
		defer key.Close()
		opened, _ := OpenKeybag(key, EncryptedKeybag{
			Header:     cloneKeybagHeader(header),
			Nonce:      bytes.Clone(nonce),
			Ciphertext: bytes.Clone(ciphertext),
		})
		if opened != nil {
			opened.Close()
		}
	})
}

func FuzzOpenWorkspaceKeyEnvelopeMalformed(f *testing.F) {
	publicKey, private, err := generateX25519Identity(bytes.NewReader(sequentialBytes(1, X25519PrivateKeyBytes)))
	if err != nil {
		f.Fatal(err)
	}
	privateBytes := copySecretForFuzz(f, private)
	private.Close()
	f.Add([]byte("malformed"))
	f.Add(make([]byte, 49))
	f.Fuzz(func(t *testing.T, sealed []byte) {
		if len(sealed) > MaxWorkspaceKeyEnvelopePlaintextBytes+64 {
			return
		}
		private, err := TakeSecret(bytes.Clone(privateBytes))
		if err != nil {
			t.Fatal(err)
		}
		defer private.Close()
		opened, _ := OpenWorkspaceKeyEnvelope(CryptoProfileV1, publicKey, private, bytes.Clone(sealed))
		if opened != nil {
			opened.Close()
		}
	})
}

func TestProductionEncryptionAPIsDoNotAcceptCallerNonces(t *testing.T) {
	tests := []struct {
		name   string
		fn     any
		inputs int
	}{
		{name: "keybag", fn: EncryptKeybag, inputs: 3},
		{name: "object", fn: EncryptObject, inputs: 3},
		{name: "backup", fn: EncryptBackup, inputs: 3},
		{name: "manifest", fn: EncryptManifest, inputs: 3},
		{name: "sealed box", fn: SealWorkspaceKeyEnvelope, inputs: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := reflect.TypeOf(test.fn).NumIn(); got != test.inputs {
				t.Fatalf("public API has %d inputs, want %d; caller-supplied nonce may have leaked into the API", got, test.inputs)
			}
		})
	}
}

func copySecretForFuzz(f *testing.F, secret *Secret) []byte {
	f.Helper()
	var result []byte
	if err := secret.Use(func(value []byte) error {
		result = bytes.Clone(value)
		return nil
	}); err != nil {
		f.Fatal(err)
	}
	return result
}
