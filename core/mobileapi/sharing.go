package mobileapi

import (
	"errors"
	"sort"

	"github.com/beresta-app/beresta/core/sharecode"
	"github.com/beresta-app/beresta/core/transport"
)

// workspaceSummary is one workspace this account holds a key for, mirroring
// desktop's WorkspaceSummaryDTO. Role is "unknown" when it cannot be
// resolved - sync disabled, or the per-workspace ListMembers call failed -
// rather than failing the whole ListWorkspaces call over one degraded
// workspace.
type workspaceSummary struct {
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
	Active      bool   `json:"active"`
	MemberCount int    `json:"member_count,omitempty"`
}

// ExportIdentity returns this account's identity as a beresta://identity
// code: paste it to whoever owns a workspace you want to join, so they can
// call ShareWorkspace with it.
func (s *Service) ExportIdentity(requestID string) (string, error) {
	_, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	code, err := sharecode.EncodeIdentity(value.ID, value.IdentityPublicKey)
	if err != nil {
		return "", err
	}
	return marshal(map[string]string{"identity_code": code})
}

// ShareWorkspace grants the account behind identityCode (an ExportIdentity
// code from the recipient's device) membership in this account's active
// workspace, and returns a beresta://grant code for the caller to hand back
// to the recipient so they can call AcceptWorkspaceGrant. Sync must already
// be enabled: AddMember requires the recipient to already be registered on
// this same server (see server/identity_store.go's memberships foreign
// key), and this call itself needs the live server connection to submit the
// grant.
func (s *Service) ShareWorkspace(requestID, identityCode string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, workspaceID, err := s.accountState()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	remote := s.remote
	s.mu.Unlock()
	if remote == nil {
		return "", errors.New("mobileapi: enable server synchronization before sharing this workspace")
	}
	recipientID, recipientIdentityPublicKey, err := sharecode.DecodeIdentity(identityCode)
	if err != nil {
		return "", err
	}
	invitation, err := value.ShareWorkspace(workspaceID, recipientID, recipientIdentityPublicKey)
	if err != nil {
		return "", err
	}
	if err := remote.AddMember(ctx, workspaceID.String(), recipientID.String(), invitation.KeyID, invitation.Envelope); err != nil {
		return "", err
	}
	grantCode, err := sharecode.EncodeGrant(workspaceID, invitation.KeyID, value.AuthorityPublicKey, invitation.Signature)
	if err != nil {
		return "", err
	}
	return marshal(map[string]string{"grant_code": grantCode})
}

// AcceptWorkspaceGrant redeems a beresta://grant code produced by
// ShareWorkspace: it fetches the matching sealed key envelope from the
// server, adds the shared workspace to this account's local keybag, and
// makes it the active workspace for the rest of this unlocked session (a
// fresh unlock resets to the deterministic smallest-workspace-ID pick;
// SetActiveWorkspace after unlocking restores whichever workspace the user
// prefers - this session-only scope, unlike desktop's persisted
// ActiveWorkspaceID setting, is a known limitation of this first cut). Sync
// must already be enabled - the account must already be registered on the
// same server the grant was issued against, since GetKeyEnvelopes only
// returns envelopes for workspaces this device's user is already a member
// of (granted by ShareWorkspace's AddMember call).
func (s *Service) AcceptWorkspaceGrant(requestID, grantCode string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	remote := s.remote
	s.mu.Unlock()
	if remote == nil {
		return "", errors.New("mobileapi: connect to the server before joining a shared workspace")
	}
	workspaceID, keyID, authorityPublicKey, signature, err := sharecode.DecodeGrant(grantCode)
	if err != nil {
		return "", err
	}
	envelopes, err := remote.GetKeyEnvelopes(ctx, workspaceID.String())
	if err != nil {
		return "", err
	}
	envelope, found := transport.FindKeyEnvelope(envelopes, keyID)
	if !found {
		return "", errors.New("mobileapi: this workspace share was not found, or has already been revoked")
	}
	if err := value.AcceptWorkspaceShare(ctx, workspaceID, keyID, envelope, authorityPublicKey, signature); err != nil {
		return "", err
	}
	if err := s.attachWorkspaceSync(value, workspaceID); err != nil {
		return "", err
	}
	s.emit("workspace_changed", map[string]string{"workspace_id": workspaceID.String()})

	summary := workspaceSummary{WorkspaceID: workspaceID.String(), Active: true, Role: "unknown"}
	if members, err := remote.ListMembers(ctx, workspaceID.String()); err == nil {
		summary.Role, summary.MemberCount = transport.SelfRole(members, value.ID.String()), transport.ActiveMemberCount(members)
	}
	return marshal(summary)
}

// ListWorkspaces returns every workspace this account holds a key for, with
// a best-effort role/member count per workspace (degraded to "unknown"/0
// rather than failing the whole call when sync is disabled or one lookup
// fails) and the currently active one flagged.
func (s *Service) ListWorkspaces(requestID string) (string, error) {
	ctx, done, err := s.begin(requestID)
	if err != nil {
		return "", err
	}
	defer done()
	value, activeID, err := s.accountState()
	if err != nil {
		return "", err
	}
	ids, err := value.Workspaces()
	if err != nil {
		return "", err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Compare(ids[j]) < 0 })

	s.mu.Lock()
	remote := s.remote
	s.mu.Unlock()

	summaries := make([]workspaceSummary, len(ids))
	for i, id := range ids {
		summary := workspaceSummary{WorkspaceID: id.String(), Active: id == activeID, Role: "unknown"}
		if remote != nil {
			if members, err := remote.ListMembers(ctx, id.String()); err == nil {
				summary.Role, summary.MemberCount = transport.SelfRole(members, value.ID.String()), transport.ActiveMemberCount(members)
			}
		}
		summaries[i] = summary
	}
	return marshal(summaries)
}

// SetActiveWorkspace changes which of the account's workspaces
// workspace-scoped methods act against for the rest of this unlocked
// session, and - when sync is enabled - redirects the running sync worker
// at it. workspaceID must name a workspace this account already holds a key
// for (see ListWorkspaces); switching to one it does not is rejected rather
// than silently falling back, since that would be confusing after an
// explicit user choice.
func (s *Service) SetActiveWorkspace(requestID, workspaceID string) error {
	_, done, err := s.begin(requestID)
	if err != nil {
		return err
	}
	defer done()
	value, _, err := s.accountState()
	if err != nil {
		return err
	}
	id, err := parseID(workspaceID)
	if err != nil {
		return err
	}
	ids, err := value.Workspaces()
	if err != nil {
		return err
	}
	member := false
	for _, candidate := range ids {
		if candidate == id {
			member = true
			break
		}
	}
	if !member {
		return errors.New("mobileapi: that workspace is not one this account holds")
	}

	s.mu.Lock()
	remote := s.remote
	s.mu.Unlock()
	if remote != nil {
		if err := s.attachWorkspaceSync(value, id); err != nil {
			return err
		}
	} else {
		s.mu.Lock()
		s.workspaceID = id
		s.mu.Unlock()
	}
	s.emit("workspace_changed", map[string]string{"workspace_id": id.String()})
	return nil
}
