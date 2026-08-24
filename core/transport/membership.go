package transport

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"time"
)

// IsLikelyRevoked reports whether err is consistent with this device's own
// credentials having been revoked server-side: the device-key challenge
// authentication path (see server.Storage.IssueChallenge /
// VerifyChallenge) rejects every request from a revoked device, including
// fresh challenge issuance, so a device that previously authenticated
// successfully and now consistently fails authentication is a signal - not
// a proof - worth surfacing to the user as "this device may have been
// removed" alongside the option to erase local data (see
// docs/threat-model.md and account.EraseLocalAccount). Transient network
// failures and misconfiguration can also produce ErrAuthentication, so
// callers should treat this as a prompt to check, not an automatic wipe.
func IsLikelyRevoked(err error) bool {
	return errors.Is(err, ErrAuthentication)
}

// RemoteMember is one workspace membership row as the server exposes it.
// Public keys are not secret (see docs/threat-model.md); the server only
// returns them to already-authorized fellow workspace members, which lets a
// recipient verify who signed a membership grant or key-transition record
// without a separate directory lookup.
type RemoteMember struct {
	UserID          string     `json:"user_id"`
	DisplayName     string     `json:"display_name"`
	Role            string     `json:"role"`
	IdentityPublic  []byte     `json:"identity_public"`
	AuthorityPublic []byte     `json:"authority_public"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

// ListMembers returns every membership row (including revoked ones) for a
// workspace this device's user currently belongs to.
func (h *HTTP) ListMembers(ctx context.Context, workspaceID string) ([]RemoteMember, error) {
	var response struct {
		Members []RemoteMember `json:"members"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/v1/workspaces/"+workspaceID+"/members", nil, &response, true); err != nil {
		return nil, err
	}
	return response.Members, nil
}

// AddMember submits a sealed per-recipient workspace key envelope, granting
// recipientUserID access to workspaceID under keyID. The caller must have
// already sealed envelope with the recipient's X25519 identity public key
// (see core/account.Account.ShareWorkspace); this method never sees
// plaintext key material.
func (h *HTTP) AddMember(ctx context.Context, workspaceID, recipientUserID string, keyID []byte, envelope []byte) error {
	if recipientUserID == "" || len(keyID) == 0 || len(envelope) == 0 {
		return errors.New("transport: invalid membership grant")
	}
	request := struct {
		UserID   string `json:"user_id"`
		KeyID    string `json:"key_id"`
		Envelope []byte `json:"envelope"`
	}{recipientUserID, hex.EncodeToString(keyID), envelope}
	return h.doJSON(ctx, http.MethodPost, "/v1/workspaces/"+workspaceID+"/members", request, nil, true)
}

// RevokeMember removes a workspace member's access boundary going forward.
// The server rejects the request unless this device's user owns the
// workspace; it does not and cannot erase data the removed member's devices
// already downloaded (see docs/threat-model.md).
func (h *HTTP) RevokeMember(ctx context.Context, workspaceID, memberUserID string) error {
	return h.doJSON(ctx, http.MethodDelete, "/v1/workspaces/"+workspaceID+"/members/"+memberUserID, nil, nil, true)
}

// ListWorkspaceMemberDevices returns every device belonging to any current
// or former member of workspaceID, including revoked devices (needed to
// verify the signature on history authenticated before a revocation
// boundary). A client's own SyncProcessor.Verify pass needs this to
// authenticate operations signed by a fellow workspace member's device;
// core/account.Account.UpsertRemoteDevices is the local sink for the
// result.
func (h *HTTP) ListWorkspaceMemberDevices(ctx context.Context, workspaceID string) ([]RemoteDevice, error) {
	var response struct {
		Devices []RemoteDevice `json:"devices"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/v1/workspaces/"+workspaceID+"/member-devices", nil, &response, true); err != nil {
		return nil, err
	}
	return response.Devices, nil
}

// RemoteKeyEnvelope is one opaque sealed workspace-key envelope addressed to
// this device's user.
type RemoteKeyEnvelope struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	KeyID       string    `json:"key_id"`
	Envelope    []byte    `json:"envelope"`
	CreatedAt   time.Time `json:"created_at"`
}

// FindKeyEnvelope returns the envelope from envelopes (a GetKeyEnvelopes
// result) whose key ID matches keyID, tolerating a malformed key_id string
// from the server by treating it as a non-match rather than failing the
// whole lookup. Both desktop and mobile use this to redeem an
// AcceptWorkspaceGrant code: it matches the key ID the sharer's
// ShareWorkspace/AddMember pair recorded server-side against the sealed
// envelope the recipient needs to open locally.
func FindKeyEnvelope(envelopes []RemoteKeyEnvelope, keyID []byte) ([]byte, bool) {
	target := hex.EncodeToString(keyID)
	for _, candidate := range envelopes {
		if candidate.KeyID == target {
			return candidate.Envelope, true
		}
	}
	return nil, false
}

// SelfRole finds selfUserID's own row in members (a ListMembers result) and
// returns its role ("owner" or "member"), or "unknown" if selfUserID is not
// present (a transient membership-listing inconsistency, not expected in
// steady state).
func SelfRole(members []RemoteMember, selfUserID string) string {
	for _, member := range members {
		if member.UserID == selfUserID {
			return member.Role
		}
	}
	return "unknown"
}

// GetKeyEnvelopes returns every envelope the server has stored for this
// device's user in one workspace, current and historical.
func (h *HTTP) GetKeyEnvelopes(ctx context.Context, workspaceID string) ([]RemoteKeyEnvelope, error) {
	var response struct {
		KeyEnvelopes []RemoteKeyEnvelope `json:"key_envelopes"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/v1/workspaces/"+workspaceID+"/key-envelopes", nil, &response, true); err != nil {
		return nil, err
	}
	return response.KeyEnvelopes, nil
}

