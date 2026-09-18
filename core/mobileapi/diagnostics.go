package mobileapi

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

// DiagnosticSummary reports the bounded facts the user diagnostics screen
// always shows, per specs/product-experience's "Layered privacy-preserving
// diagnostics" requirement, as a strict JSON string matching desktop's
// DiagnosticSummaryDTO. appVersion is supplied by the Flutter host (via
// PackageInfo), since the Go core has no knowledge of the Android app
// package's own version.
func (s *Service) DiagnosticSummary(appVersion string) (string, error) {
	summary, err := s.collectDiagnosticSummary(appVersion)
	if err != nil {
		return "", err
	}
	return MarshalDiagnosticSummary(summary)
}

// TechnicalDiagnostics reports the additional facts the "expand technical
// details" layer of the user diagnostics screen MAY show, as a strict JSON
// string matching desktop's TechnicalDiagnosticsDTO.
func (s *Service) TechnicalDiagnostics() (string, error) {
	technical, err := s.collectTechnicalDiagnostics()
	if err != nil {
		return "", err
	}
	return MarshalTechnicalDiagnostics(technical)
}

// CopyDiagnostics renders the current user and technical diagnostics as
// the sanitized plain-text bundle the "Copy diagnostics" action places on
// the clipboard.
func (s *Service) CopyDiagnostics(appVersion string) (string, error) {
	summary, err := s.collectDiagnosticSummary(appVersion)
	if err != nil {
		return "", err
	}
	technical, err := s.collectTechnicalDiagnostics()
	if err != nil {
		return "", err
	}
	return diagnostics.CopyDiagnostics(summary, technical)
}

func (s *Service) collectDiagnosticSummary(appVersion string) (presentation.DiagnosticSummary, error) {
	value, workspaceID, err := s.accountState()
	if err != nil {
		return presentation.DiagnosticSummary{}, err
	}

	s.mu.Lock()
	configured := s.remote != nil
	repository := s.repository
	coordinator := s.coordinator
	databasePath := s.databasePath
	s.mu.Unlock()

	pendingCount, unsafeCount, err := countPendingAndUnsafe(s.root, repository, workspaceID)
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

	backupStatus, err := s.backupStatus(value)
	if err != nil {
		return presentation.DiagnosticSummary{}, err
	}

	usage, err := storageUsageBytes(databasePath)
	if err != nil {
		return presentation.DiagnosticSummary{}, err
	}

	return presentation.DiagnosticSummary{
		AppVersion:         appVersion,
		Platform:           "android",
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

func (s *Service) collectTechnicalDiagnostics() (presentation.TechnicalDiagnostics, error) {
	value, workspaceID, err := s.accountState()
	if err != nil {
		return presentation.TechnicalDiagnostics{}, err
	}

	s.mu.Lock()
	repository := s.repository
	coordinator := s.coordinator
	s.mu.Unlock()

	technical := presentation.TechnicalDiagnostics{
		WorkspaceID: workspaceID.String(),
		DeviceID:    value.DeviceID.String(),
	}
	if repository != nil {
		cursor, err := repository.Cursor(s.root, workspaceID)
		if err != nil {
			return presentation.TechnicalDiagnostics{}, err
		}
		technical.CursorSequence = cursor.LastSequence
		technical.CursorEpoch = cursor.Epoch
		if technical.PendingOperationCount, err = repository.CountPending(s.root, workspaceID); err != nil {
			return presentation.TechnicalDiagnostics{}, err
		}
		entries, err := repository.ListQuarantine(s.root, workspaceID)
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
	if cfg, err := loadSyncConnectionConfig(s.root, value.DB()); err == nil && cfg.Enabled {
		technical.TransportProtocol = cfg.Protocol
		technical.TransportSecurityMode = cfg.SecurityMode
		technical.TransportURL = cfg.URL
	}
	if version, err := store.SchemaVersion(s.root, value.DB()); err == nil {
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
// BackupStatus bridge method the Data settings backup screen calls
// directly.
func (s *Service) backupStatus(value *account.Account) (presentation.BackupStatus, error) {
	daily, err := value.ListBackups(s.root, store.BackupKindDaily)
	if err != nil {
		return presentation.BackupStatus{}, err
	}
	manual, err := value.ListBackups(s.root, store.BackupKindManual)
	if err != nil {
		return presentation.BackupStatus{}, err
	}
	return backupsummary.Summarize(daily, manual), nil
}

// BackupStatus reports the current backup status (last verified time,
// storage location, and health) as a strict JSON string matching desktop's
// identical BackupStatusDTO, for display under Data settings per
// specs/backup-and-recovery.md's "Understandable verified backup status"
// requirement.
func (s *Service) BackupStatus() (string, error) {
	value, _, err := s.accountState()
	if err != nil {
		return "", err
	}
	status, err := s.backupStatus(value)
	if err != nil {
		return "", err
	}
	return MarshalBackupStatus(status)
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
