package main

import (
	"context"
	"errors"
	"time"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/keyrotation"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/sharecode"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/syncsummary"
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
		parsed, err := sharecode.DecodeConnect(request.QRCode)
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
	a.mu.Lock()
	generation := a.syncGeneration
	a.mu.Unlock()
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
		if err := httpTransport.Register(ctx, transport.RegistrationRequest{InviteCode: request.InviteCode, DeviceName: request.DeviceName, Platform: "windows", Data: registration}); err != nil {
			return ServerConnectionInfo{}, mapError(err)
		}
	}
	diagnostics := httpTransport.Diagnose(ctx)
	if !diagnostics.Reachable || !diagnostics.Authenticated {
		return ServerConnectionInfo{}, mapError(errors.New("server connection diagnostics failed: " + string(diagnostics.ErrorClass)))
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
	if a.syncGeneration != generation {
		// DisableServer or lockAccount ran while this attempt was still
		// working (most often activate's background reconnect-after-unlock
		// retry, raced by the user disabling sync or locking again before
		// it finished): drop the result instead of silently resurrecting a
		// connection the user just turned off. Checked in the same critical
		// section as the commit below, so there is no window between the
		// check and the assignment for a concurrent DisableServer/
		// lockAccount to land in.
		a.mu.Unlock()
		coordinator.Detach()
		return ServerConnectionInfo{}, &AppError{Code: ErrCodeInvalidInput, Message: "server connection was disabled before this connection attempt finished"}
	}
	previous := a.syncCoordinator
	a.settings, a.transport, a.httpTransport, a.syncCoordinator, a.syncRepository = next, httpTransport, httpTransport, coordinator, repository
	a.mu.Unlock()
	if previous != nil {
		previous.Detach()
	}
	a.emit(EventSyncSummary)
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
		Prepare: func(ctx context.Context) error {
			if err := refreshRemoteDevices(ctx, acc, httpTransport, workspaceID); err != nil {
				return err
			}
			// Finishes a rotation this device began but could not publish
			// inline (see RevokeWorkspaceMember), and detects/applies a
			// rotation a fellow member initiated (see design.md decision 10).
			return keyrotation.Reconcile(ctx, acc, httpTransport, workspaceID)
		},
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
			// progress.ErrorDetail is bounded diagnostic text; it is not
			// surfaced through SyncSummary today (specs/sync-engine's
			// "Aggregated synchronization state" requirement deliberately
			// keeps raw backend errors out of the shared summary) but will
			// feed the sanitized technical diagnostics screen once that
			// lands (specs/product-experience's "Layered privacy-preserving
			// diagnostics" requirement).
			//
			// The frontend's shared SyncSummary combines this progress
			// snapshot with durable pending/unsafe counts (see SyncSummary
			// below); emitting only a signal here, instead of computing
			// that summary on every phase transition, keeps this callback
			// - which runs on the worker's own goroutine mid-cycle - free
			// of extra database reads.
			a.emit(EventSyncSummary)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return worker, repository, nil
}

