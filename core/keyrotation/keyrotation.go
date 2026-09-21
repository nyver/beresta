// Package keyrotation orchestrates automatic workspace-key rotation on
// member revocation: the owner-side trigger that begins and publishes a
// fresh key, and the reactive detection/application every other device
// (including the owner's own other devices) performs on its next sync. It
// sits above both core/account and core/transport - neither of which may
// import the other's caller - because it needs both: core/account for the
// rotation crypto (BeginWorkspaceKeyRotation, AcceptWorkspaceKeyRotation)
// and core/transport's wire types for the server calls that publish and
// fetch rotation state. See
// openspec/changes/harden-product-ux-reliability/design.md decision 10.
package keyrotation

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/transport"
)

// Transport is the subset of *transport.HTTP that TriggerAfterRevocation
// and Reconcile need.
type Transport interface {
	ListMembers(ctx context.Context, workspaceID string) ([]transport.RemoteMember, error)
	GetKeyEnvelopes(ctx context.Context, workspaceID string) ([]transport.RemoteKeyEnvelope, []transport.RemoteKeyTransition, error)
	RotateWorkspaceKey(ctx context.Context, workspaceID string, keyID []byte, envelopes []transport.RotationEnvelope, signature []byte) error
}

// TriggerAfterRevocation begins and publishes a fresh workspace key sealed
// to every member still active after a revocation, then applies it to this
// device. Call it immediately after a RevokeMember call succeeds: it marks
// the rotation durably pending first, so a failure here (network loss,
// process termination) is retried by a later Reconcile call from the sync
// worker's Prepare hook instead of silently leaving the removed member's
// key envelope as the workspace's only key.
//
// Automatic rotation is scoped to member revocation only - not revoking one
// of this account's own devices - because key envelopes are sealed to a
// member's account-level identity key, which every device of that account
// already shares through the account-wide keybag sync. Rotating in
// response to a same-account device revocation would re-seal to the same
// identity key and provide no confidentiality benefit.
func TriggerAfterRevocation(ctx context.Context, acc *account.Account, remote Transport, workspaceID model.ID) error {
	if err := store.MarkKeyRotationPending(ctx, acc.DB(), workspaceID, time.Now()); err != nil {
		return fmt.Errorf("keyrotation: mark pending: %w", err)
	}
	return completePendingRotation(ctx, acc, remote, workspaceID)
}

// Reconcile applies any workspace key rotation this device has not yet
// applied for workspaceID, and - if this device previously began a
// rotation that did not finish publishing (a durable local marker from
// TriggerAfterRevocation) - retries publishing it. It is safe to call on
// every sync cycle: with nothing pending and nothing new to apply, it costs
// one GetKeyEnvelopes call and, for a workspace owner, one ListMembers call
// only when a local marker is present.
func Reconcile(ctx context.Context, acc *account.Account, remote Transport, workspaceID model.ID) error {
	if err := completePendingRotation(ctx, acc, remote, workspaceID); err != nil {
		return err
	}
	return applyLatestKey(ctx, acc, remote, workspaceID)
}

// completePendingRotation finishes a rotation this device began (or is the
// workspace owner retrying) if a local pending-rotation marker exists. It
// is a no-op when none does.
//
// Known limitation: each retry generates a brand-new random key rather than
// resuming a specific prior attempt, so if RotateWorkspaceKey previously
// published successfully but this device's own AcceptWorkspaceKeyRotation
// then failed before clearing the marker (a narrow window, since that call
// is local-only with no network I/O), the next retry rotates again to a
// second new key rather than reusing the first. Both keys still correctly
// exclude the revoked member - the confidentiality guarantee holds - so
// this is wasted churn (one extra rotation other devices must reconcile),
// not a security issue.
func completePendingRotation(ctx context.Context, acc *account.Account, remote Transport, workspaceID model.ID) error {
	pending, err := store.KeyRotationPending(ctx, acc.DB(), workspaceID)
	if err != nil {
		return fmt.Errorf("keyrotation: check pending marker: %w", err)
	}
	if !pending {
		return nil
	}

	members, err := remote.ListMembers(ctx, workspaceID.String())
	if err != nil {
		return fmt.Errorf("keyrotation: list members: %w", err)
	}
	recipients := make(map[model.ID][]byte, len(members))
	for _, member := range members {
		if member.RevokedAt != nil {
			continue
		}
		id, err := model.ParseIDString(member.UserID)
		if err != nil {
			return fmt.Errorf("keyrotation: malformed member id: %w", err)
		}
		recipients[id] = member.IdentityPublic
	}

	invitation, err := acc.BeginWorkspaceKeyRotation(workspaceID, recipients)
	if err != nil {
		return fmt.Errorf("keyrotation: begin rotation: %w", err)
	}
	envelopes := make([]transport.RotationEnvelope, 0, len(invitation.Recipients))
	allRecipientIDs := make([]model.ID, 0, len(invitation.Recipients))
	var selfEnvelope []byte
	for _, recipient := range invitation.Recipients {
		envelopes = append(envelopes, transport.RotationEnvelope{UserID: recipient.UserID.String(), Envelope: recipient.Envelope})
		allRecipientIDs = append(allRecipientIDs, recipient.UserID)
		if recipient.UserID == acc.ID {
			selfEnvelope = recipient.Envelope
		}
	}
	if len(selfEnvelope) == 0 {
		return errors.New("keyrotation: rotation invitation missing this account's own envelope")
	}

	if err := remote.RotateWorkspaceKey(ctx, workspaceID.String(), invitation.KeyID, envelopes, invitation.Signature); err != nil {
		return fmt.Errorf("keyrotation: publish rotation: %w", err)
	}
	if err := acc.AcceptWorkspaceKeyRotation(ctx, workspaceID, invitation.KeyID, selfEnvelope, acc.AuthorityPublicKey, invitation.Signature, allRecipientIDs); err != nil {
		return fmt.Errorf("keyrotation: apply own rotation: %w", err)
	}
	if err := store.ClearKeyRotationPending(ctx, acc.DB(), workspaceID); err != nil {
		return fmt.Errorf("keyrotation: clear pending marker: %w", err)
	}
	return nil
}

