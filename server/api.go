package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

type principalContextKey struct{}
type requestIDContextKey struct{}

type API struct {
	storage       *Storage
	config        Config
	pubsub        *PubSub
	ipLimiter     *RateLimiter
	deviceLimiter *RateLimiter
	metrics       *Metrics
	router        http.Handler
}

func NewAPI(storage *Storage, cfg Config) *API {
	api := &API{
		storage:       storage,
		config:        cfg,
		pubsub:        NewPubSub(256),
		ipLimiter:     NewRateLimiter(cfg.Limits.RequestsPerSecond, cfg.Limits.RequestBurst, 4096),
		deviceLimiter: NewRateLimiter(cfg.Limits.RequestsPerSecond, cfg.Limits.RequestBurst, 256),
		metrics:       &Metrics{},
	}
	api.router = api.routes()
	return api
}

func (a *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	a.router.ServeHTTP(writer, request)
}

func (a *API) routes() http.Handler {
	router := chi.NewRouter()
	router.Use(a.requestIDMiddleware)
	router.Use(a.recoveryMiddleware)
	router.Use(a.loggingMiddleware)
	router.Use(a.timeoutMiddleware)
	router.Use(a.ipRateLimitMiddleware)
	router.Get("/health", a.health)
	router.Route("/v1", func(v1 chi.Router) {
		v1.Post("/register", a.register)
		v1.Get("/auth/challenge", a.issueChallenge)
		v1.Post("/auth/verify", a.verifyChallenge)

		v1.Group(func(protected chi.Router) {
			protected.Use(a.authenticateMiddleware)
			protected.Use(a.deviceRateLimitMiddleware)
			protected.Post("/auth/refresh", a.refreshSession)
			protected.Get("/keybag", a.getKeybag)
			protected.Put("/keybag", a.putKeybag)
			protected.Get("/workspaces", a.listWorkspaces)
			protected.Post("/workspaces", a.createWorkspace)
			protected.Get("/workspaces/{workspaceID}/members", a.listMembers)
			protected.Post("/workspaces/{workspaceID}/members", a.addMember)
			protected.Delete("/workspaces/{workspaceID}/members/{userID}", a.revokeMember)
			protected.Get("/workspaces/{workspaceID}/key-envelopes", a.getKeyEnvelopes)
			protected.Put("/workspaces/{workspaceID}/key-envelopes", a.rotateWorkspaceKey)
			protected.Get("/devices", a.listDevices)
			protected.Post("/devices", a.addDevice)
			protected.Delete("/devices/{deviceID}", a.revokeDevice)
			protected.Post("/sync/ops", a.pushOperations)
			protected.Get("/sync/changes", a.pullChanges)
			protected.Get("/sync/stream", a.streamChanges)
			protected.Post("/blobs/init", a.beginBlob)
			protected.Put("/blobs/{blobID}/chunks/{index}", a.putBlobChunk)
			protected.Post("/blobs/{blobID}/complete", a.completeBlob)
			protected.Get("/blobs/{blobID}", a.getBlob)
			protected.Get("/blobs/{blobID}/chunks/{index}", a.getBlobChunk)
			protected.Put("/blobs/{blobID}/references/{referenceID}", a.addBlobReference)
			protected.Delete("/blobs/{blobID}/references/{referenceID}", a.removeBlobReference)
			protected.Post("/snapshots", a.putSnapshot)
			protected.Get("/snapshots", a.listSnapshots)
			protected.Get("/snapshots/latest", a.latestSnapshot)
			protected.Get("/snapshots/{snapshotID}", a.getSnapshot)
			protected.Post("/snapshots/{snapshotID}/ack", a.acknowledgeSnapshot)
		})
	})
	return router
}

func (a *API) health(writer http.ResponseWriter, request *http.Request) {
	var one int
	if err := a.storage.db.QueryRowContext(request.Context(), "SELECT 1").Scan(&one); err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"status": "ok", "schema": "server-v1"})
}

func (a *API) metricsHandler(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
	if err := a.metrics.WritePrometheus(writer); err != nil {
		slog.Warn("metrics response failed", "error_class", "write_failed")
	}
}

