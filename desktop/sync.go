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
	if err := refreshRemoteDevices(ctx, acc, httpTransport); err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	repository, err := store.NewSyncRepository(acc.DB(), "http")
	if err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	processor, err := account.NewSyncProcessor(acc, account.SyncProcessorOptions{})
	if err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	var lastSnapshot uint64
	var lastReviewed model.ID
	worker, err := coresync.NewWorker(workspaceID, repository, httpTransport, processor, coresync.WorkerOptions{
		Prepare: func(ctx context.Context) error { return refreshRemoteDevices(ctx, acc, httpTransport) },
		Bootstrap: func(ctx context.Context) error {
			if err := refreshRemoteDevices(ctx, acc, httpTransport); err != nil {
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
		ReviewSnapshot: func(ctx context.Context, cursor coresync.Cursor) error {
			snapshot, err := httpTransport.LatestSnapshot(ctx, workspaceID)
			if errors.Is(err, transport.ErrNotFound) {
				return nil
			}
			if err != nil || snapshot.ID == lastReviewed || snapshot.BaseSequence > cursor.LastSequence {
				return err
			}
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
		PublishSnapshot: func(ctx context.Context, cursor coresync.Cursor) error {
			if cursor.LastSequence <= lastSnapshot || (lastSnapshot != 0 && cursor.LastSequence-lastSnapshot < 1000) {
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
			lastReviewed = snapshot.ID
			return nil
		},
		Progress: func(progress coresync.Progress) {
			status := transport.StatusActive
			switch progress.Phase {
			case coresync.PhaseCurrent:
				status = transport.StatusCurrent
			case coresync.PhaseBackoff:
				status = transport.StatusOffline
			case coresync.PhaseQuarantine:
				status = transport.StatusFailed
			}
			a.emit(EventSyncStatus, string(status))
		},
	})
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
	if err := saveSettings(next); err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}

	coordinator := coresync.NewCoordinator(a.requestContext())
	if err := coordinator.Attach(worker); err != nil {
		return ServerConnectionInfo{}, mapError(err)
	}
	a.mu.Lock()
	previous := a.syncCoordinator
	a.settings, a.transport, a.httpTransport, a.syncCoordinator, a.syncRepository = next, httpTransport, httpTransport, coordinator, repository
	a.mu.Unlock()
	if previous != nil {
		previous.Detach()
	}
	a.emit(EventSyncStatus, string(transport.StatusActive))
	return ServerConnectionInfo{Enabled: true, URL: request.URL, SecurityMode: request.SecurityMode, Fingerprint: request.Fingerprint, Diagnostics: diagnostics}, nil
}

func refreshRemoteDevices(ctx context.Context, acc *account.Account, remote *transport.HTTP) error {
	rows, err := remote.ListDevices(ctx)
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
	a.mu.Unlock()
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
