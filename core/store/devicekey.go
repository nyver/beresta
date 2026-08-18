package store

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
)

// DatabaseKeyPurpose is the fixed keystore purpose token bound into the
// wrapped per-device SQLCipher database key envelope.
const DatabaseKeyPurpose = "database-key"

const databaseKeyBytes = 32

// LoadOrCreateDatabaseKey unwraps a previously wrapped per-device SQLCipher
// database key from envelope, or generates and OS-keystore-wraps a fresh
// random key when envelope is empty. The database key is random and
// independent of any password-derived key; it never leaves the device and
// is recovered only through the local platform keystore.
//
// It returns the plaintext key as an owned Secret, which the caller must
// close, and the envelope to persist beside the database (unchanged from
// the input when one was supplied).
func LoadOrCreateDatabaseKey(ctx context.Context, wrapper keystore.Wrapper, deviceID string, envelope []byte) (*corecrypto.Secret, []byte, error) {
	if wrapper == nil {
		return nil, nil, keystore.ErrUnavailable
	}
	metadata := keystore.Metadata{KeyID: deviceID, Purpose: DatabaseKeyPurpose}
	if err := metadata.Validate(); err != nil {
		return nil, nil, err
	}

	if len(envelope) > 0 {
		key, err := wrapper.Unwrap(ctx, metadata, envelope)
		if err != nil {
			return nil, nil, err
		}
		return key, envelope, nil
	}

	raw := make([]byte, databaseKeyBytes)
	if _, err := io.ReadFull(cryptorand.Reader, raw); err != nil {
		return nil, nil, fmt.Errorf("store: generate database key: %w", err)
	}
	key, err := corecrypto.TakeSecret(raw)
	if err != nil {
		return nil, nil, err
	}
	wrapped, err := wrapper.Wrap(ctx, metadata, key)
	if err != nil {
		key.Close()
		return nil, nil, err
	}
	return key, wrapped, nil
}