func (a *API) register(writer http.ResponseWriter, request *http.Request) {
	var input Registration
	if !decodeJSON(writer, request, a.config.Limits.MaxOperationBytes*6, &input) {
		return
	}
	result, err := a.storage.Register(request.Context(), input, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (a *API) issueChallenge(writer http.ResponseWriter, request *http.Request) {
	challenge, err := a.storage.IssueChallenge(request.Context(), request.URL.Query().Get("device_id"), request.URL.Query().Get("scope"), time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, challenge)
}

func (a *API) verifyChallenge(writer http.ResponseWriter, request *http.Request) {
	var proof ChallengeProof
	if !decodeJSON(writer, request, 16<<10, &proof) {
		return
	}
	session, err := a.storage.VerifyChallenge(request.Context(), proof, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, session)
}

func (a *API) refreshSession(writer http.ResponseWriter, request *http.Request) {
	principal := principalFrom(request.Context())
	var proof ChallengeProof
	if !decodeJSON(writer, request, 16<<10, &proof) {
		return
	}
	session, err := a.storage.RefreshSession(request.Context(), principal, proof, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, session)
}

func (a *API) getKeybag(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.GetKeybag(request.Context(), principalFrom(request.Context()))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *API) putKeybag(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ExpectedVersion int64  `json:"expected_version"`
		Ciphertext      []byte `json:"ciphertext"`
	}
	if !decodeJSON(writer, request, a.config.Limits.MaxOperationBytes*6, &input) {
		return
	}
	result, err := a.storage.PutKeybag(request.Context(), principalFrom(request.Context()), input.ExpectedVersion, input.Ciphertext, time.Now())
	if errors.Is(err, ErrConflict) {
		writeJSON(writer, http.StatusConflict, map[string]any{"code": "version_conflict", "current": result})
		return
	}
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *API) listWorkspaces(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.ListWorkspaces(request.Context(), principalFrom(request.Context()))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"workspaces": result})
}

func (a *API) createWorkspace(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID string `json:"workspace_id"`
		KeyID       string `json:"key_id"`
		Envelope    []byte `json:"envelope"`
	}
	if !decodeJSON(writer, request, a.config.Limits.MaxOperationBytes, &input) {
		return
	}
	result, err := a.storage.CreateWorkspace(request.Context(), principalFrom(request.Context()), input.WorkspaceID, input.KeyID, input.Envelope, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (a *API) listMembers(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.ListMembers(request.Context(), principalFrom(request.Context()), chi.URLParam(request, "workspaceID"))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"members": result})
}

func (a *API) addMember(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		UserID   string `json:"user_id"`
		KeyID    string `json:"key_id"`
		Envelope []byte `json:"envelope"`
	}
	if !decodeJSON(writer, request, a.config.Limits.MaxOperationBytes, &input) {
		return
	}
	workspaceID := chi.URLParam(request, "workspaceID")
	err := a.storage.AddMember(request.Context(), principalFrom(request.Context()), workspaceID, input.UserID, input.KeyID, input.Envelope, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	a.publishWorkspace(request.Context(), workspaceID)
	writeJSON(writer, http.StatusCreated, map[string]any{"status": "created"})
}

func (a *API) revokeMember(writer http.ResponseWriter, request *http.Request) {
	workspaceID := chi.URLParam(request, "workspaceID")
	err := a.storage.RevokeMember(request.Context(), principalFrom(request.Context()), workspaceID, chi.URLParam(request, "userID"), time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	a.publishWorkspace(request.Context(), workspaceID)
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) getKeyEnvelopes(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.GetKeyEnvelopes(request.Context(), principalFrom(request.Context()), chi.URLParam(request, "workspaceID"))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"key_envelopes": result})
}

func (a *API) rotateWorkspaceKey(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		KeyID     string             `json:"key_id"`
		Envelopes []KeyEnvelopeInput `json:"envelopes"`
	}
	if !decodeJSON(writer, request, a.config.Limits.MaxOperationBytes*6, &input) {
		return
	}
	workspaceID := chi.URLParam(request, "workspaceID")
	if err := a.storage.RotateWorkspaceKey(request.Context(), principalFrom(request.Context()), workspaceID, input.KeyID, input.Envelopes, time.Now()); err != nil {
		writeAPIError(writer, err)
		return
	}
	a.publishWorkspace(request.Context(), workspaceID)
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) listDevices(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.ListDevices(request.Context(), principalFrom(request.Context()))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"devices": result})
}