// SyncSummary returns the shared platform-neutral synchronization summary
// (see core/syncsummary and specs/sync-engine's "Aggregated synchronization
// state" requirement) for the active workspace, combining live coordinator
// progress with durable pending and unsafe-operation counts. The frontend
// calls this on load, on an interval, and in response to EventSyncSummary
// instead of branching on raw transport status strings.
func (a *App) SyncSummary() (SyncSummaryDTO, error) {
	a.mu.Lock()
	acc := a.account
	coordinator := a.syncCoordinator
	repository := a.syncRepository
	configured := a.httpTransport != nil
	preferred := a.settings.ActiveWorkspaceID
	a.mu.Unlock()

	var progress coresync.CoordinatorProgress
	if coordinator != nil {
		progress = coordinator.Progress()
	}
	// repository is only non-nil once unlocked and connected (lockAccount
	// and DisableServer both clear it together with acc/coordinator), so
	// this never needs to resolve a workspace - and never errors just
	// because the account happens to be locked, matching the permissive
	// contract the SyncStatus this replaces had - while locked or
	// unconfigured.
	var pendingCount, unsafeCount int
	if repository != nil && acc != nil {
		ids, err := acc.Workspaces()
		if err != nil {
			return SyncSummaryDTO{}, mapError(err)
		}
		if len(ids) > 0 {
			workspaceID := resolveActiveWorkspace(ids, preferred)
			ctx := a.requestContext()
			if pendingCount, err = repository.CountPending(ctx, workspaceID); err != nil {
				return SyncSummaryDTO{}, mapError(err)
			}
			if unsafeCount, err = repository.CountQuarantine(ctx, workspaceID); err != nil {
				return SyncSummaryDTO{}, mapError(err)
			}
		}
	}
	summary := syncsummary.Summarize(syncsummary.Inputs{
		Configured:   configured,
		Progress:     progress,
		PendingCount: pendingCount,
		UnsafeCount:  unsafeCount,
		Now:          time.Now(),
	})
	return newSyncSummaryDTO(summary), nil
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
	a.syncGeneration++
	a.mu.Unlock()
	a.emit(EventSyncSummary)
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

// RetrySyncQuarantine discards a quarantined operation's locally-rejected
// copy so the next cycle re-pulls and re-verifies it from scratch - the
// durable cursor was never advanced past it, so this can never skip or lose
// an operation, only give a fixed client (or a transient false rejection)
// another chance to accept it. A quarantined worker exits permanently (see
// coresync.Coordinator.Attach), so retrying also reattaches it when needed;
// otherwise Trigger would silently no-op against a coordinator with no
// worker left to wake.
func (a *App) RetrySyncQuarantine(operationID string) error {
	acc, workspaceID, err := a.primaryWorkspace()
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
	coordinator, err = a.reattachIfDetached(acc, workspaceID, coordinator)
	if err != nil {
		return mapError(err)
	}
	if coordinator != nil {
		coordinator.Trigger()
	}
	a.emit(EventSyncSummary)
	return nil
}

// reattachIfDetached returns coordinator unchanged if it is still attached
// and running; otherwise it rebuilds and attaches a fresh worker for
// workspaceID (the same worker attachWorkspaceSync always builds) and
// returns that. Both RetrySyncQuarantine and SyncNow need this: a
// quarantined worker exits and detaches itself permanently, so acting on a
// stale coordinator reference after that would silently do nothing.
func (a *App) reattachIfDetached(acc *account.Account, workspaceID model.ID, coordinator *coresync.Coordinator) (*coresync.Coordinator, error) {
	if coordinator != nil && coordinator.Enabled() {
		return coordinator, nil
	}
	if err := a.attachWorkspaceSync(acc, workspaceID); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.syncCoordinator, nil
}

// SyncNow starts an immediate synchronization cycle for the currently active
// workspace. Coordinator.Trigger coalesces concurrent requests, so repeated
// calls cannot create overlapping pull/push cycles.
func (a *App) SyncNow() error {
	a.mu.Lock()
	coordinator, remote := a.syncCoordinator, a.httpTransport
	a.mu.Unlock()
	if remote == nil {
		return &AppError{Code: ErrCodeInvalidInput, Message: "server synchronization is disabled"}
	}
	if coordinator == nil || !coordinator.Enabled() {
		acc, workspaceID, err := a.primaryWorkspace()
		if err != nil {
			return mapError(err)
		}
		if coordinator, err = a.reattachIfDetached(acc, workspaceID, coordinator); err != nil {
			return mapError(err)
		}
	}
	a.emit(EventSyncSummary)
	if coordinator == nil || !coordinator.Trigger() {
		a.emit(EventSyncSummary)
		return &AppError{Code: ErrCodeInternal, Message: "synchronization worker is not running"}
	}
	return nil
}
