package main

import (
	"context"
	"sort"
	"time"

	"github.com/beresta-app/beresta/core/sharecode"
	"github.com/beresta-app/beresta/core/transport"
)

// WorkspaceSummaryDTO is one workspace this account holds a key for: either
// the one it created locally (Role "owner") or one it joined by redeeming a
// grant code from AcceptWorkspaceGrant (Role "owner" or "member", per the
// server's membership record). Role is "unknown" when it cannot be resolved
// - sync is disabled, or the per-workspace ListMembers call failed - rather
// than failing the whole ListWorkspaces call over one degraded workspace.
type WorkspaceSummaryDTO struct {
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
	MemberCount int    `json:"member_count,omitempty"`
}

// WorkspaceMemberDTO is one account with current or historical access to a
// workspace. A removed member remains in the response as a revoked row so
// the owner can see that access was intentionally withdrawn.
type WorkspaceMemberDTO struct {
	UserID      string     `json:"user_id"`
	DisplayName string     `json:"display_name"`
	Role        string     `json:"role"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// ExportIdentity returns this account's identity as a beresta://identity
// code: paste it to whoever owns a workspace you want to join, so they can
// call ShareWorkspace with it.
func (a *App) ExportIdentity() (string, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return "", mapError(err)
	}
	code, err := sharecode.EncodeIdentity(acc.ID, acc.IdentityPublicKey)
	if err != nil {
		return "", mapError(err)
	}
	return code, nil
}

// ShareWorkspace grants the account behind identityCode (an ExportIdentity
// code from the recipient's device) membership in this account's active
// workspace, and returns a beresta://grant code for the caller to hand back
// to the recipient so they can call AcceptWorkspaceGrant. Sync must already
// be enabled: AddMember requires the recipient to already be registered on
// this same server (see server/identity_store.go's memberships foreign key),
// and this call itself needs the live server connection to submit the grant.
func (a *App) ShareWorkspace(identityCode string) (string, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return "", mapError(err)
	}
	a.mu.Lock()
	httpTransport := a.httpTransport
	a.mu.Unlock()
	if httpTransport == nil {
		return "", &AppError{Code: ErrCodeInvalidInput, Message: "Enable server synchronization before sharing this workspace."}
	}
	recipientID, recipientIdentityPublicKey, err := sharecode.DecodeIdentity(identityCode)
	if err != nil {
		return "", &AppError{Code: ErrCodeInvalidInput, Message: err.Error()}
	}
	invitation, err := acc.ShareWorkspace(workspaceID, recipientID, recipientIdentityPublicKey)
	if err != nil {
		return "", mapError(err)
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 30*time.Second)
	defer cancel()
	if err := httpTransport.AddMember(ctx, workspaceID.String(), recipientID.String(), invitation.KeyID, invitation.Envelope); err != nil {
		return "", mapError(err)
	}
	grantCode, err := sharecode.EncodeGrant(workspaceID, invitation.KeyID, acc.AuthorityPublicKey, invitation.Signature)
	if err != nil {
		return "", mapError(err)
	}
	return grantCode, nil
}

// AcceptWorkspaceGrant redeems a beresta://grant code produced by
// ShareWorkspace: it fetches the matching sealed key envelope from the
// server, adds the shared workspace to this account's local keybag, and
// makes it the active workspace. Sync must already be enabled - the account
// must already be registered on the same server the grant was issued
// against, since GetKeyEnvelopes only returns envelopes for workspaces this
// device's user is already a member of (granted by ShareWorkspace's
// AddMember call).
func (a *App) AcceptWorkspaceGrant(grantCode string) (WorkspaceSummaryDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return WorkspaceSummaryDTO{}, mapError(err)
	}
	a.mu.Lock()
	httpTransport := a.httpTransport
	a.mu.Unlock()
	if httpTransport == nil {
		return WorkspaceSummaryDTO{}, &AppError{Code: ErrCodeInvalidInput, Message: "Connect to the server before joining a shared workspace."}
	}
	workspaceID, keyID, authorityPublicKey, signature, err := sharecode.DecodeGrant(grantCode)
	if err != nil {
		return WorkspaceSummaryDTO{}, &AppError{Code: ErrCodeInvalidInput, Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 30*time.Second)
	defer cancel()
	envelopes, err := httpTransport.GetKeyEnvelopes(ctx, workspaceID.String())
	if err != nil {
		return WorkspaceSummaryDTO{}, mapError(err)
	}
	envelope, found := transport.FindKeyEnvelope(envelopes, keyID)
	if !found {
		return WorkspaceSummaryDTO{}, &AppError{Code: ErrCodeShareNotFound, Message: "This workspace share was not found, or has already been revoked."}
	}
	if err := acc.AcceptWorkspaceShare(ctx, workspaceID, keyID, envelope, authorityPublicKey, signature); err != nil {
		return WorkspaceSummaryDTO{}, mapError(err)
	}

	a.mu.Lock()
	previousSettings := a.settings
	next := previousSettings
	next.ActiveWorkspaceID = workspaceID.String()
	a.mu.Unlock()
	if err := saveSettings(next); err != nil {
		return WorkspaceSummaryDTO{}, mapError(err)
	}
	a.mu.Lock()
	a.settings = next
	a.mu.Unlock()
	if err := a.attachWorkspaceSync(acc, workspaceID); err != nil {
		// The workspace key is already durably in the local keybag (accepted
		// above) - only the active-workspace switch failed - so roll back
		// the settings change, not the join, keeping settings.json and the
		// still-running sync worker (targeting whatever was active before)
		// in agreement. SetActiveWorkspace can retry making it active later.
		if rollbackErr := saveSettings(previousSettings); rollbackErr == nil {
			a.mu.Lock()
			a.settings = previousSettings
			a.mu.Unlock()
		}
		return WorkspaceSummaryDTO{}, err
	}
	a.emit(EventWorkspaceChanged)

	summary := WorkspaceSummaryDTO{WorkspaceID: workspaceID.String(), Active: true, Role: "unknown"}
	if members, err := httpTransport.ListMembers(ctx, workspaceID.String()); err == nil {
		summary.Role, summary.MemberCount = transport.SelfRole(members, acc.ID.String()), transport.ActiveMemberCount(members)
	}
	return summary, nil
}

// ListWorkspaces returns every workspace this account holds a key for, with
// a best-effort role/member count per workspace (degraded to "unknown"/0
// rather than failing the whole call when sync is disabled or one lookup
// fails) and the currently active one flagged.
func (a *App) ListWorkspaces() ([]WorkspaceSummaryDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return nil, mapError(err)
	}
	ids, err := acc.Workspaces()
	if err != nil {
		return nil, mapError(err)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Compare(ids[j]) < 0 })

	a.mu.Lock()
	preferred := a.settings.ActiveWorkspaceID
	httpTransport := a.httpTransport
	a.mu.Unlock()
	activeID := resolveActiveWorkspace(ids, preferred)

	ctx, cancel := context.WithTimeout(a.requestContext(), 15*time.Second)
	defer cancel()
	summaries := make([]WorkspaceSummaryDTO, len(ids))
	for i, id := range ids {
		summary := WorkspaceSummaryDTO{WorkspaceID: id.String(), Active: id == activeID, Role: "unknown"}
		if httpTransport != nil {
			if members, err := httpTransport.ListMembers(ctx, id.String()); err == nil {
				summary.Role, summary.MemberCount = transport.SelfRole(members, acc.ID.String()), transport.ActiveMemberCount(members)
			}
		}
		summaries[i] = summary
	}
	return summaries, nil
}

// ListWorkspaceMembers returns the accounts connected to a workspace. The
// server authorizes this only for an active workspace member; owners can use
// RevokeWorkspaceMember to remove a non-owner account's future sync access.
func (a *App) ListWorkspaceMembers(workspaceID string) ([]WorkspaceMemberDTO, error) {
	if _, err := a.currentAccount(); err != nil {
		return nil, mapError(err)
	}
	if _, err := parseID(workspaceID); err != nil {
		return nil, err
	}
	a.mu.Lock()
	httpTransport := a.httpTransport
	a.mu.Unlock()
	if httpTransport == nil {
		return nil, &AppError{Code: ErrCodeInvalidInput, Message: "Server synchronization is disabled."}
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 15*time.Second)
	defer cancel()
	members, err := httpTransport.ListMembers(ctx, workspaceID)
	if err != nil {
		return nil, mapError(err)
	}
	result := make([]WorkspaceMemberDTO, len(members))
	for i, member := range members {
		result[i] = WorkspaceMemberDTO{UserID: member.UserID, DisplayName: member.DisplayName, Role: member.Role, RevokedAt: member.RevokedAt}
	}
	return result, nil
}

// RevokeWorkspaceMember removes a non-owner account from workspaceID. It
// blocks that account's subsequent server synchronization; it cannot erase
// notes or keys the account downloaded before removal.
func (a *App) RevokeWorkspaceMember(workspaceID, memberUserID string) error {
	if _, err := a.currentAccount(); err != nil {
		return mapError(err)
	}
	if _, err := parseID(workspaceID); err != nil {
		return err
	}
	if _, err := parseID(memberUserID); err != nil {
		return err
	}
	a.mu.Lock()
	httpTransport := a.httpTransport
	a.mu.Unlock()
	if httpTransport == nil {
		return &AppError{Code: ErrCodeInvalidInput, Message: "Server synchronization is disabled."}
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 30*time.Second)
	defer cancel()
	if err := httpTransport.RevokeMember(ctx, workspaceID, memberUserID); err != nil {
		return mapError(err)
	}
	a.emit(EventWorkspaceChanged)
	return nil
}

// SetActiveWorkspace changes which of the account's workspaces bound methods
// act against, and - when sync is enabled - redirects the running sync
// worker at it. workspaceID must name a workspace this account already
// holds a key for (see ListWorkspaces); switching to one it does not is
// rejected rather than silently falling back, since that would be confusing
// after an explicit user choice.
func (a *App) SetActiveWorkspace(workspaceID string) error {
	acc, err := a.currentAccount()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(workspaceID)
	if err != nil {
		return err
	}
	ids, err := acc.Workspaces()
	if err != nil {
		return mapError(err)
	}
	member := false
	for _, candidate := range ids {
		if candidate == id {
			member = true
			break
		}
	}
	if !member {
		return &AppError{Code: ErrCodeWorkspaceNotHeld, Message: "That workspace is not one this account holds."}
	}

	a.mu.Lock()
	previousSettings, httpTransport := a.settings, a.httpTransport
	a.mu.Unlock()
	next := previousSettings
	next.ActiveWorkspaceID = id.String()
	if err := saveSettings(next); err != nil {
		return mapError(err)
	}
	a.mu.Lock()
	a.settings = next
	a.mu.Unlock()

	if httpTransport != nil {
		if err := a.attachWorkspaceSync(acc, id); err != nil {
			// Roll back so settings.json and the live sync worker (still
			// targeting the previous workspace) never disagree.
			if rollbackErr := saveSettings(previousSettings); rollbackErr == nil {
				a.mu.Lock()
				a.settings = previousSettings
				a.mu.Unlock()
			}
			return err
		}
	}
	a.emit(EventWorkspaceChanged)
	return nil
}
