package account

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

type ServerRegistration struct {
	UserID            model.ID
	IdentityPublic    []byte
	AuthorityPublic   []byte
	DeviceID          model.ID
	SigningPublic     []byte
	WorkspaceID       model.ID
	WorkspaceKeyID    []byte
	WorkspaceEnvelope []byte
	KeybagCiphertext  []byte
}

// SignDeviceChallenge signs an already domain-separated server challenge.
// Only the signature crosses the caller boundary; private key bytes do not.
func (a *Account) SignDeviceChallenge(message []byte) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return nil, ErrAccountLocked
	}
	if len(message) == 0 || len(message) > 16<<10 {
		return nil, errors.New("account: invalid challenge input")
	}
	var signature []byte
	err := a.devicePrivate.Use(func(private []byte) error {
		if len(private) != ed25519.PrivateKeySize {
			return errors.New("account: invalid device signing key")
		}
		signature = ed25519.Sign(ed25519.PrivateKey(private), message)
		return nil
	})
	return signature, err
}

// ServerRegistrationData builds the opaque account bootstrap fields used by
// invite-only enrollment. It performs no network access.
func (a *Account) ServerRegistrationData(ctx context.Context, workspaceID model.ID) (ServerRegistration, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return ServerRegistration{}, ErrAccountLocked
	}
	db := a.db
	entry, ok := a.workspaceKeys[workspaceID]
	identityPublic := append([]byte(nil), a.IdentityPublicKey...)
	authorityPublic := append([]byte(nil), a.AuthorityPublicKey...)
	signingPublic := append([]byte(nil), a.DevicePublicKey...)
	accountID, deviceID := a.ID, a.DeviceID
	a.mu.Unlock()
	if !ok {
		return ServerRegistration{}, ErrUnknownWorkspace
	}

	row, err := loadAccountRow(ctx, db)
	if err != nil {
		return ServerRegistration{}, err
	}
	params := corecrypto.Argon2idParams{
		CryptoProfile: corecrypto.CryptoProfileV1, Algorithm: corecrypto.Argon2idName, Salt: row.kdfSalt,
		MemoryKiB: row.kdfMemoryKiB, TimeCost: row.kdfTimeCost, Parallelism: row.kdfParallelism,
		DerivedKeyBytes: corecrypto.RootKeyBytes,
	}
	header, err := corecrypto.NewKeybagHeader(accountID.Bytes(), row.keybagVersion, params)
	if err != nil {
		return ServerRegistration{}, err
	}
	keybag, err := json.Marshal(corecrypto.EncryptedKeybag{Header: header, Nonce: row.keybagNonce, Ciphertext: row.keybagCiphertext})
	if err != nil {
		return ServerRegistration{}, err
	}

	payloadBytes := append([]byte(nil), entry.KeyID...)
	err = entry.Key.Use(func(key []byte) error {
		payloadBytes = append(payloadBytes, key...)
		return nil
	})
	if err != nil {
		clear(payloadBytes)
		return ServerRegistration{}, err
	}
	payload, err := corecrypto.TakeSecret(payloadBytes)
	if err != nil {
		clear(payloadBytes)
		return ServerRegistration{}, err
	}
	defer payload.Close()
	envelope, err := corecrypto.SealWorkspaceKeyEnvelope(corecrypto.CryptoProfileV1, identityPublic, payload)
	if err != nil {
		return ServerRegistration{}, err
	}
	return ServerRegistration{
		UserID: accountID, IdentityPublic: identityPublic, AuthorityPublic: authorityPublic,
		DeviceID: deviceID, SigningPublic: signingPublic, WorkspaceID: workspaceID,
		WorkspaceKeyID: append([]byte(nil), entry.KeyID...), WorkspaceEnvelope: envelope, KeybagCiphertext: keybag,
	}, nil
}
