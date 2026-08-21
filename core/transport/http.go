package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/gorilla/websocket"
)

const (
	maxHTTPResponseBytes = 64 << 20
	authScopeSync        = "sync"
	authSignatureDomain  = "beresta.auth.v1"
)

var (
	ErrCertificatePin = errors.New("transport: server certificate fingerprint mismatch")
	ErrAuthentication = errors.New("transport: device authentication failed")
	ErrPermanent      = errors.New("transport: permanent server rejection")
	ErrNotFound       = errors.New("transport: remote object not found")
)

type HTTPSecurityMode string

const (
	HTTPSecurityPinned  HTTPSecurityMode = "pinned"
	HTTPSecurityTrusted HTTPSecurityMode = "trusted"
)

type HTTPConfig struct {
	BaseURL               string
	SecurityMode          HTTPSecurityMode
	PinnedFingerprint     string
	RootCAs               *x509.CertPool
	DeviceID              model.ID
	SignChallenge         func([]byte) ([]byte, error)
	RequestTimeout        time.Duration
	MaxOperationBytes     int
	MaxOperationsPerBatch int
}

type HTTP struct {
	base       *url.URL
	client     *http.Client
	tlsConfig  *tls.Config
	config     HTTPConfig
	mu         sync.Mutex
	session    string
	sessionEnd time.Time
	status     Status
}

func NewHTTP(config HTTPConfig) (*HTTP, error) {
	base, err := url.Parse(config.BaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("transport: server URL must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	if err := config.DeviceID.Validate(); err != nil || config.SignChallenge == nil {
		return nil, errors.New("transport: valid device credentials are required")
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 30 * time.Second
	}
	if config.MaxOperationBytes <= 0 || config.MaxOperationBytes > coresync.HardMaxOperationBytes {
		config.MaxOperationBytes = coresync.DefaultMaxOperationBytes
	}
	if config.MaxOperationsPerBatch <= 0 || config.MaxOperationsPerBatch > 256 {
		config.MaxOperationsPerBatch = 100
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: config.RootCAs}
	switch config.SecurityMode {
	case HTTPSecurityPinned:
		pin, err := normalizeFingerprint(config.PinnedFingerprint)
		if err != nil {
			return nil, err
		}
		// Verification is replaced by the explicit SHA-256 leaf certificate pin.
		// TLS still authenticates the handshake transcript and enforces TLS 1.3.
		tlsConfig.InsecureSkipVerify = true //nolint:gosec -- pinned verification below is the trust decision.
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return ErrCertificatePin
			}
			digest := sha256.Sum256(state.PeerCertificates[0].Raw)
			if !constantHexEqual(hex.EncodeToString(digest[:]), pin) {
				return ErrCertificatePin
			}
			return nil
		}
	case HTTPSecurityTrusted:
		// Standard hostname and public/private CA verification remains enabled.
	default:
		return nil, errors.New("transport: select pinned or trusted certificate mode")
	}
	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: config.RequestTimeout,
	}
	return &HTTP{
		base: base, client: &http.Client{Transport: transport, Timeout: config.RequestTimeout},
		tlsConfig: tlsConfig, config: config, status: StatusOffline,
	}, nil
}

func (h *HTTP) Status(context.Context) Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *HTTP) Pull(ctx context.Context, workspaceID model.ID, cursor coresync.Cursor, limit int) (coresync.PullPage, error) {
	if limit <= 0 || limit > h.config.MaxOperationsPerBatch {
		return coresync.PullPage{}, errors.New("transport: invalid pull limit")
	}
	query := url.Values{
		"workspace_id": {workspaceID.String()},
		"cursor":       {strconv.FormatUint(cursor.LastSequence, 10)},
		"limit":        {strconv.Itoa(limit)},
	}
	var response operationChangesJSON
	if err := h.doJSON(ctx, http.MethodGet, "/v1/sync/changes?"+query.Encode(), nil, &response, true); err != nil {
		return coresync.PullPage{}, err
	}
	if response.WorkspaceID != workspaceID.String() || response.Cursor < 0 || response.CursorEpoch <= 0 || response.CursorEpoch > int64(^uint32(0)) {
		return coresync.PullPage{}, coresync.ErrInvalidCursor
	}
	operations := make([]coresync.WireOperation, len(response.Operations))
	for index, item := range response.Operations {
		operation, err := item.toWire()
		if err != nil {
			return coresync.PullPage{}, err
		}
		if len(operation.Ciphertext) > h.config.MaxOperationBytes {
			return coresync.PullPage{}, coresync.ErrOperationSizeExceeded
		}
		operations[index] = operation
	}
	return coresync.PullPage{
		Cursor:     coresync.Cursor{WorkspaceID: workspaceID, LastSequence: uint64(response.Cursor), Epoch: uint32(response.CursorEpoch)},
		Operations: operations,
		More:       len(operations) == limit,
	}, nil
}

