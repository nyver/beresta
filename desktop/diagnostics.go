package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/backupsummary"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/core/store"
	coresync "github.com/beresta-app/beresta/core/sync"
	"github.com/beresta-app/beresta/core/syncsummary"
	"github.com/beresta-app/beresta/internal/diagnostics"
)

// appVersion is the running application's display version. It matches
// desktop/wails.json's productVersion, which also names the installer.
const appVersion = "0.1.0"

// DiagnosticSummary reports the bounded facts the user diagnostics view
// always shows, per specs/product-experience's "Layered privacy-preserving
// diagnostics" requirement.
func (a *App) DiagnosticSummary() (DiagnosticSummaryDTO, error) {
	summary, err := a.collectDiagnosticSummary()
	if err != nil {
		return DiagnosticSummaryDTO{}, mapError(err)
	}
	return newDiagnosticSummaryDTO(summary), nil
}

// TechnicalDiagnostics reports the additional facts the "expand technical
// details" layer of the user diagnostics view MAY show.
func (a *App) TechnicalDiagnostics() (TechnicalDiagnosticsDTO, error) {
	technical, err := a.collectTechnicalDiagnostics()
	if err != nil {
		return TechnicalDiagnosticsDTO{}, mapError(err)
	}
	return newTechnicalDiagnosticsDTO(technical), nil
}

// CopyDiagnostics renders the current user and technical diagnostics as
// the sanitized plain-text bundle the "Copy diagnostics" action places on
// the clipboard.
func (a *App) CopyDiagnostics() (string, error) {
	summary, err := a.collectDiagnosticSummary()
	if err != nil {
		return "", mapError(err)
	}
	technical, err := a.collectTechnicalDiagnostics()
	if err != nil {
		return "", mapError(err)
	}
	bundle, err := diagnostics.CopyDiagnostics(summary, technical)
	if err != nil {
		return "", mapError(err)
	}
	return bundle, nil
}

func (a *App) collectDiagnosticSummary() (presentation.DiagnosticSummary, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return presentation.DiagnosticSummary{}, err
	}
	ctx := a.requestContext()

	a.mu.Lock()
	configured := a.httpTransport != nil
	repository := a.syncRepository
	coordinator := a.syncCoordinator
	databasePath := a.settings.LastDatabasePath
	a.mu.Unlock()

	pendingCount, unsafeCount, err := countPendingAndUnsafe(ctx, repository, workspaceID)
	if err != nil {
		return presentation.DiagnosticSummary{}, err
	}
	var progress coresync.CoordinatorProgress
	if coordinator != nil {
		progress = coordinator.Progress()
	}
	syncState := syncsummary.Summarize(syncsummary.Inputs{
		Configured: configured, Progress: progress, PendingCount: pendingCount, UnsafeCount: unsafeCount, Now: time.Now(),
	})

	backupStatus, err := a.backupStatus(ctx, acc)
	if err != nil {
		return presentation.DiagnosticSummary{}, err
	}

	usage, err := storageUsageBytes(databasePath)
	if err != nil {
		return presentation.DiagnosticSummary{}, err
	}

	return presentation.DiagnosticSummary{
		AppVersion:         appVersion,
		Platform:           "windows",
		SyncConfigured:     configured,
		LastSuccessfulSync: progress.LastSuccess,
		PendingCount:       syncState.PendingCount,
		ConnectionState:    syncState.State,
		Backup:             backupStatus,
		StorageUsageBytes:  usage,
		// The account is open, and store.Open already ran
		// cipher_integrity_check before returning it, so "ok" reflects a
		// real, already-completed check rather than a fabricated status.
		Database: presentation.DatabaseHealthOK,
		Update:   presentation.UpdateStatusUnknown,
	}, nil
}

