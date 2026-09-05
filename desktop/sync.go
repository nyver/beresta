package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/transport"
)

type ConnectServerRequest struct {
	URL          string `json:"url"`
	InviteCode   string `json:"invite_code"`
	Fingerprint  string `json:"fingerprint"`
	SecurityMode string `json:"security_mode"`
	QRCode       string `json:"qr_code"`
	DeviceName   string `json:"device_name"`
}

type ServerConnectionInfo struct {
	Enabled      bool                  `json:"enabled"`
	URL          string                `json:"url"`
	Protocol     string                `json:"protocol"`
	SecurityMode string                `json:"security_mode"`
	Fingerprint  string                `json:"fingerprint,omitempty"`
	Diagnostics  transport.Diagnostics `json:"diagnostics"`
}

type SyncQuarantineDTO struct {
	OperationID    string `json:"operation_id"`
	Sequence       uint64 `json:"sequence"`
	Reason         string `json:"reason"`
	ReceivedUnixMS int64  `json:"received_unix_ms"`
}

func (a *App) ConnectServer(request ConnectServerRequest) (ServerConnectionInfo, error) {
	if request.QRCode != "" {
		parsed, err := parseConnectionQR(request.QRCode)
		if err != nil {
			return ServerConnectionInfo{}, mapError(err)
		}
		if request.URL == "" {
			request.URL = parsed.URL
		}
		if request.InviteCode == "" {
			request.InviteCode = parsed.InviteCode
		}
		if request.Fingerprint == "" {
			request.Fingerprint = parsed.Fingerprint
		}
		if request.SecurityMode == "" {
			request.SecurityMode = parsed.SecurityMode
		}
	}
	if request.SecurityMode == "" {
		request.SecurityMode = string(transport.HTTPSecurityPinned)
	}
	if request.DeviceName == "" {
		request.DeviceName = "Windows desktop"
	}
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	httpTransport, err := transport.NewHTTP(transport.HTTPConfig{
		BaseURL: request.URL, SecurityMode: transport.HTTPSecurityMode(request.SecurityMode),
		PinnedFingerprint: request.Fingerprint, DeviceID: acc.DeviceID, SignChallenge: acc.SignDeviceChallenge,
	})
	if err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 30*time.Second)
	defer cancel()
	if request.InviteCode != "" {
		registration, err := acc.ServerRegistrationData(ctx, workspaceID)
		if err != nil {
			return ServerConnectionInfo{}, mapError(err)
		}
		if err := httpTransport.Register(ctx, transport.RegistrationRequest{InviteCode: request.InviteCode, DeviceName: request.DeviceName, Data: registration}); err != nil {
			return ServerConnectionInfo{}, mapError(err)
		}
	}
	diagnostics := httpTransport.Diagnose(ctx)
	if !diagnostics.Reachable || !diagnostics.Authenticated {
		return ServerConnectionInfo{}, mapError(errors.New("server connection diagnostics failed: " + diagnostics.ErrorClass))
	}
	if err := refreshRemoteDevices(ctx, acc, httpTransport, workspaceID); err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	worker, repository, err := a.buildWorkspaceWorker(acc, workspaceID, httpTransport)
	if err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}

	a.mu.Lock()
	next := a.settings
	next.SyncEnabled, next.SyncServerURL, next.SyncSecurityMode, next.SyncFingerprint = true, request.URL, request.SecurityMode, request.Fingerprint
	a.mu.Unlock()
	if err := next.validate(); err != nil {
		return ServerConnectionInfo{}, err
	}

	coordinator := coresync.NewCoordinator(a.requestContext())
	httpTransport.BeginSync()
	if err := coordinator.Attach(worker); err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	// Keep the currently working connection and its persisted settings intact
	// unless both the replacement worker and the new settings are ready. This
	// makes changing servers atomic from the user's perspective.
	if err := saveSettings(next); err != nil {
		coordinator.Detach()
		return ServerConnectionInfo{}, mapError(err)
	}
	a.mu.Lock()
	previous := a.syncCoordinator
	a.settings, a.transport, a.httpTransport, a.syncCoordinator, a.syncRepository = next, httpTransport, httpTransport, coordinator, repository
	a.mu.Unlock()
	if previous != nil {
		previous.Detach()
	}
	return ServerConnectionInfo{Enabled: true, URL: request.URL, Protocol: "https", SecurityMode: request.SecurityMode, Fingerprint: request.Fingerprint, Diagnostics: diagnostics}, nil
}