func (a *API) addDevice(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		DeviceID    string `json:"device_id"`
		DisplayName string `json:"display_name"`
		PublicKey   []byte `json:"signing_public"`
	}
	if !decodeJSON(writer, request, 16<<10, &input) {
		return
	}
	if err := a.storage.AddDevice(request.Context(), principalFrom(request.Context()), input.DeviceID, input.DisplayName, input.PublicKey, time.Now()); err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"device_id": input.DeviceID})
}

func (a *API) revokeDevice(writer http.ResponseWriter, request *http.Request) {
	principal := principalFrom(request.Context())
	workspaces, _ := a.storage.ListWorkspaces(request.Context(), principal)
	if err := a.storage.RevokeDevice(request.Context(), principal, chi.URLParam(request, "deviceID"), time.Now()); err != nil {
		writeAPIError(writer, err)
		return
	}
	for _, workspace := range workspaces {
		a.publishWorkspace(request.Context(), workspace.ID)
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) pushOperations(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Operations []Operation `json:"operations"`
	}
	maxBody := min(int64(64<<20), a.config.Limits.MaxOperationBytes*int64(a.config.Limits.MaxOperationsPerBatch)*2)
	if !decodeJSON(writer, request, maxBody, &input) {
		return
	}
	result, err := a.storage.PushOperations(request.Context(), principalFrom(request.Context()), input.Operations, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	seen := make(map[string]bool)
	for _, operation := range input.Operations {
		if !seen[operation.WorkspaceID] {
			a.publishWorkspace(request.Context(), operation.WorkspaceID)
			seen[operation.WorkspaceID] = true
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"accepted": result})
}

func (a *API) pullChanges(writer http.ResponseWriter, request *http.Request) {
	cursor, err := parseIntQuery(request, "cursor", 0)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	limit, err := parseIntQuery(request, "limit", int64(min(100, a.config.Limits.MaxOperationsPerBatch)))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	result, err := a.storage.PullChanges(request.Context(), principalFrom(request.Context()), request.URL.Query().Get("workspace_id"), cursor, int(limit))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *API) beginBlob(writer http.ResponseWriter, request *http.Request) {
	var input BlobInit
	if !decodeJSON(writer, request, a.config.Limits.MaxOperationBytes*2, &input) {
		return
	}
	result, err := a.storage.BeginBlob(request.Context(), principalFrom(request.Context()).UserID, input, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, result)
}

func (a *API) putBlobChunk(writer http.ResponseWriter, request *http.Request) {
	workspaceID := request.URL.Query().Get("workspace_id")
	index, err := strconv.Atoi(chi.URLParam(request, "index"))
	if err != nil {
		writeAPIError(writer, ErrInvalid)
		return
	}
	contents, err := readLimitedBody(request.Body, a.config.Limits.BlobChunkBytes+65)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	if err := a.storage.PutBlobChunk(request.Context(), principalFrom(request.Context()).UserID, workspaceID, chi.URLParam(request, "blobID"), index, contents, time.Now()); err != nil {
		writeAPIError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) completeBlob(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.CompleteBlob(request.Context(), principalFrom(request.Context()).UserID,
		request.URL.Query().Get("workspace_id"), chi.URLParam(request, "blobID"), time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *API) getBlob(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.GetBlob(request.Context(), principalFrom(request.Context()).UserID,
		request.URL.Query().Get("workspace_id"), chi.URLParam(request, "blobID"))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *API) getBlobChunk(writer http.ResponseWriter, request *http.Request) {
	index, err := strconv.Atoi(chi.URLParam(request, "index"))
	if err != nil {
		writeAPIError(writer, ErrInvalid)
		return
	}
	contents, err := a.storage.ReadBlobChunk(request.Context(), principalFrom(request.Context()).UserID,
		request.URL.Query().Get("workspace_id"), chi.URLParam(request, "blobID"), index)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	writer.Header().Set("Content-Length", strconv.Itoa(len(contents)))
	writer.Write(contents)
}

func (a *API) addBlobReference(writer http.ResponseWriter, request *http.Request) {
	a.setBlobReference(writer, request, true)
}

func (a *API) removeBlobReference(writer http.ResponseWriter, request *http.Request) {
	a.setBlobReference(writer, request, false)
}

func (a *API) setBlobReference(writer http.ResponseWriter, request *http.Request, referenced bool) {
	err := a.storage.SetBlobReferenced(request.Context(), principalFrom(request.Context()).UserID,
		request.URL.Query().Get("workspace_id"), chi.URLParam(request, "blobID"), chi.URLParam(request, "referenceID"), referenced, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) putSnapshot(writer http.ResponseWriter, request *http.Request) {
	var input Snapshot
	maxSnapshotCiphertext := min(a.config.Limits.MaxBlobBytes, int64(corecrypto.MaxSnapshotCiphertextBytes))
	maxSnapshotBody := maxSnapshotCiphertext + maxSnapshotCiphertext/2 + (1 << 20)
	if !decodeJSON(writer, request, maxSnapshotBody, &input) {
		return
	}
	if err := a.storage.PutSnapshot(request.Context(), principalFrom(request.Context()), input, time.Now()); err != nil {
		writeAPIError(writer, err)
		return
	}
	a.publishWorkspace(request.Context(), input.WorkspaceID)
	writeJSON(writer, http.StatusCreated, map[string]any{"snapshot_id": input.ID})
}

func (a *API) listSnapshots(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.ListSnapshots(request.Context(), principalFrom(request.Context()), request.URL.Query().Get("workspace_id"))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"snapshots": result})
}

func (a *API) latestSnapshot(writer http.ResponseWriter, request *http.Request) {
	list, err := a.storage.ListSnapshots(request.Context(), principalFrom(request.Context()), request.URL.Query().Get("workspace_id"))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	for _, item := range list {
		if item.EligibleAt != nil {
			full, err := a.storage.GetSnapshot(request.Context(), principalFrom(request.Context()), item.ID)
			if err != nil {
				writeAPIError(writer, err)
				return
			}
			writeJSON(writer, http.StatusOK, full)
			return
		}
	}
	writeAPIError(writer, ErrNotFound)
}

func (a *API) getSnapshot(writer http.ResponseWriter, request *http.Request) {
	result, err := a.storage.GetSnapshot(request.Context(), principalFrom(request.Context()), chi.URLParam(request, "snapshotID"))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *API) acknowledgeSnapshot(writer http.ResponseWriter, request *http.Request) {
	var input SnapshotAck
	if !decodeJSON(writer, request, 16<<10, &input) {
		return
	}
	if input.SnapshotID != chi.URLParam(request, "snapshotID") {
		writeAPIError(writer, ErrInvalid)
		return
	}
	eligible, err := a.storage.AcknowledgeSnapshot(request.Context(), principalFrom(request.Context()), input, time.Now())
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	a.publishWorkspace(request.Context(), input.WorkspaceID)
	writeJSON(writer, http.StatusOK, map[string]any{"eligible_for_compaction": eligible})
}

func (a *API) publishWorkspace(ctx context.Context, workspaceID string) {
	var latest, epoch int64
	if err := a.storage.db.QueryRowContext(ctx, `SELECT latest_seq, cursor_epoch FROM workspaces WHERE workspace_id = ?`, workspaceID).
		Scan(&latest, &epoch); err == nil {
		a.pubsub.Publish(CursorHint{Protocol: "beresta.sync.v1", WorkspaceID: workspaceID, LatestSeq: latest, CursorEpoch: epoch})
	}
}

func (a *API) streamChanges(writer http.ResponseWriter, request *http.Request) {
	workspaceID := request.URL.Query().Get("workspace_id")
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		writeAPIError(writer, err)
		return
	}
	principal := principalFrom(request.Context())
	member, err := a.storage.isActiveMember(request.Context(), a.storage.db, principal.UserID, workspaceID)
	if err != nil || !member {
		writeAPIError(writer, ErrForbidden)
		return
	}
	subscription, err := a.pubsub.Subscribe(workspaceID)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	defer subscription.Close()
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 5 * time.Second,
		CheckOrigin: func(candidate *http.Request) bool {
			origin := candidate.Header.Get("Origin")
			if origin == "" {
				return true
			}
			parsed, err := url.Parse(origin)
			return err == nil && strings.EqualFold(parsed.Host, candidate.Host)
		},
	}
	connection, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	a.metrics.activeWS.Add(1)
	defer a.metrics.activeWS.Add(-1)
	connection.SetReadLimit(1024)
	_ = connection.SetReadDeadline(time.Time{})
	_ = connection.SetWriteDeadline(time.Time{})
	disconnected := make(chan struct{})
	go func() {
		defer close(disconnected)
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case hint, ok := <-subscription.C:
			if !ok {
				return
			}
			connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := connection.WriteJSON(hint); err != nil {
				return
			}
			_ = connection.SetWriteDeadline(time.Time{})
		case <-ticker.C:
			connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
			_ = connection.SetWriteDeadline(time.Time{})
		case <-disconnected:
			return
		case <-request.Context().Done():
			return
		}
	}
}

