//go:build windows

package windowskeystore

import (
	"bytes"
	"context"
	"errors"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
)

func TestDPAPIRoundTripAndMetadataBinding(t *testing.T) {
	adapter := NewDPAPI()
	metadata := keystore.Metadata{KeyID: "test.database", Purpose: "database-key"}
	plaintext := bytes.Repeat([]byte{0x5a}, 32)
	secret, err := corecrypto.TakeSecret(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Close()

	encoded, err := adapter.Wrap(context.Background(), metadata, secret)
	if err != nil {
		t.Fatal(err)
	}
	unwrapped, err := adapter.Unwrap(context.Background(), metadata, encoded)
	if err != nil {
		t.Fatal(err)
	}
	defer unwrapped.Close()
	if err := unwrapped.Use(func(value []byte) error {
		if !bytes.Equal(value, bytes.Repeat([]byte{0x5a}, 32)) {
			t.Fatalf("unwrapped value = %x", value)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	wrong := keystore.Metadata{KeyID: "test.other", Purpose: metadata.Purpose}
	if _, err := adapter.Unwrap(context.Background(), wrong, encoded); !errors.Is(err, keystore.ErrInvalidEnvelope) {
		t.Fatalf("metadata substitution error = %v", err)
	}
}

func TestDPAPIRejectsTamperingAndCancellation(t *testing.T) {
	adapter := NewDPAPI()
	metadata := keystore.Metadata{KeyID: "test.tamper", Purpose: "database-key"}
	secret, err := corecrypto.TakeSecret(bytes.Repeat([]byte{0x2c}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Close()
	encoded, err := adapter.Wrap(context.Background(), metadata, secret)
	if err != nil {
		t.Fatal(err)
	}
	encoded[len(encoded)-1] ^= 0x80
	if _, err := adapter.Unwrap(context.Background(), metadata, encoded); !errors.Is(err, keystore.ErrAuthentication) {
		t.Fatalf("tampered unwrap error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Unwrap(ctx, metadata, encoded); !errors.Is(err, keystore.ErrCanceled) {
		t.Fatalf("canceled unwrap error = %v", err)
	}
}