// SyncConnectionInfo returns the saved server endpoint and HTTPS verification
// policy. It performs no network request, so settings remain visible while the
// configured server is offline and can be replaced without disconnecting it
// first. Invite codes are intentionally never persisted or returned.
func (a *App) SyncConnectionInfo() ServerConnectionInfo {
	a.mu.Lock()
	settings := a.settings
	a.mu.Unlock()
	protocol := ""
	if settings.SyncServerURL != "" {
		protocol = "https"
	}
	return ServerConnectionInfo{
		Enabled:      settings.SyncEnabled,
		URL:          settings.SyncServerURL,
		Protocol:     protocol,
		SecurityMode: settings.SyncSecurityMode,
		Fingerprint:  settings.SyncFingerprint,
	}
}

// buildWorkspaceWorker constructs (without attaching) the sync worker for
// one workspace against httpTransport: a fresh SyncRepository/SyncProcessor
// pair and the Prepare/Bootstrap/ReviewSnapshot/PublishSnapshot/Progress
// hooks every workspace sync worker needs. ConnectServer, SetActiveWorkspace,
// and AcceptWorkspaceGrant all call this so the hook wiring is defined once.
func (a *App) buildWorkspaceWorker(acc *account.Account, workspaceID model.ID, httpTransport *transport.HTTP) (*coresync.Worker, *store.SyncRepository, error) {
	repository, err := store.NewSyncRepository(acc.DB(), "http")
	if err != nil {
		return nil, nil, err
	}
	processor, err := account.NewSyncProcessor(acc, account.SyncProcessorOptions{})
	if err != nil {
		return nil, nil, err
	}
	var lastSnapshot uint64
	var lastCatalogDigest [32]byte
	var lastReviewed model.ID
	worker, err := coresync.NewWorker(workspaceID, repository, httpTransport, processor, coresync.WorkerOptions{
		Prepare: func(ctx context.Context) error { return refreshRemoteDevices(ctx, acc, httpTransport, workspaceID) },
		Bootstrap: func(ctx context.Context) error {
			if err := refreshRemoteDevices(ctx, acc, httpTransport, workspaceID); err != nil {
				return err
			}
			snapshot, err := httpTransport.LatestSnapshot(ctx, workspaceID)
			if err != nil {
				return err
			}
			ack, err := acc.ApplyWorkspaceSnapshot(ctx, snapshot, repository, processor)
			if err != nil {
				return err
			}
			_, err = httpTransport.AcknowledgeSnapshot(ctx, ack)
			return err
		},
		ReviewSnapshot: func(ctx context.Context, _ coresync.Cursor) error {
			snapshot, err := httpTransport.LatestSnapshot(ctx, workspaceID)
			if errors.Is(err, transport.ErrNotFound) {
				return nil
			}
			if err != nil || snapshot.ID == lastReviewed {
				return err
			}
			// A snapshot whose base is ahead of the local cursor is precisely
			// the recovery path for a newly joined client: ApplyWorkspaceSnapshot
			// replays the missing authenticated operations and advances that
			// cursor atomically. Skipping it left a joined workspace empty even
			// though the server had returned its snapshot.
			ack, err := acc.ApplyWorkspaceSnapshot(ctx, snapshot, repository, processor)
			if err != nil {
				return err
			}
			if _, err := httpTransport.AcknowledgeSnapshot(ctx, ack); err != nil {
				return err
			}
			lastReviewed = snapshot.ID
			return nil
		},
		SyncAttachments: func(ctx context.Context) error {
			return acc.SynchronizeWorkspaceAttachments(ctx, workspaceID, httpTransport)
		},
		PublishSnapshot: func(ctx context.Context, cursor coresync.Cursor) error {
			// This device's own catalog (notebooks/tags/attachments, which
			// travel only inside snapshots, never as incremental operations)
			// can still contain an EnsureNotebookPlaceholder/EnsureTagPlaceholder
			// stand-in applied from a pulled note-metadata operation, ahead of
			// ever reviewing the sharer's own snapshot. Publishing that
			// placeholder-only catalog would overwrite the server's "latest"
			// snapshot with incomplete data - and since neither side's local
			// catalog digest ever changes again afterward, neither device
			// would ever republish a corrected one, permanently stranding
			// this member with hidden placeholders instead of the real
			// notebooks/tags. Deferring self-publish until every placeholder
			// resolves lets a future ReviewSnapshot catch up first.
			if pending, err := store.HasPendingSyncPlaceholders(ctx, acc.DB(), workspaceID); err != nil {
				return err
			} else if pending {
				return nil
			}
			catalogDigest, err := acc.WorkspaceCatalogDigest(ctx, workspaceID)
			if err != nil {
				return err
			}
			if cursor.LastSequence <= lastSnapshot && catalogDigest == lastCatalogDigest {
				return nil
			}
			if lastSnapshot != 0 && cursor.LastSequence-lastSnapshot < 1000 && catalogDigest == lastCatalogDigest {
				return nil
			}
			snapshot, err := acc.CreateWorkspaceSnapshot(ctx, workspaceID, repository)
			if err != nil {
				return err
			}
			if err := httpTransport.PutSnapshot(ctx, snapshot); err != nil {
				return err
			}
			ack, err := acc.ApplyWorkspaceSnapshot(ctx, snapshot, repository, processor)
			if err != nil {
				return err
			}
			if _, err := httpTransport.AcknowledgeSnapshot(ctx, ack); err != nil {
				return err
			}
			lastSnapshot = cursor.LastSequence
			lastCatalogDigest = catalogDigest
			lastReviewed = snapshot.ID
			return nil
		},
		Progress: func(progress coresync.Progress) {
			status := transport.StatusActive
			switch progress.Phase {
			case coresync.PhaseCurrent:
				status = transport.StatusCurrent
				httpTransport.CompleteSync()
				a.setSyncError("")
			case coresync.PhaseBackoff:
				status = transport.StatusOffline
				httpTransport.SyncOffline()
				a.setSyncError(progress.ErrorDetail)
			case coresync.PhaseQuarantine:
				status = transport.StatusFailed
				httpTransport.SyncFailed()
				a.setSyncError(progress.ErrorDetail)
			default:
				httpTransport.BeginSync()
			}
			a.emit(EventSyncStatus, string(status))
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return worker, repository, nil
}

func (a *App) setSyncError(detail string) {
	a.mu.Lock()
	a.syncErrorDetail = detail
	a.mu.Unlock()
	a.emit(EventSyncError, detail)
}

// attachWorkspaceSync builds a sync worker for workspaceID and swaps it into
// a's live sync state, detaching whatever coordinator was previously
// attached. It requires sync to already be enabled (a.httpTransport set by a
// prior ConnectServer); SetActiveWorkspace and AcceptWorkspaceGrant use this
// to redirect the running sync worker at a different workspace without
// re-registering or re-diagnosing the server connection.
func (a *App) attachWorkspaceSync(acc *account.Account, workspaceID model.ID) error {
	a.mu.Lock()
	httpTransport := a.httpTransport
	a.mu.Unlock()
	if httpTransport == nil {
		return &AppError{Code: ErrCodeInvalidInput, Message: "server synchronization is disabled"}
	}
	worker, repository, err := a.buildWorkspaceWorker(acc, workspaceID, httpTransport)
	if err != nil {
		return mapError(err)
	}
	coordinator := coresync.NewCoordinator(a.requestContext())
	httpTransport.BeginSync()
	if err := coordinator.Attach(worker); err != nil {
		return mapError(err)
	}
	a.mu.Lock()
	previous := a.syncCoordinator
	a.syncCoordinator, a.syncRepository = coordinator, repository
	a.mu.Unlock()
	if previous != nil {
		previous.Detach()
	}
	return nil
}

func refreshRemoteDevices(ctx context.Context, acc *account.Account, remote *transport.HTTP, workspaceID model.ID) error {
	// Operations in a shared workspace can be signed by any member's device,
	// not just another device owned by this account. Refresh the workspace
	// device directory before every pull so those signatures can be verified.
	rows, err := remote.ListWorkspaceMemberDevices(ctx, workspaceID.String())
	if err != nil {
		return err
	}
	records := make([]account.RemoteDeviceRecord, 0, len(rows))
	for _, row := range rows {
		id, err := parseID(row.ID)
		if err != nil {
			return err
		}
		records = append(records, account.RemoteDeviceRecord{ID: id, PublicKey: row.SigningPublic, Active: row.RevokedAt == nil})
	}
	return acc.UpsertRemoteDevices(ctx, records)
}

// DisableServer detaches networking only. The local database, full collection,
// outbox, cursor, and previously downloaded data remain untouched.
func (a *App) DisableServer() error {
	a.mu.Lock()
	coordinator := a.syncCoordinator
	next := a.settings
	next.SyncEnabled = false
	a.mu.Unlock()
	if err := saveSettings(next); err != nil {
		return mapError(err)
	}
	if coordinator != nil {
		coordinator.Detach()
	}
	a.mu.Lock()
	a.settings, a.transport, a.httpTransport, a.syncCoordinator, a.syncRepository = next, transport.NewLocal(), nil, nil, nil
	a.syncErrorDetail = ""
	a.mu.Unlock()
	a.emit(EventSyncError, "")
	a.emit(EventSyncStatus, string(transport.StatusDisabled))
	return nil
}

func (a *App) DiagnoseServer() (transport.Diagnostics, error) {
	a.mu.Lock()
	remote := a.httpTransport
	a.mu.Unlock()
	if remote == nil {
		return transport.Diagnostics{}, &AppError{Code: ErrCodeInvalidInput, Message: "server synchronization is disabled"}
	}
	ctx, cancel := context.WithTimeout(a.requestContext(), 15*time.Second)
	defer cancel()
	return remote.Diagnose(ctx), nil
}

func (a *App) ListSyncDevices() ([]transport.RemoteDevice, error) {
	a.mu.Lock()
	remote := a.httpTransport
	a.mu.Unlock()
	if remote == nil {
		return nil, &AppError{Code: ErrCodeInvalidInput, Message: "server synchronization is disabled"}
	}
	devices, err := remote.ListDevices(a.requestContext())
	return devices, mapError(err)
}

func (a *App) RevokeSyncDevice(deviceID string) error {
	a.mu.Lock()
	remote := a.httpTransport
	a.mu.Unlock()
	if remote == nil {
		return &AppError{Code: ErrCodeInvalidInput, Message: "server synchronization is disabled"}
	}
	return mapError(remote.RevokeDevice(a.requestContext(), deviceID))
}

func (a *App) ListSyncQuarantine() ([]SyncQuarantineDTO, error) {
	_, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	a.mu.Lock()
	repository := a.syncRepository
	a.mu.Unlock()
	if repository == nil {
		return nil, nil
	}
	entries, err := repository.ListQuarantine(a.requestContext(), workspaceID)
	if err != nil {
		return nil, mapError(err)
	}
	result := make([]SyncQuarantineDTO, len(entries))
	for i, entry := range entries {
		result[i] = SyncQuarantineDTO{OperationID: entry.OperationID.String(), Sequence: entry.Sequence,
			Reason: entry.Reason, ReceivedUnixMS: entry.ReceivedAt.UnixMilli()}
	}
	return result, nil
}

func (a *App) RetrySyncQuarantine(operationID string) error {
	_, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(operationID)
	if err != nil {
		return mapError(err)
	}
	a.mu.Lock()
	repository, coordinator := a.syncRepository, a.syncCoordinator
	a.mu.Unlock()
	if repository == nil {
		return &AppError{Code: ErrCodeInvalidInput, Message: "server synchronization is disabled"}
	}
	if err := repository.RetryQuarantined(a.requestContext(), workspaceID, id); err != nil {
		return mapError(err)
	}
	if coordinator != nil {
		coordinator.Trigger()
	}
	return nil
}

// SyncNow starts an immediate synchronization cycle for the currently active
// workspace. Coordinator.Trigger coalesces concurrent requests, so repeated
// calls cannot create overlapping pull/push cycles.
func (a *App) SyncNow() error {
	a.mu.Lock()
	coordinator, remote := a.syncCoordinator, a.httpTransport
	a.mu.Unlock()
	if coordinator == nil || remote == nil {
		return &AppError{Code: ErrCodeInvalidInput, Message: "server synchronization is disabled"}
	}
	remote.BeginSync()
	a.emit(EventSyncStatus, string(transport.StatusActive))
	if !coordinator.Trigger() {
		remote.SyncFailed()
		a.setSyncError("synchronization worker is not running")
		a.emit(EventSyncStatus, string(transport.StatusFailed))
		return &AppError{Code: ErrCodeInternal, Message: "synchronization worker is not running"}
	}
	return nil
}

func parseConnectionQR(encoded string) (ConnectServerRequest, error) {
	parsed, err := url.Parse(strings.TrimSpace(encoded))
	if err != nil || parsed.Scheme != "beresta" || parsed.Host != "connect" {
		return ConnectServerRequest{}, errors.New("invalid Beresta connection QR")
	}
	query := parsed.Query()
	request := ConnectServerRequest{URL: query.Get("url"), InviteCode: query.Get("invite"), Fingerprint: query.Get("fingerprint"), SecurityMode: query.Get("mode")}
	if request.SecurityMode == "" {
		request.SecurityMode = string(transport.HTTPSecurityPinned)
	}
	if request.URL == "" {
		return ConnectServerRequest{}, errors.New("connection QR does not contain a server URL")
	}
	return request, nil
}