func (a *API) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			writeAPIError(writer, err)
			return
		}
		requestID := hex.EncodeToString(bytes)
		writer.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, requestID)))
	})
}

func (a *API) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				a.metrics.errors.Add(1)
				slog.Error("request panic", "request_id", requestIDFrom(request.Context()), "error_class", "panic")
				writeJSON(writer, http.StatusInternalServerError, map[string]string{"code": "internal_error"})
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func (a *API) timeoutMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/sync/stream") {
			next.ServeHTTP(writer, request)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (a *API) ipRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		host, _, err := net.SplitHostPort(request.RemoteAddr)
		if err != nil {
			host = request.RemoteAddr
		}
		if !a.ipLimiter.Allow(host, time.Now()) {
			writeAPIError(writer, ErrRateLimited)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (a *API) authenticateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || strings.Contains(strings.TrimPrefix(header, "Bearer "), " ") {
			writeAPIError(writer, ErrUnauthorized)
			return
		}
		principal, err := a.storage.Authenticate(request.Context(), strings.TrimPrefix(header, "Bearer "), time.Now())
		if err != nil {
			writeAPIError(writer, err)
			return
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), principalContextKey{}, principal)))
	})
}

func (a *API) deviceRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !a.deviceLimiter.Allow(principalFrom(request.Context()).DeviceID, time.Now()) {
			writeAPIError(writer, ErrRateLimited)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type responseCapture struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (capture *responseCapture) WriteHeader(status int) {
	if capture.status == 0 {
		capture.status = status
	}
	capture.ResponseWriter.WriteHeader(status)
}

func (capture *responseCapture) Write(contents []byte) (int, error) {
	if capture.status == 0 {
		capture.status = http.StatusOK
	}
	written, err := capture.ResponseWriter.Write(contents)
	capture.bytes += int64(written)
	return written, err
}

func (capture *responseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := capture.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response does not support hijacking")
	}
	return hijacker.Hijack()
}