func (a *App) collectTechnicalDiagnostics() (presentation.TechnicalDiagnostics, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return presentation.TechnicalDiagnostics{}, err
	}
	ctx := a.requestContext()

	a.mu.Lock()
	repository := a.syncRepository
	coordinator := a.syncCoordinator
	settings := a.settings
	a.mu.Unlock()

	technical := presentation.TechnicalDiagnostics{
		WorkspaceID: idString(workspaceID),
		DeviceID:    idString(acc.DeviceID),
	}
	if repository != nil {
		cursor, err := repository.Cursor(ctx, workspaceID)
		if err != nil {
			return presentation.TechnicalDiagnostics{}, err
		}
		technical.CursorSequence = cursor.LastSequence
		technical.CursorEpoch = cursor.Epoch
		if technical.PendingOperationCount, err = repository.CountPending(ctx, workspaceID); err != nil {
			return presentation.TechnicalDiagnostics{}, err
		}
		entries, err := repository.ListQuarantine(ctx, workspaceID)
		if err != nil {
			return presentation.TechnicalDiagnostics{}, err
		}
		technical.QuarantinedOperationIDs = make([]string, len(entries))
		for i, entry := range entries {
			technical.QuarantinedOperationIDs[i] = entry.OperationID.String()
		}
	}
	if coordinator != nil {
		progress := coordinator.Progress()
		technical.LastErrorClass = progress.ErrorClass
		technical.RetryCount = progress.RetryCount
		if remaining := time.Until(progress.RetryDeadline); !progress.RetryDeadline.IsZero() && remaining > 0 {
			technical.RetryIn = remaining
		}
	}
	if settings.SyncEnabled {
		technical.TransportProtocol = "https"
		technical.TransportSecurityMode = settings.SyncSecurityMode
		technical.TransportURL = settings.SyncServerURL
	}
	if version, err := store.SchemaVersion(ctx, acc.DB()); err == nil {
		technical.MigrationVersion = version
	}
	return technical, nil
}

// countPendingAndUnsafe reads the durable pending/unsafe counts, or (0, 0)
// when synchronization has never been configured for this account.
func countPendingAndUnsafe(ctx context.Context, repository *store.SyncRepository, workspaceID model.ID) (int, int, error) {
	if repository == nil {
		return 0, 0, nil
	}
	pendingCount, err := repository.CountPending(ctx, workspaceID)
	if err != nil {
		return 0, 0, err
	}
	unsafeCount, err := repository.CountQuarantine(ctx, workspaceID)
	if err != nil {
		return 0, 0, err
	}
	return pendingCount, unsafeCount, nil
}

// backupStatus derives a presentation.BackupStatus (see
// core/backupsummary.Summarize) from the newest daily or manual backup
// catalog entry, for both the diagnostics summary and the dedicated
// BackupStatus bridge method the Data settings backup panel calls directly.
func (a *App) backupStatus(ctx context.Context, acc *account.Account) (presentation.BackupStatus, error) {
	daily, err := acc.ListBackups(ctx, store.BackupKindDaily)
	if err != nil {
		return presentation.BackupStatus{}, err
	}
	manual, err := acc.ListBackups(ctx, store.BackupKindManual)
	if err != nil {
		return presentation.BackupStatus{}, err
	}
	return backupsummary.Summarize(daily, manual), nil
}

// BackupStatus reports the current presentation.BackupStatus (last verified
// time, storage location, and health) for display under Data settings, per
// specs/backup-and-recovery.md's "Understandable verified backup status"
// requirement.
func (a *App) BackupStatus() (BackupStatusDTO, error) {
	acc, _, err := a.primaryWorkspace()
	if err != nil {
		return BackupStatusDTO{}, mapError(err)
	}
	status, err := a.backupStatus(a.requestContext(), acc)
	if err != nil {
		return BackupStatusDTO{}, mapError(err)
	}
	return newBackupStatusDTO(status), nil
}

// storageUsageBytes sums the local database file (and its WAL/SHM
// sidecars) and the attachment blob cache next to it, matching
// core/account's newBlobStore layout (a "blobs" directory beside the
// database file).
func storageUsageBytes(databasePath string) (int64, error) {
	if databasePath == "" {
		return 0, nil
	}
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(databasePath + suffix); err == nil {
			total += info.Size()
		}
	}
	blobsDir := filepath.Join(filepath.Dir(databasePath), "blobs")
	err := filepath.WalkDir(blobsDir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	return total, nil
}
