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

type fakeVerifier struct {
	availableErr error
	verifyErr    error
	verifyCalls  int
}

func (f *fakeVerifier) Available(context.Context) error { return f.availableErr }
func (f *fakeVerifier) Verify(context.Context, uintptr, string) error {
	f.verifyCalls++
	return f.verifyErr
}

func TestHelloDPAPIGatesUnwrap(t *testing.T) {
	verifier := &fakeVerifier{}
	adapter, err := NewHelloDPAPI(verifier, func() uintptr { return 42 }, "Unlock Beresta")
	if err != nil {
		t.Fatal(err)
	}
	metadata := keystore.Metadata{KeyID: "hello.test", Purpose: "database-key"}
	secret, err := corecrypto.TakeSecret(bytes.Repeat([]byte{0x41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Close()
	encoded, err := adapter.Wrap(context.Background(), metadata, secret)
	if err != nil {
		t.Fatal(err)
	}

	verifier.verifyErr = keystore.ErrAuthentication
	if _, err := adapter.Unwrap(context.Background(), metadata, encoded); !errors.Is(err, keystore.ErrAuthentication) {
		t.Fatalf("denied unwrap error = %v", err)
	}
	if verifier.verifyCalls != 1 {
		t.Fatalf("Verify calls = %d, want 1", verifier.verifyCalls)
	}

	verifier.verifyErr = nil
	unwrapped, err := adapter.Unwrap(context.Background(), metadata, encoded)
	if err != nil {
		t.Fatal(err)
	}
	unwrapped.Close()
}

func TestHelloEnvelopeCannotDowngradeToDPAPI(t *testing.T) {
	verifier := &fakeVerifier{}
	adapter, err := NewHelloDPAPI(verifier, func() uintptr { return 42 }, "Unlock Beresta")
	if err != nil {
		t.Fatal(err)
	}
	metadata := keystore.Metadata{KeyID: "hello.downgrade", Purpose: "database-key"}
	secret, err := corecrypto.TakeSecret(bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer secret.Close()
	encoded, err := adapter.Wrap(context.Background(), metadata, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDPAPI().Unwrap(context.Background(), metadata, encoded); !errors.Is(err, keystore.ErrInvalidEnvelope) {
		t.Fatalf("fallback downgrade error = %v", err)
	}
}