func (h *HTTP) Push(ctx context.Context, workspaceID model.ID, operations []coresync.WireOperation) ([]coresync.PushResult, error) {
	if len(operations) == 0 || len(operations) > h.config.MaxOperationsPerBatch {
		return nil, errors.New("transport: invalid push batch")
	}
	request := struct {
		Operations []operationJSON `json:"operations"`
	}{Operations: make([]operationJSON, len(operations))}
	for index, operation := range operations {
		if operation.WorkspaceID != workspaceID || operation.Sequence != 0 || len(operation.Ciphertext) > h.config.MaxOperationBytes {
			return nil, errors.New("transport: invalid local operation")
		}
		request.Operations[index] = operationFromWire(operation)
	}
	var response struct {
		Accepted []struct {
			OpID      string `json:"op_id"`
			Sequence  int64  `json:"seq"`
			Duplicate bool   `json:"duplicate"`
		} `json:"accepted"`
	}
	if err := h.doJSON(ctx, http.MethodPost, "/v1/sync/ops", request, &response, true); err != nil {
		return nil, err
	}
	results := make([]coresync.PushResult, len(response.Accepted))
	for index, accepted := range response.Accepted {
		id, err := parseCanonicalID(accepted.OpID)
		if err != nil || accepted.Sequence <= 0 {
			return nil, errors.New("transport: malformed push response")
		}
		results[index] = coresync.PushResult{OpID: id, Sequence: uint64(accepted.Sequence), Duplicate: accepted.Duplicate}
	}
	return results, nil
}

func (h *HTTP) Subscribe(ctx context.Context, workspaceID model.ID) (<-chan coresync.Cursor, error) {
	if err := h.ensureSession(ctx, false); err != nil {
		return nil, err
	}
	h.mu.Lock()
	token := h.session
	h.mu.Unlock()
	endpoint := *h.base
	endpoint.Scheme = "wss"
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/sync/stream"
	endpoint.RawQuery = url.Values{"workspace_id": {workspaceID.String()}}.Encode()
	dialer := websocket.Dialer{TLSClientConfig: h.tlsConfig.Clone(), HandshakeTimeout: h.config.RequestTimeout}
	headers := http.Header{"Authorization": {"Bearer " + token}}
	connection, response, err := dialer.DialContext(ctx, endpoint.String(), headers)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, err
	}
	output := make(chan coresync.Cursor, 1)
	go func() {
		defer close(output)
		defer connection.Close()
		closed := make(chan struct{})
		defer close(closed)
		go func() {
			select {
			case <-ctx.Done():
				_ = connection.Close()
			case <-closed:
			}
		}()
		for {
			var hint struct {
				Protocol       string `json:"protocol"`
				WorkspaceID    string `json:"workspace_id"`
				LatestSequence int64  `json:"latest_seq"`
				CursorEpoch    int64  `json:"cursor_epoch"`
			}
			if err := connection.ReadJSON(&hint); err != nil {
				return
			}
			if hint.Protocol != coresync.ProtocolV1 || hint.WorkspaceID != workspaceID.String() || hint.LatestSequence < 0 || hint.CursorEpoch <= 0 {
				continue
			}
			cursor := coresync.Cursor{WorkspaceID: workspaceID, LastSequence: uint64(hint.LatestSequence), Epoch: uint32(hint.CursorEpoch)}
			select {
			case output <- cursor:
			default:
				select {
				case <-output:
				default:
				}
				select {
				case output <- cursor:
				default:
				}
			}
		}
	}()
	return output, nil
}

func (h *HTTP) doJSON(ctx context.Context, method, path string, input, output any, authenticated bool) error {
	for attempt := 0; attempt < 2; attempt++ {
		var token string
		if authenticated {
			if err := h.ensureSession(ctx, false); err != nil {
				return err
			}
			h.mu.Lock()
			token = h.session
			h.mu.Unlock()
		}
		status, err := h.requestJSON(ctx, method, path, input, output, token)
		if status == http.StatusUnauthorized && authenticated && attempt == 0 {
			h.clearSession()
			continue
		}
		if err != nil {
			return err
		}
		h.setStatus(StatusCurrent)
		return nil
	}
	return ErrAuthentication
}

