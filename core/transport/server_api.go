package transport

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/beresta-app/beresta/core/account"
)

type RegistrationRequest struct {
	InviteCode string
	DeviceName string
	Data       account.ServerRegistration
}

func (h *HTTP) Register(ctx context.Context, request RegistrationRequest) error {
	if request.InviteCode == "" || request.DeviceName == "" || len(request.DeviceName) > 200 {
		return errors.New("transport: invite and device name are required")
	}
	payload := struct {
		InviteCode        string `json:"invite_code"`
		UserID            string `json:"user_id"`
		IdentityPublic    []byte `json:"identity_public"`
		AuthorityPublic   []byte `json:"authority_public"`
		DeviceID          string `json:"device_id"`
		DeviceName        string `json:"device_name"`
		SigningPublic     []byte `json:"signing_public"`
		WorkspaceID       string `json:"workspace_id"`
		WorkspaceKeyID    string `json:"workspace_key_id"`
		WorkspaceEnvelope []byte `json:"workspace_envelope"`
		KeybagCiphertext  []byte `json:"keybag_ciphertext"`
	}{
		request.InviteCode, request.Data.UserID.String(), request.Data.IdentityPublic, request.Data.AuthorityPublic,
		request.Data.DeviceID.String(), request.DeviceName, request.Data.SigningPublic, request.Data.WorkspaceID.String(),
		hex.EncodeToString(request.Data.WorkspaceKeyID), request.Data.WorkspaceEnvelope, request.Data.KeybagCiphertext,
	}
	var response struct {
		UserID      string `json:"user_id"`
		DeviceID    string `json:"device_id"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := h.doJSON(ctx, http.MethodPost, "/v1/register", payload, &response, false); err != nil {
		return err
	}
	if response.UserID != request.Data.UserID.String() || response.DeviceID != request.Data.DeviceID.String() || response.WorkspaceID != request.Data.WorkspaceID.String() {
		return errors.New("transport: registration response identity mismatch")
	}
	return nil
}

type Diagnostics struct {
	Reachable     bool   `json:"reachable"`
	TLS13         bool   `json:"tls_1_3"`
	Authenticated bool   `json:"authenticated"`
	LatencyMS     int64  `json:"latency_ms"`
	ErrorClass    string `json:"error_class,omitempty"`
}

func (h *HTTP) Diagnose(ctx context.Context) Diagnostics {
	started := time.Now()
	var health struct {
		Status string `json:"status"`
		Schema string `json:"schema"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/health", nil, &health, false); err != nil {
		return Diagnostics{LatencyMS: time.Since(started).Milliseconds(), ErrorClass: classifyTransportError(err)}
	}
	result := Diagnostics{Reachable: health.Status == "ok", TLS13: true, LatencyMS: time.Since(started).Milliseconds()}
	if err := h.ensureSession(ctx, false); err != nil {
		result.ErrorClass = classifyTransportError(err)
		return result
	}
	result.Authenticated = true
	return result
}

type RemoteDevice struct {
	ID            string     `json:"device_id"`
	UserID        string     `json:"user_id"`
	DisplayName   string     `json:"display_name"`
	SigningPublic []byte     `json:"signing_public"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

func (h *HTTP) ListDevices(ctx context.Context) ([]RemoteDevice, error) {
	var response struct {
		Devices []RemoteDevice `json:"devices"`
	}
	if err := h.doJSON(ctx, http.MethodGet, "/v1/devices", nil, &response, true); err != nil {
		return nil, err
	}
	return response.Devices, nil
}

func (h *HTTP) RevokeDevice(ctx context.Context, deviceID string) error {
	if _, err := parseCanonicalID(deviceID); err != nil {
		return err
	}
	return h.doJSON(ctx, http.MethodDelete, "/v1/devices/"+deviceID, nil, nil, true)
}

func classifyTransportError(err error) string {
	switch {
	case errors.Is(err, ErrCertificatePin):
		return "certificate_mismatch"
	case errors.Is(err, ErrAuthentication):
		return "authentication_failed"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "unreachable"
	}
}
