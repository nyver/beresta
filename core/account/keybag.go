package account

import (
	"encoding/binary"
	"errors"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

// keybagPayloadMagic and keybagPayloadVersion identify this package's
// private on-disk encoding for decrypted keybag content. This is not the
// wire or AAD canonical CBOR profile from docs/crypto-spec.md: the keybag
// plaintext never leaves the device and is authenticated only by the outer
// XChaCha20-Poly1305 keybag envelope, so its internal encoding is free to
// evolve independently of the synchronization wire format.
const (
	keybagPayloadMagic   = "BRSTKBG1"
	keybagPayloadVersion = 1
	maxWorkspaceKeys     = 4096
	workspaceKeyIDBytes  = 16
	workspaceKeyBytes    = 32
)

// ErrInvalidKeybagPayload reports a malformed decrypted keybag payload. This
// can only occur from local storage corruption, since the payload is
// authenticated before this package ever decodes it.
var ErrInvalidKeybagPayload = errors.New("account: invalid keybag payload")

// workspaceKeyState mirrors the store schema's workspace_keys.state column.
type workspaceKeyState uint8

const (
	workspaceKeyStateCurrent workspaceKeyState = iota + 1
	workspaceKeyStateHistorical
	workspaceKeyStateRetired
)

func (s workspaceKeyState) valid() bool {
	return s == workspaceKeyStateCurrent || s == workspaceKeyStateHistorical || s == workspaceKeyStateRetired
}

// keybagWorkspaceKey is one workspace key record held in the keybag.
type keybagWorkspaceKey struct {
	WorkspaceID model.ID
	KeyID       []byte // 16 opaque random bytes, not a UUID
	Key         *corecrypto.Secret
	State       workspaceKeyState
}

// keybagPlaintext is the complete decrypted keybag content: the account's
// X25519 identity and Ed25519 authority key pairs, plus every workspace key
// record the account currently holds.
type keybagPlaintext struct {
	IdentityPublicKey   []byte
	IdentityPrivateKey  *corecrypto.Secret
	AuthorityPublicKey  []byte
	AuthorityPrivateKey *corecrypto.Secret
	WorkspaceKeys       []keybagWorkspaceKey
}

// close wipes every secret held by the payload. It is safe to call more
// than once and on a zero value.
func (k *keybagPlaintext) close() {
	if k == nil {
		return
	}
	k.IdentityPrivateKey.Close()
	k.AuthorityPrivateKey.Close()
	for i := range k.WorkspaceKeys {
		k.WorkspaceKeys[i].Key.Close()
	}
}

// encodeKeybagPlaintext serializes payload into the owned Secret that
// becomes the input to corecrypto.EncryptKeybag. It does not close or
// otherwise consume payload's secrets.
func encodeKeybagPlaintext(payload keybagPlaintext) (*corecrypto.Secret, error) {
	if len(payload.WorkspaceKeys) > maxWorkspaceKeys {
		return nil, ErrInvalidKeybagPayload
	}

	buffer := make([]byte, 0, 512)
	buffer = append(buffer, keybagPayloadMagic...)
	buffer = append(buffer, keybagPayloadVersion)
	buffer = appendLP(buffer, payload.IdentityPublicKey)
	buffer = appendLP(buffer, payload.AuthorityPublicKey)

	var err error
	buffer, err = appendSecretLP(buffer, payload.IdentityPrivateKey)
	if err != nil {
		clear(buffer)
		return nil, err
	}
	buffer, err = appendSecretLP(buffer, payload.AuthorityPrivateKey)
	if err != nil {
		clear(buffer)
		return nil, err
	}

	buffer = appendU32(buffer, uint32(len(payload.WorkspaceKeys)))
	for _, wk := range payload.WorkspaceKeys {
		if len(wk.KeyID) != workspaceKeyIDBytes || !wk.State.valid() {
			clear(buffer)
			return nil, ErrInvalidKeybagPayload
		}
		buffer = appendLP(buffer, wk.WorkspaceID.Bytes())
		buffer = appendLP(buffer, wk.KeyID)
		buffer, err = appendSecretLP(buffer, wk.Key)
		if err != nil {
			clear(buffer)
			return nil, err
		}
		buffer = append(buffer, byte(wk.State))
	}

	secret, err := corecrypto.TakeSecret(buffer)
	if err != nil {
		clear(buffer)
		return nil, err
	}
	return secret, nil
}

// decodeKeybagPlaintext parses a Secret previously produced by
// encodeKeybagPlaintext, or opened by corecrypto.OpenKeybag /
// corecrypto.UnlockKeybag from an on-disk envelope. On any error it wipes
// every secret it had already extracted.
func decodeKeybagPlaintext(secret *corecrypto.Secret) (keybagPlaintext, error) {
	if secret == nil {
		return keybagPlaintext{}, corecrypto.ErrSecretClosed
	}

	var result keybagPlaintext
	err := secret.Use(func(data []byte) error {
		if len(data) < len(keybagPayloadMagic)+1 || string(data[:len(keybagPayloadMagic)]) != keybagPayloadMagic {
			return ErrInvalidKeybagPayload
		}
		data = data[len(keybagPayloadMagic):]
		if data[0] != keybagPayloadVersion {
			return ErrInvalidKeybagPayload
		}
		data = data[1:]

		var identityPub, authorityPub, identityPriv, authorityPriv []byte
		var err error
		if identityPub, data, err = readLP(data); err != nil {
			return err
		}
		if authorityPub, data, err = readLP(data); err != nil {
			return err
		}
		if identityPriv, data, err = readLP(data); err != nil {
			return err
		}
		if authorityPriv, data, err = readLP(data); err != nil {
			return err
		}
		if len(identityPub) != corecrypto.X25519PublicKeyBytes || len(identityPriv) != corecrypto.X25519PrivateKeyBytes {
			return ErrInvalidKeybagPayload
		}
		if len(authorityPub) != corecrypto.Ed25519PublicKeyBytes || len(authorityPriv) != corecrypto.Ed25519PrivateKeyBytes {
			return ErrInvalidKeybagPayload
		}

		result.IdentityPublicKey = append([]byte(nil), identityPub...)
		result.AuthorityPublicKey = append([]byte(nil), authorityPub...)
		if result.IdentityPrivateKey, err = corecrypto.TakeSecret(append([]byte(nil), identityPriv...)); err != nil {
			return err
		}
		if result.AuthorityPrivateKey, err = corecrypto.TakeSecret(append([]byte(nil), authorityPriv...)); err != nil {
			return err
		}

		var count uint32
		if count, data, err = readU32(data); err != nil {
			return err
		}
		if count > maxWorkspaceKeys {
			return ErrInvalidKeybagPayload
		}

		result.WorkspaceKeys = make([]keybagWorkspaceKey, 0, count)
		for i := uint32(0); i < count; i++ {
			var workspaceIDBytes, keyID, keyBytes []byte
			if workspaceIDBytes, data, err = readLP(data); err != nil {
				return err
			}
			if keyID, data, err = readLP(data); err != nil {
				return err
			}
			if keyBytes, data, err = readLP(data); err != nil {
				return err
			}
			if len(data) < 1 {
				return ErrInvalidKeybagPayload
			}
			state := workspaceKeyState(data[0])
			data = data[1:]

			workspaceID, err := model.ParseID(workspaceIDBytes)
			if err != nil {
				return err
			}
			if len(keyID) != workspaceKeyIDBytes || len(keyBytes) != workspaceKeyBytes || !state.valid() {
				return ErrInvalidKeybagPayload
			}
			keySecret, err := corecrypto.TakeSecret(append([]byte(nil), keyBytes...))
			if err != nil {
				return err
			}
			result.WorkspaceKeys = append(result.WorkspaceKeys, keybagWorkspaceKey{
				WorkspaceID: workspaceID,
				KeyID:       append([]byte(nil), keyID...),
				Key:         keySecret,
				State:       state,
			})
		}
		if len(data) != 0 {
			return ErrInvalidKeybagPayload
		}
		return nil
	})
	if err != nil {
		result.close()
		return keybagPlaintext{}, err
	}
	return result, nil
}

func appendSecretLP(dst []byte, secret *corecrypto.Secret) ([]byte, error) {
	if secret == nil {
		return nil, corecrypto.ErrSecretClosed
	}
	var out []byte
	err := secret.Use(func(value []byte) error {
		out = appendLP(dst, value)
		return nil
	})
	return out, err
}

func appendLP(dst, value []byte) []byte {
	dst = appendU32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendU32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func readLP(data []byte) (value, rest []byte, err error) {
	length, data, err := readU32(data)
	if err != nil {
		return nil, nil, err
	}
	if uint64(length) > uint64(len(data)) {
		return nil, nil, ErrInvalidKeybagPayload
	}
	return data[:length], data[length:], nil
}

func readU32(data []byte) (uint32, []byte, error) {
	if len(data) < 4 {
		return 0, nil, ErrInvalidKeybagPayload
	}
	return binary.BigEndian.Uint32(data[:4]), data[4:], nil
}
