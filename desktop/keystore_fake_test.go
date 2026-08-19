package main

import (
	"context"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
)

// fakeWrapper is a deterministic in-memory keystore.Wrapper for desktop
// tests, mirroring core/account's test helper of the same shape: it
// exercises the real BKW1 envelope format without depending on a real
// Windows Hello/DPAPI round trip, which is slow (the Hello availability
// check alone can take tens of seconds) and, if Hello is enrolled on the
// machine running the tests, can trigger a real OS credential prompt.
type fakeWrapper struct{}

func (fakeWrapper) Protection() keystore.Protection { return keystore.ProtectionWindowsDPAPI }

func (fakeWrapper) Wrap(_ context.Context, metadata keystore.Metadata, secret *corecrypto.Secret) ([]byte, error) {
	var plaintext []byte
	if err := secret.Use(func(b []byte) error {
		plaintext = append([]byte(nil), b...)
		return nil
	}); err != nil {
		return nil, err
	}
	defer clear(plaintext)
	return keystore.SealEnvelope(keystore.ProtectionWindowsDPAPI, metadata, plaintext)
}

func (fakeWrapper) Unwrap(_ context.Context, metadata keystore.Metadata, encoded []byte) (*corecrypto.Secret, error) {
	plaintext, err := keystore.OpenEnvelope(encoded, keystore.ProtectionWindowsDPAPI, metadata)
	if err != nil {
		return nil, err
	}
	return corecrypto.TakeSecret(plaintext)
}

func (fakeWrapper) Delete(context.Context, keystore.Metadata) error { return nil }

var _ keystore.Wrapper = fakeWrapper{}

// fakeKeyWrapperFactory is the keyWrapperFactory every desktop test
// installs in place of newKeyWrapper.
func fakeKeyWrapperFactory(context.Context, string) (keystore.Wrapper, string, error) {
	return fakeWrapper{}, "fake", nil
}
