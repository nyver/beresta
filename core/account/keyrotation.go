package account

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sort"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

// RotationRecipient is one active member's sealed copy of a freshly
// rotated workspace key.
type RotationRecipient struct {
	UserID   model.ID
	Envelope []byte
}

// RotationInvitation is the complete sealed, signed material for rotating a
// workspace's current key: a fresh key sealed independently to every active
// member (including the rotating account itself), plus a signature over the
// key-transition record so any recipient can verify who authorized it.
type RotationInvitation struct {
	WorkspaceID model.ID
	KeyID       []byte
	Recipients  []RotationRecipient
	Signature   []byte
}

// BeginWorkspaceKeyRotation generates a brand-new workspace key and seals an
// independent copy to every identity public key in recipients (which must
// include this account's own IdentityPublicKey to keep read access after
// rotation). It performs no network access and does not mutate local
// state: the caller submits the sealed envelopes through a transport (see
// core/transport.HTTP.RotateWorkspaceKey), then applies the same key to
// every local device - including this one - through
// AcceptWorkspaceKeyRotation, exactly as any other recipient would.
//
// Key rotation is the mechanism behind member and device revocation's
// forward-looking guarantee: a party left out of recipients can no longer
// decrypt content encrypted after this point, even though it retains
// whatever it already downloaded under the old key (see
// docs/threat-model.md).
func (a *Account) BeginWorkspaceKeyRotation(workspaceID model.ID, recipients map[model.ID][]byte) (RotationInvitation, error) {
	if len(recipients) == 0 {
		return RotationInvitation{}, errors.New("account: key rotation requires at least one recipient")
	}
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return RotationInvitation{}, ErrAccountLocked
	}
	_, ok := a.workspaceKeys[workspaceID]
	authorityPrivate := a.authorityPrivate
	a.mu.Unlock()
	if !ok {
		return RotationInvitation{}, ErrUnknownWorkspace
	}

	newKeyID := make([]byte, workspaceKeyIDBytes)
	if _, err := io.ReadFull(cryptorand.Reader, newKeyID); err != nil {
		return RotationInvitation{}, fmt.Errorf("account: generate rotated key ID: %w", err)
	}
	newKeyBytes := make([]byte, workspaceKeyBytes)
	if _, err := io.ReadFull(cryptorand.Reader, newKeyBytes); err != nil {
		return RotationInvitation{}, fmt.Errorf("account: generate rotated key: %w", err)
	}
	newKey, err := corecrypto.TakeSecret(newKeyBytes)
	if err != nil {
		clear(newKeyBytes)
		return RotationInvitation{}, err
	}
	defer newKey.Close()

	recipientIDs := make([]model.ID, 0, len(recipients))
	for id := range recipients {
		recipientIDs = append(recipientIDs, id)
	}
	sort.Slice(recipientIDs, func(i, j int) bool { return recipientIDs[i].String() < recipientIDs[j].String() })

	invitation := RotationInvitation{WorkspaceID: workspaceID, KeyID: newKeyID}
	for _, id := range recipientIDs {
		envelope, err := corecrypto.SealWorkspaceKeyEnvelope(corecrypto.CryptoProfileV1, recipients[id], newKey)
		if err != nil {
			return RotationInvitation{}, fmt.Errorf("account: seal rotated key to recipient: %w", err)
		}
		invitation.Recipients = append(invitation.Recipients, RotationRecipient{UserID: id, Envelope: envelope})
	}
	signature, err := corecrypto.SignCanonical(corecrypto.CryptoProfileV1, authorityPrivate, corecrypto.SignatureDomainKeyTransition,
		keyTransitionSignatureInput(workspaceID, newKeyID, recipientIDs))
	if err != nil {
		return RotationInvitation{}, err
	}
	invitation.Signature = signature
	return invitation, nil
}