// applyLatestKey detects and applies a workspace key rotation this device
// did not itself initiate (an owner's other device, or a fellow non-owner
// member's device).
func applyLatestKey(ctx context.Context, acc *account.Account, remote Transport, workspaceID model.ID) error {
	_, currentKeyID, err := acc.WorkspaceKey(workspaceID)
	if err != nil {
		return fmt.Errorf("keyrotation: read current key: %w", err)
	}

	envelopes, transitions, err := remote.GetKeyEnvelopes(ctx, workspaceID.String())
	if err != nil {
		return fmt.Errorf("keyrotation: get key envelopes: %w", err)
	}
	if len(envelopes) == 0 {
		return nil
	}
	latest := envelopes[0]
	for _, candidate := range envelopes[1:] {
		if candidate.CreatedAt.After(latest.CreatedAt) {
			latest = candidate
		}
	}
	latestKeyID, err := hex.DecodeString(latest.KeyID)
	if err != nil {
		return fmt.Errorf("keyrotation: malformed key id: %w", err)
	}
	if bytes.Equal(latestKeyID, currentKeyID) {
		return nil // already applied - the common case on every ordinary cycle
	}

	var signature []byte
	var signedRecipients []string
	for _, transition := range transitions {
		if transition.KeyID == latest.KeyID {
			signature = transition.Signature
			signedRecipients = transition.RecipientUserIDs
			break
		}
	}
	// Fail closed, not "skip verification", when no transition record
	// matches: a hostile server cannot be trusted to admit it is
	// withholding one, so there is no way to tell a genuinely old server
	// (before key_transitions existed) apart from a current one selectively
	// omitting the record for one forged rotation. Skipping verification
	// here would let either case bypass it entirely, defeating the point of
	// verifying at all. This device simply keeps its last-known key and
	// retries next cycle - self-healing once the server is upgraded (or, in
	// the hostile case, never wrongly trusting the forged key).
	if len(signature) == 0 {
		return errors.New("keyrotation: no key-transition signature found for the newest workspace key; not applying it")
	}
	recipientIDs := make([]model.ID, 0, len(signedRecipients))
	for _, userID := range signedRecipients {
		id, err := model.ParseIDString(userID)
		if err != nil {
			return fmt.Errorf("keyrotation: malformed transition recipient id: %w", err)
		}
		recipientIDs = append(recipientIDs, id)
	}
	sort.Slice(recipientIDs, func(i, j int) bool { return recipientIDs[i].String() < recipientIDs[j].String() })

	members, err := remote.ListMembers(ctx, workspaceID.String())
	if err != nil {
		return fmt.Errorf("keyrotation: list members: %w", err)
	}
	var rotatorAuthorityPublicKey []byte
	for _, member := range members {
		if member.Role == "owner" {
			rotatorAuthorityPublicKey = member.AuthorityPublic
			break
		}
	}
	if len(rotatorAuthorityPublicKey) == 0 {
		return errors.New("keyrotation: workspace owner's authority key was not found; not applying the detected rotation")
	}

	if err := acc.AcceptWorkspaceKeyRotation(ctx, workspaceID, latestKeyID, latest.Envelope, rotatorAuthorityPublicKey, signature, recipientIDs); err != nil {
		return fmt.Errorf("keyrotation: apply detected rotation: %w", err)
	}
	return nil
}