func (h *HTTP) requestJSON(ctx context.Context, method, path string, input, output any, token string) (int, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, h.resolve(path), body)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := h.client.Do(request)
	if err != nil {
		h.setStatus(StatusOffline)
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		h.setStatus(StatusFailed)
		return response.StatusCode, decodeHTTPError(response)
	}
	if output != nil {
		decoder := json.NewDecoder(io.LimitReader(response.Body, maxHTTPResponseBytes+1))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(output); err != nil {
			return response.StatusCode, fmt.Errorf("transport: decode response: %w", err)
		}
	}
	return response.StatusCode, nil
}

func (h *HTTP) ensureSession(ctx context.Context, forceRefresh bool) error {
	h.mu.Lock()
	if h.session != "" && !forceRefresh && time.Until(h.sessionEnd) > time.Minute {
		h.mu.Unlock()
		return nil
	}
	oldToken := h.session
	h.mu.Unlock()
	challengePath := "/v1/auth/challenge?" + url.Values{"device_id": {h.config.DeviceID.String()}, "scope": {authScopeSync}}.Encode()
	var challenge challengeJSON
	if _, err := h.requestJSON(ctx, http.MethodGet, challengePath, nil, &challenge, ""); err != nil {
		return err
	}
	if challenge.DeviceID != h.config.DeviceID.String() || challenge.Scope != authScopeSync || len(challenge.Nonce) == 0 {
		return ErrAuthentication
	}
	if h.config.SecurityMode == HTTPSecurityPinned {
		pin, _ := normalizeFingerprint(h.config.PinnedFingerprint)
		serverPin, err := normalizeFingerprint(challenge.ServerFingerprint)
		if err != nil || !constantHexEqual(pin, serverPin) {
			return ErrCertificatePin
		}
	}
	proof := challengeProofJSON{
		ChallengeID: challenge.ID, DeviceID: challenge.DeviceID, ServerFingerprint: challenge.ServerFingerprint,
		Nonce: challenge.Nonce, Scope: challenge.Scope,
	}
	signature, err := h.config.SignChallenge(authSignatureInput(proof))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrAuthentication
	}
	proof.Signature = signature
	path := "/v1/auth/verify"
	authenticated := false
	if oldToken != "" {
		path = "/v1/auth/refresh"
		authenticated = true
	}
	var session sessionJSON
	token := ""
	if authenticated {
		token = oldToken
	}
	status, requestErr := h.requestJSON(ctx, http.MethodPost, path, proof, &session, token)
	if requestErr != nil {
		if authenticated {
			h.clearSession()
			if status == http.StatusUnauthorized {
				return h.ensureSession(ctx, false)
			}
		}
		return requestErr
	}
	if session.Token == "" || session.DeviceID != h.config.DeviceID.String() || session.Scope != authScopeSync || !session.ExpiresAt.After(time.Now()) {
		return ErrAuthentication
	}
	h.mu.Lock()
	h.session, h.sessionEnd, h.status = session.Token, session.ExpiresAt, StatusCurrent
	h.mu.Unlock()
	return nil
}

func (h *HTTP) resolve(path string) string {
	base := strings.TrimRight(h.base.String(), "/")
	return base + path
}

func (h *HTTP) clearSession() {
	h.mu.Lock()
	h.session, h.sessionEnd = "", time.Time{}
	h.mu.Unlock()
}

func (h *HTTP) setStatus(status Status) {
	h.mu.Lock()
	h.status = status
	h.mu.Unlock()
}

