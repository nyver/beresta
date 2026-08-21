package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

// ErrAlreadyMember reports that AcceptWorkspaceShare was called for a
// workspace key this account already holds under a different key ID; use
// AcceptKeyRotation (see core/account/keyrotation.go) for a rotated key
// instead.
var ErrAlreadyMember = errors.New("account: this account already holds a different key for this workspace")

// ShareInvitation is the sealed, signed material produced by an authorized
// workspace member to add one recipient. The sealed envelope is opaque to
// the server and to every party except the recipient's own identity private
// key; the signature lets the recipient verify who authorized the grant
// without trusting the server's bookkeeping alone.
type ShareInvitation struct {
	WorkspaceID model.ID
	RecipientID model.ID
	KeyID       []byte
	Envelope    []byte
	Signature   []byte
}

// ShareWorkspace seals workspaceID's current key to recipientIdentityPublicKey
// and signs a membership record with this account's authority key. It makes
// no network access and mutates no local state; the caller submits the
// result through a transport (see core/transport.HTTP.AddMember) and, on
// the recipient's device, through AcceptWorkspaceShare.
func (a *Account) ShareWorkspace(workspaceID, recipientID model.ID, recipientIdentityPublicKey []byte) (ShareInvitation, error) {
	if err := recipientID.Validate(); err != nil {
		return ShareInvitation{}, errors.New("account: invalid recipient")
	}
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return ShareInvitation{}, ErrAccountLocked
	}
	entry, ok := a.workspaceKeys[workspaceID]
	authorityPrivate := a.authorityPrivate
	a.mu.Unlock()
	if !ok {
		return ShareInvitation{}, ErrUnknownWorkspace
	}
	if entry.State != workspaceKeyStateCurrent {
		return ShareInvitation{}, errors.New("account: only the current workspace key can be shared")
	}

	envelope, err := corecrypto.SealWorkspaceKeyEnvelope(corecrypto.CryptoProfileV1, recipientIdentityPublicKey, entry.Key)
	if err != nil {
		return ShareInvitation{}, err
	}
	signature, err := corecrypto.SignCanonical(corecrypto.CryptoProfileV1, authorityPrivate, corecrypto.SignatureDomainMembership,
		membershipSignatureInput(workspaceID, recipientID, entry.KeyID))
	if err != nil {
		return ShareInvitation{}, err
	}
	return ShareInvitation{
		WorkspaceID: workspaceID, RecipientID: recipientID,
		KeyID: append([]byte(nil), entry.KeyID...), Envelope: envelope, Signature: signature,
	}, nil
}

// AcceptWorkspaceShare opens a sealed key envelope addressed to this
// account's identity key and, on success, adds the workspace and its
// current key to local storage and the keybag. If inviterAuthorityPublicKey
// is non-empty, membershipSignature is verified against it first; a caller
// that has not yet obtained a trusted copy of the inviter's authority key
// (for example, over the same out-of-band channel used to confirm a device
// pairing code) may pass nil to skip that additional check, relying on the
// server's own membership authorization instead.
func (a *Account) AcceptWorkspaceShare(ctx context.Context, workspaceID model.ID, keyID, sealedEnvelope []byte, inviterAuthorityPublicKey, membershipSignature []byte) error {
	if err := workspaceID.Validate(); err != nil || len(keyID) != workspaceKeyIDBytes {
		return errors.New("account: invalid workspace share")
	}
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return ErrAccountLocked
	}
	if existing, ok := a.workspaceKeys[workspaceID]; ok {
		a.mu.Unlock()
		if bytesEqual(existing.KeyID, keyID) {
			return nil // already accepted; idempotent
		}
		return ErrAlreadyMember
	}
	db, identityPublic, identityPrivate, recipientID, deviceID := a.db, a.IdentityPublicKey, a.identityPrivate, a.ID, a.DeviceID
	a.mu.Unlock()

	if len(inviterAuthorityPublicKey) != 0 {
		if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, inviterAuthorityPublicKey, corecrypto.SignatureDomainMembership,
			membershipSignatureInput(workspaceID, recipientID, keyID), membershipSignature); err != nil {
			return fmt.Errorf("account: membership grant signature verification failed: %w", err)
		}
	}

	workspaceKey, err := corecrypto.OpenWorkspaceKeyEnvelope(corecrypto.CryptoProfileV1, identityPublic, identityPrivate, sealedEnvelope)
	if err != nil {
		return fmt.Errorf("account: open shared workspace key: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		workspaceKey.Close()
		return ErrAccountLocked
	}
	if existing, ok := a.workspaceKeys[workspaceID]; ok {
		workspaceKey.Close()
		if bytesEqual(existing.KeyID, keyID) {
			return nil // a concurrent call already accepted this exact share while we verified/decrypted
		}
		return ErrAlreadyMember
	}
	clock, err := a.clock.Tick()
	if err != nil {
		workspaceKey.Close()
		return err
	}
	newEntry := keybagWorkspaceKey{WorkspaceID: workspaceID, KeyID: append([]byte(nil), keyID...), Key: workspaceKey, State: workspaceKeyStateCurrent}
	identity := keybagIdentity{
		IdentityPublicKey: a.IdentityPublicKey, IdentityPrivateKey: a.identityPrivate,
		AuthorityPublicKey: a.AuthorityPublicKey, AuthorityPrivateKey: a.authorityPrivate,
	}
	if err := persistNewWorkspaceKey(ctx, db, a.rootKey, identity, a.ID, workspaceID, deviceID, clock, a.snapshotKeybagWorkspaceKeys(), newEntry); err != nil {
		workspaceKey.Close()
		return err
	}
	a.workspaceKeys[workspaceID] = workspaceKeyEntry{KeyID: newEntry.KeyID, Key: workspaceKey, State: workspaceKeyStateCurrent}
	return nil
}

