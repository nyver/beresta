package mobileapi

import (
	"context"
	"crypto/rand"
	"errors"
	"io"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"golang.org/x/crypto/chacha20poly1305"
)

// deviceWrapper is the Go half of Android Keystore protection. Android owns
// and unwraps a random 32-byte device secret; the shared core uses that secret
// only while unlocked to wrap its database and signing keys with metadata as
// authenticated data. The secret is never persisted by Go.
type deviceWrapper struct {
	key *corecrypto.Secret
}

func newDeviceWrapper(raw []byte) (*deviceWrapper, error) {
	if len(raw) != chacha20poly1305.KeySize {
		clear(raw)
		return nil, errors.New("mobileapi: device secret must be 32 bytes")
	}
	owned := append([]byte(nil), raw...)
	clear(raw)
	key, err := corecrypto.TakeSecret(owned)
	if err != nil {
		return nil, err
	}
	return &deviceWrapper{key: key}, nil
}

func (*deviceWrapper) Protection() keystore.Protection { return keystore.ProtectionAndroidKeystore }

func (w *deviceWrapper) Wrap(_ context.Context, metadata keystore.Metadata, plaintext *corecrypto.Secret) ([]byte, error) {
	binding, err := keystore.Binding(w.Protection(), metadata)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	var wrapped []byte
	err = w.key.Use(func(key []byte) error {
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return err
		}
		return plaintext.Use(func(value []byte) error {
			wrapped = aead.Seal(append([]byte(nil), nonce...), nonce, value, binding)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return keystore.SealEnvelope(w.Protection(), metadata, wrapped)
}

func (w *deviceWrapper) Unwrap(_ context.Context, metadata keystore.Metadata, envelope []byte) (*corecrypto.Secret, error) {
	binding, err := keystore.Binding(w.Protection(), metadata)
	if err != nil {
		return nil, err
	}
	wrapped, err := keystore.OpenEnvelope(envelope, w.Protection(), metadata)
	if err != nil || len(wrapped) < chacha20poly1305.NonceSizeX+chacha20poly1305.Overhead {
		return nil, keystore.ErrInvalidEnvelope
	}
	nonce := wrapped[:chacha20poly1305.NonceSizeX]
	var plaintext []byte
	err = w.key.Use(func(key []byte) error {
		aead, err := chacha20poly1305.NewX(key)
		if err != nil {
			return err
		}
		plaintext, err = aead.Open(nil, nonce, wrapped[chacha20poly1305.NonceSizeX:], binding)
		return err
	})
	if err != nil {
		clear(plaintext)
		return nil, keystore.ErrAuthentication
	}
	return corecrypto.TakeSecret(plaintext)
}

func (*deviceWrapper) Delete(context.Context, keystore.Metadata) error { return nil }

func (w *deviceWrapper) close() {
	if w != nil && w.key != nil {
		w.key.Close()
	}
}

var _ keystore.Wrapper = (*deviceWrapper)(nil)