// AcceptWorkspaceKeyRotation applies a rotated workspace key that was sealed
// to this account: it opens the envelope, marks the workspace's current key
// historical (still usable to decrypt content written before the
// transition), and installs the new key as current. If
// rotatorAuthorityPublicKey is non-empty, signature is verified against it
// first. Calling this again with the same keyID is idempotent.
func (a *Account) AcceptWorkspaceKeyRotation(ctx context.Context, workspaceID model.ID, newKeyID, sealedEnvelope []byte, rotatorAuthorityPublicKey, signature []byte, allRecipientIDs []model.ID) error {
	if err := workspaceID.Validate(); err != nil || len(newKeyID) != workspaceKeyIDBytes {
		return errors.New("account: invalid key rotation")
	}
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return ErrAccountLocked
	}
	current, ok := a.workspaceKeys[workspaceID]
	if !ok {
		a.mu.Unlock()
		return ErrUnknownWorkspace
	}
	if bytesEqual(current.KeyID, newKeyID) {
		a.mu.Unlock()
		return nil // already applied
	}
	db, identityPublic, identityPrivate, deviceID := a.db, a.IdentityPublicKey, a.identityPrivate, a.DeviceID
	a.mu.Unlock()

	if len(rotatorAuthorityPublicKey) != 0 {
		sorted := append([]model.ID(nil), allRecipientIDs...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
		if err := corecrypto.VerifyCanonical(corecrypto.CryptoProfileV1, rotatorAuthorityPublicKey, corecrypto.SignatureDomainKeyTransition,
			keyTransitionSignatureInput(workspaceID, newKeyID, sorted), signature); err != nil {
			return fmt.Errorf("account: key-transition signature verification failed: %w", err)
		}
	}

	newKey, err := corecrypto.OpenWorkspaceKeyEnvelope(corecrypto.CryptoProfileV1, identityPublic, identityPrivate, sealedEnvelope)
	if err != nil {
		return fmt.Errorf("account: open rotated workspace key: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		newKey.Close()
		return ErrAccountLocked
	}
	current, ok = a.workspaceKeys[workspaceID]
	if !ok {
		newKey.Close()
		return ErrUnknownWorkspace
	}
	if bytesEqual(current.KeyID, newKeyID) {
		newKey.Close()
		return nil // applied by a concurrent call while we verified/decrypted
	}
	clock, err := a.clock.Tick()
	if err != nil {
		newKey.Close()
		return err
	}

	existing := a.snapshotKeybagWorkspaceKeys()
	for i := range existing {
		if existing[i].WorkspaceID == workspaceID && bytesEqual(existing[i].KeyID, current.KeyID) {
			existing[i].State = workspaceKeyStateHistorical
		}
	}
	newEntry := keybagWorkspaceKey{WorkspaceID: workspaceID, KeyID: append([]byte(nil), newKeyID...), Key: newKey, State: workspaceKeyStateCurrent}
	identity := keybagIdentity{
		IdentityPublicKey: a.IdentityPublicKey, IdentityPrivateKey: a.identityPrivate,
		AuthorityPublicKey: a.AuthorityPublicKey, AuthorityPrivateKey: a.authorityPrivate,
	}
	if err := persistWorkspaceKeyRotation(ctx, db, a.rootKey, identity, a.ID, workspaceID, deviceID, clock, current.KeyID, existing, newEntry); err != nil {
		newKey.Close()
		return err
	}
	// The old current key is now historical: keep it retained in memory
	// (rather than closing it) so this device can still decrypt content
	// encrypted before the rotation.
	a.historicalWorkspaceKeys[workspaceID] = append(a.historicalWorkspaceKeys[workspaceID],
		workspaceKeyEntry{KeyID: current.KeyID, Key: current.Key, State: workspaceKeyStateHistorical})
	a.workspaceKeys[workspaceID] = workspaceKeyEntry{KeyID: newEntry.KeyID, Key: newKey, State: workspaceKeyStateCurrent}
	return nil
}

// keyTransitionSignatureInput builds the canonical payload signed for a
// workspace key rotation: the workspace, the new key ID, and the sorted set
// of recipients the new key was sealed to.
func keyTransitionSignatureInput(workspaceID model.ID, newKeyID []byte, sortedRecipientIDs []model.ID) []byte {
	result := appendLP(nil, workspaceID.Bytes())
	result = appendLP(result, newKeyID)
	result = appendU32(result, uint32(len(sortedRecipientIDs)))
	for _, id := range sortedRecipientIDs {
		result = appendLP(result, id.Bytes())
	}
	return result
}

// persistWorkspaceKeyRotation transactionally marks oldKeyID historical,
// inserts the new current key row, and re-encrypts the keybag from
// existingKeys (already updated to reflect the historical transition) plus
// newKey.
func persistWorkspaceKeyRotation(ctx context.Context, db *sql.DB, rootKey *corecrypto.Secret, identity keybagIdentity, accountID, workspaceID, deviceID model.ID, clock model.HLC, oldKeyID []byte, existingKeys []keybagWorkspaceKey, newKey keybagWorkspaceKey) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("account: begin key rotation transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE workspace_keys SET state = ? WHERE workspace_id = ? AND key_id = ?`,
		workspaceKeyStateHistorical, workspaceID.Bytes(), oldKeyID); err != nil {
		return fmt.Errorf("account: mark previous workspace key historical: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_keys (key_id, workspace_id, state, activated_physical_ms, activated_logical, activated_device_id)
		VALUES (?, ?, ?, ?, ?, ?)`,
		newKey.KeyID, workspaceID.Bytes(), newKey.State, clock.PhysicalMS, clock.Logical, deviceID.Bytes()); err != nil {
		return fmt.Errorf("account: insert rotated workspace key row: %w", err)
	}
	if err := reencryptKeybag(ctx, tx, rootKey, identity, accountID, append(existingKeys, newKey)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("account: commit key rotation transaction: %w", err)
	}
	return nil
}