// membershipSignatureInput builds the canonical payload signed for a
// workspace membership grant. It is independent of the sealed envelope
// bytes, so verification never requires decrypting anything.
func membershipSignatureInput(workspaceID, recipientID model.ID, keyID []byte) []byte {
	result := appendLP(nil, workspaceID.Bytes())
	result = appendLP(result, recipientID.Bytes())
	result = appendLP(result, keyID)
	return result
}

// keybagIdentity carries the account-level key pairs the keybag payload
// always includes, so persistence helpers do not need to reach back into a
// locked *Account.
type keybagIdentity struct {
	IdentityPublicKey   []byte
	IdentityPrivateKey  *corecrypto.Secret
	AuthorityPublicKey  []byte
	AuthorityPrivateKey *corecrypto.Secret
}

// persistNewWorkspaceKey transactionally records a newly obtained workspace
// key: it ensures the workspace row exists, inserts the key row, and
// re-encrypts the keybag to include it alongside every key already in
// existingKeys. It does not mutate the caller's live account state; the
// caller updates a.workspaceKeys only after this succeeds.
func persistNewWorkspaceKey(ctx context.Context, db *sql.DB, rootKey *corecrypto.Secret, identity keybagIdentity, accountID, workspaceID, deviceID model.ID, clock model.HLC, existingKeys []keybagWorkspaceKey, newKey keybagWorkspaceKey) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin workspace key transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspaces (id, created_physical_ms, created_logical, created_device_id) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		workspaceID.Bytes(), clock.PhysicalMS, clock.Logical, deviceID.Bytes()); err != nil {
		return fmt.Errorf("account: insert shared workspace row: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_keys (key_id, workspace_id, state, activated_physical_ms, activated_logical, activated_device_id)
		VALUES (?, ?, ?, ?, ?, ?)`,
		newKey.KeyID, workspaceID.Bytes(), newKey.State, clock.PhysicalMS, clock.Logical, deviceID.Bytes()); err != nil {
		return fmt.Errorf("account: insert workspace key row: %w", err)
	}
	if err := reencryptKeybag(ctx, tx, rootKey, identity, accountID, append(existingKeys, newKey)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit workspace key transaction: %w", err)
	}
	return nil
}

// reencryptKeybag re-serializes and re-encrypts the account's keybag from
// identity and workspaceKeys, then updates the stored ciphertext in the
// same transaction as whatever local mutation prompted the change. It
// preserves the account's existing KDF parameters and keybag format
// version; only the nonce and ciphertext change.
func reencryptKeybag(ctx context.Context, tx *sql.Tx, rootKey *corecrypto.Secret, identity keybagIdentity, accountID model.ID, workspaceKeys []keybagWorkspaceKey) error {
	var keybagVersion uint64
	var kdfSalt []byte
	var kdfMemoryKiB, kdfTimeCost, kdfParallelism uint32
	if err := tx.QueryRowContext(ctx, `
		SELECT keybag_version, kdf_salt, kdf_memory_kib, kdf_time_cost, kdf_parallelism
		FROM accounts WHERE id = ?`, accountID.Bytes(),
	).Scan(&keybagVersion, &kdfSalt, &kdfMemoryKiB, &kdfTimeCost, &kdfParallelism); err != nil {
		return fmt.Errorf("account: read account row for keybag update: %w", err)
	}

	payload := keybagPlaintext{
		IdentityPublicKey: identity.IdentityPublicKey, IdentityPrivateKey: identity.IdentityPrivateKey,
		AuthorityPublicKey: identity.AuthorityPublicKey, AuthorityPrivateKey: identity.AuthorityPrivateKey,
		WorkspaceKeys: workspaceKeys,
	}
	payloadSecret, err := encodeKeybagPlaintext(payload)
	if err != nil {
		return fmt.Errorf("account: encode updated keybag: %w", err)
	}
	defer payloadSecret.Close()

	params := corecrypto.Argon2idParams{
		CryptoProfile: corecrypto.CryptoProfileV1, Algorithm: corecrypto.Argon2idName, Salt: kdfSalt,
		MemoryKiB: kdfMemoryKiB, TimeCost: kdfTimeCost, Parallelism: kdfParallelism, DerivedKeyBytes: corecrypto.RootKeyBytes,
	}
	header, err := corecrypto.NewKeybagHeader(accountID.Bytes(), keybagVersion, params)
	if err != nil {
		return fmt.Errorf("account: build updated keybag header: %w", err)
	}
	encrypted, err := corecrypto.EncryptKeybag(rootKey, header, payloadSecret)
	if err != nil {
		return fmt.Errorf("account: encrypt updated keybag: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET keybag_nonce = ?, keybag_ciphertext = ? WHERE id = ?`,
		encrypted.Nonce, encrypted.Ciphertext, accountID.Bytes()); err != nil {
		return fmt.Errorf("account: persist updated keybag: %w", err)
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