func (a *API) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		capture := &responseCapture{ResponseWriter: writer}
		a.metrics.requests.Add(1)
		next.ServeHTTP(capture, request)
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 400 {
			a.metrics.errors.Add(1)
		}
		route := request.Method + " " + chi.RouteContext(request.Context()).RoutePattern()
		slog.Info("request complete", "request_id", requestIDFrom(request.Context()), "route", route,
			"status", status, "duration_ms", time.Since(started).Milliseconds(), "response_bytes", capture.bytes)
	})
}

func principalFrom(ctx context.Context) Principal {
	principal, _ := ctx.Value(principalContextKey{}).(Principal)
	return principal
}

func requestIDFrom(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, limit int64, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, limit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeAPIError(writer, fmt.Errorf("%w: malformed JSON body", ErrInvalid))
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(writer, fmt.Errorf("%w: request must contain one JSON value", ErrInvalid))
		return false
	}
	return true
}

func readLimitedBody(reader io.Reader, limit int64) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, fmt.Errorf("%w: request body is too large", ErrInvalid)
	}
	return contents, nil
}

func parseIntQuery(request *http.Request, name string, fallback int64) (int64, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s is not an integer", ErrInvalid, name)
	}
	return parsed, nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Warn("response encoding failed", "error_class", "encode_failed")
	}
}

func writeAPIError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrUnauthorized):
		status, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, ErrQuota):
		status, code = http.StatusRequestEntityTooLarge, "quota_exceeded"
	case errors.Is(err, ErrRateLimited):
		status, code = http.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = http.StatusGatewayTimeout, "timeout"
	}
	writeJSON(writer, status, map[string]string{"code": code})
}