type challengeJSON struct {
	ID                string    `json:"challenge_id"`
	DeviceID          string    `json:"device_id"`
	ServerFingerprint string    `json:"server_fingerprint"`
	Nonce             []byte    `json:"nonce"`
	Scope             string    `json:"scope"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type challengeProofJSON struct {
	ChallengeID       string `json:"challenge_id"`
	DeviceID          string `json:"device_id"`
	ServerFingerprint string `json:"server_fingerprint"`
	Nonce             []byte `json:"nonce"`
	Scope             string `json:"scope"`
	Signature         []byte `json:"signature"`
}

type sessionJSON struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	DeviceID  string    `json:"device_id"`
	Scope     string    `json:"scope"`
	ExpiresAt time.Time `json:"expires_at"`
}

type operationJSON struct {
	Protocol      string `json:"protocol"`
	SchemaVersion int    `json:"schema_version"`
	OpID          string `json:"op_id"`
	WorkspaceID   string `json:"workspace_id"`
	DeviceID      string `json:"device_id"`
	Sequence      int64  `json:"seq,omitempty"`
	HLCPhysicalMS int64  `json:"hlc_physical_ms"`
	HLCLogical    uint32 `json:"hlc_logical"`
	KeyID         string `json:"key_id"`
	Nonce         []byte `json:"nonce"`
	Ciphertext    []byte `json:"ciphertext"`
	Signature     []byte `json:"sig"`
}

type operationChangesJSON struct {
	WorkspaceID string          `json:"workspace_id"`
	Cursor      int64           `json:"cursor"`
	CursorEpoch int64           `json:"cursor_epoch"`
	Operations  []operationJSON `json:"operations"`
}

func operationFromWire(operation coresync.WireOperation) operationJSON {
	return operationJSON{
		Protocol: coresync.ProtocolV1, SchemaVersion: int(coresync.SchemaVersionV1), OpID: operation.OpID.String(),
		WorkspaceID: operation.WorkspaceID.String(), DeviceID: operation.DeviceID.String(), Sequence: int64(operation.Sequence),
		HLCPhysicalMS: int64(operation.Clock.PhysicalMS), HLCLogical: operation.Clock.Logical,
		KeyID: hex.EncodeToString(operation.KeyID), Nonce: operation.Nonce, Ciphertext: operation.Ciphertext, Signature: operation.Signature,
	}
}

func (operation operationJSON) toWire() (coresync.WireOperation, error) {
	if operation.Protocol != coresync.ProtocolV1 || operation.SchemaVersion != int(coresync.SchemaVersionV1) || operation.Sequence <= 0 || operation.HLCPhysicalMS < 0 {
		return coresync.WireOperation{}, coresync.ErrUnsupportedVersion
	}
	opID, err := parseCanonicalID(operation.OpID)
	if err != nil {
		return coresync.WireOperation{}, err
	}
	workspaceID, err := parseCanonicalID(operation.WorkspaceID)
	if err != nil {
		return coresync.WireOperation{}, err
	}
	deviceID, err := parseCanonicalID(operation.DeviceID)
	if err != nil {
		return coresync.WireOperation{}, err
	}
	keyID, err := hex.DecodeString(operation.KeyID)
	if err != nil || len(keyID) != 16 || strings.ToLower(operation.KeyID) != operation.KeyID {
		return coresync.WireOperation{}, errors.New("transport: malformed key identifier")
	}
	result := coresync.WireOperation{
		OpID: opID, WorkspaceID: workspaceID, DeviceID: deviceID, Sequence: uint64(operation.Sequence),
		Clock: model.HLC{PhysicalMS: uint64(operation.HLCPhysicalMS), Logical: operation.HLCLogical, DeviceID: deviceID},
		KeyID: keyID, Nonce: operation.Nonce, Ciphertext: operation.Ciphertext, Signature: operation.Signature,
	}
	if _, err := coresync.EncodeOperation(result); err != nil {
		return coresync.WireOperation{}, err
	}
	return result, nil
}

func parseCanonicalID(value string) (model.ID, error) {
	id, err := model.ParseIDString(value)
	if err != nil {
		return model.Nil, fmt.Errorf("transport: invalid identifier: %w", err)
	}
	return id, nil
}

func authSignatureInput(proof challengeProofJSON) []byte {
	fields := [][]byte{[]byte(authSignatureDomain), []byte(proof.ChallengeID), []byte(proof.DeviceID), []byte(proof.ServerFingerprint), []byte(proof.Scope), proof.Nonce}
	var result []byte
	for _, field := range fields {
		length := uint32(len(field))
		result = append(result, byte(length>>24), byte(length>>16), byte(length>>8), byte(length))
		result = append(result, field...)
	}
	return result
}

func normalizeFingerprint(value string) (string, error) {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), ":", ""))
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("transport: fingerprint must be a SHA-256 digest")
	}
	return value, nil
}

func constantHexEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func decodeHTTPError(response *http.Response) error {
	limited, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(limited, &payload)
	if payload.Code == "" {
		payload.Code = http.StatusText(response.StatusCode)
	}
	if payload.Code == "snapshot_required" {
		return coresync.ErrSnapshotRequired
	}
	if response.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return fmt.Errorf("transport: transient HTTP %d: %s", response.StatusCode, payload.Code)
	}
	return fmt.Errorf("%w: HTTP %d: %s", ErrPermanent, response.StatusCode, payload.Code)
}

var _ coresync.OperationTransport = (*HTTP)(nil)
var _ coresync.CursorSubscriber = (*HTTP)(nil)
var _ SyncTransport = (*HTTP)(nil)