// RotationEnvelope is one recipient's sealed copy of a freshly rotated
// workspace key.
type RotationEnvelope struct {
	UserID   string
	Envelope []byte
}

// RotateWorkspaceKey publishes a new current workspace key. The server
// rejects the call unless envelopes cover every currently active member
// (see server.Storage.RotateWorkspaceKey), so a caller must seal the new key
// to every active member, including itself, before calling this method.
func (h *HTTP) RotateWorkspaceKey(ctx context.Context, workspaceID string, keyID []byte, envelopes []RotationEnvelope) error {
	if len(keyID) == 0 || len(envelopes) == 0 {
		return errors.New("transport: invalid key rotation")
	}
	request := struct {
		KeyID     string `json:"key_id"`
		Envelopes []struct {
			UserID   string `json:"user_id"`
			Envelope []byte `json:"envelope"`
		} `json:"envelopes"`
	}{KeyID: hex.EncodeToString(keyID)}
	for _, envelope := range envelopes {
		request.Envelopes = append(request.Envelopes, struct {
			UserID   string `json:"user_id"`
			Envelope []byte `json:"envelope"`
		}{envelope.UserID, envelope.Envelope})
	}
	return h.doJSON(ctx, http.MethodPut, "/v1/workspaces/"+workspaceID+"/key-envelopes", request, nil, true)
}

// RemoteKeybag is the account-wide opaque keybag as the server stores it,
// versioned for optimistic compare-and-swap updates across this account's
// devices.
type RemoteKeybag struct {
	Version    int64  `json:"version"`
	Ciphertext []byte `json:"ciphertext"`
}

// ErrKeybagConflict reports that PutKeybag's expectedVersion no longer
// matches the server's stored version: another device updated the keybag
// first. The caller should GetKeybag, merge, and retry.
var ErrKeybagConflict = errors.New("transport: keybag version conflict")

// GetKeybag fetches this account's current opaque keybag from the server.
func (h *HTTP) GetKeybag(ctx context.Context) (RemoteKeybag, error) {
	var response RemoteKeybag
	if err := h.doJSON(ctx, http.MethodGet, "/v1/keybag", nil, &response, true); err != nil {
		return RemoteKeybag{}, err
	}
	return response, nil
}

// PutKeybag publishes a freshly re-encrypted keybag, replacing the version
// this device last observed. A conflict (another device updated first)
// reports ErrKeybagConflict and returns the server's current keybag so the
// caller can merge and retry.
func (h *HTTP) PutKeybag(ctx context.Context, expectedVersion int64, ciphertext []byte) (RemoteKeybag, error) {
	if expectedVersion <= 0 || len(ciphertext) == 0 {
		return RemoteKeybag{}, errors.New("transport: invalid keybag update")
	}
	request := struct {
		ExpectedVersion int64  `json:"expected_version"`
		Ciphertext      []byte `json:"ciphertext"`
	}{expectedVersion, ciphertext}
	if err := h.ensureSession(ctx, false); err != nil {
		return RemoteKeybag{}, err
	}
	h.mu.Lock()
	token := h.session
	h.mu.Unlock()
	status, err := h.requestJSON(ctx, http.MethodPut, "/v1/keybag", request, nil, token)
	if status == http.StatusConflict {
		var conflict struct {
			Current RemoteKeybag `json:"current"`
		}
		if _, retryErr := h.requestJSON(ctx, http.MethodGet, "/v1/keybag", nil, &conflict.Current, token); retryErr == nil {
			return conflict.Current, ErrKeybagConflict
		}
		return RemoteKeybag{}, ErrKeybagConflict
	}
	if err != nil {
		return RemoteKeybag{}, err
	}
	return RemoteKeybag{Version: expectedVersion + 1, Ciphertext: ciphertext}, nil
}
