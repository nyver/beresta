package store

import (
	"bytes"
	"context"
	"errors"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
)

type fakeWrapper struct {
	protection keystore.Protection
	failWrap   error
	failUnwrap error
}

func (f *fakeWrapper) Protection() keystore.Protection { return f.protection }

func (f *fakeWrapper) Wrap(_ context.Context, metadata keystore.Metadata, secret *corecrypto.Secret) ([]byte, error) {
	if f.failWrap != nil {
		return nil, f.failWrap
	}
	var plaintext []byte
	if err := secret.Use(func(b []byte) error {
		plaintext = append([]byte(nil), b...)
		return nil
	}); err != nil {
		return nil, err
	}
	defer clear(plaintext)
	return keystore.SealEnvelope(f.protection, metadata, plaintext)
}

func (f *fakeWrapper) Unwrap(_ context.Context, metadata keystore.Metadata, encoded []byte) (*corecrypto.Secret, error) {
	if f.failUnwrap != nil {
		return nil, f.failUnwrap
	}
	plaintext, err := keystore.OpenEnvelope(encoded, f.protection, metadata)
	if err != nil {
		return nil, err
	}
	return corecrypto.TakeSecret(plaintext)
}

func (f *fakeWrapper) Delete(context.Context, keystore.Metadata) error { return nil }

var _ keystore.Wrapper = (*fakeWrapper)(nil)

func secretBytes(t *testing.T, secret *corecrypto.Secret) []byte {
	t.Helper()
	var out []byte
	if err := secret.Use(func(b []byte) error {
		out = append([]byte(nil), b...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLoadOrCreateDatabaseKeyGeneratesAndWraps(t *testing.T) {
	wrapper := &fakeWrapper{protection: keystore.ProtectionWindowsDPAPI}
	key, envelope, err := LoadOrCreateDatabaseKey(context.Background(), wrapper, "device-one", nil)
	if err != nil {
		t.Fatalf("LoadOrCreateDatabaseKey() error = %v", err)
	}
	defer key.Close()
	if key.Len() != databaseKeyBytes {
		t.Fatalf("key length = %d, want %d", key.Len(), databaseKeyBytes)
	}
	if len(envelope) == 0 {
		t.Fatal("expected a non-empty wrapped envelope")
	}
}

func TestLoadOrCreateDatabaseKeyUnwrapsExisting(t *testing.T) {
	wrapper := &fakeWrapper{protection: keystore.ProtectionWindowsDPAPI}
	ctx := context.Background()

	original, envelope, err := LoadOrCreateDatabaseKey(ctx, wrapper, "device-two", nil)
	if err != nil {
		t.Fatalf("create: LoadOrCreateDatabaseKey() error = %v", err)
	}
	originalBytes := secretBytes(t, original)
	original.Close()

	restored, restoredEnvelope, err := LoadOrCreateDatabaseKey(ctx, wrapper, "device-two", envelope)
	if err != nil {
		t.Fatalf("restore: LoadOrCreateDatabaseKey() error = %v", err)
	}
	defer restored.Close()
	if !bytes.Equal(secretBytes(t, restored), originalBytes) {
		t.Fatal("restored key bytes differ from the originally generated key")
	}
	if !bytes.Equal(restoredEnvelope, envelope) {
		t.Fatal("restore must return the unchanged input envelope")
	}
}

func TestLoadOrCreateDatabaseKeyRejectsNilWrapper(t *testing.T) {
	_, _, err := LoadOrCreateDatabaseKey(context.Background(), nil, "device-three", nil)
	if !errors.Is(err, keystore.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestLoadOrCreateDatabaseKeyRejectsInvalidDeviceID(t *testing.T) {
	wrapper := &fakeWrapper{protection: keystore.ProtectionWindowsDPAPI}
	_, _, err := LoadOrCreateDatabaseKey(context.Background(), wrapper, "", nil)
	if !errors.Is(err, keystore.ErrInvalidMetadata) {
		t.Fatalf("error = %v, want ErrInvalidMetadata", err)
	}
}

func TestLoadOrCreateDatabaseKeyPropagatesWrapFailure(t *testing.T) {
	wrapper := &fakeWrapper{protection: keystore.ProtectionWindowsDPAPI, failWrap: keystore.ErrUnavailable}
	_, _, err := LoadOrCreateDatabaseKey(context.Background(), wrapper, "device-four", nil)
	if !errors.Is(err, keystore.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestLoadOrCreateDatabaseKeyRejectsCrossDeviceEnvelope(t *testing.T) {
	wrapper := &fakeWrapper{protection: keystore.ProtectionWindowsDPAPI}
	ctx := context.Background()
	_, envelope, err := LoadOrCreateDatabaseKey(ctx, wrapper, "device-five", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrCreateDatabaseKey(ctx, wrapper, "device-six", envelope); err == nil {
		t.Fatal("expected an error unwrapping another device's envelope")
	}
}
