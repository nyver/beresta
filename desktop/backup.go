package main

import (
	"fmt"
	"time"

	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/store"
)

// backupKindNames maps store.BackupKind* constants to their JS-facing
// names.
var backupKindNames = map[int]string{
	store.BackupKindDaily:        "daily",
	store.BackupKindPreMigration: "pre_migration",
	store.BackupKindPreRestore:   "pre_restore",
	store.BackupKindManual:       "manual",
}

func backupKindName(kind int) string {
	if name, ok := backupKindNames[kind]; ok {
		return name
	}
	return "unknown"
}

func parseBackupKind(name string) (int, error) {
	for kind, n := range backupKindNames {
		if n == name {
			return kind, nil
		}
	}
	return 0, &AppError{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("invalid backup kind %q", name)}
}

// BackupDTO is the JS-facing shape of a store.Backup.
type BackupDTO struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Location   string `json:"location"`
	VerifiedMS *int64 `json:"verified_unix_ms,omitempty"`
	NoteCount  *int64 `json:"note_count,omitempty"`
	SizeBytes  *int64 `json:"size_bytes,omitempty"`
	CreatedMS  int64  `json:"created_unix_ms"`
	Corrupt    bool   `json:"corrupt"`
}

func backupDTO(b store.Backup) BackupDTO {
	return BackupDTO{
		ID:         idString(b.ID),
		Kind:       backupKindName(b.Kind),
		Location:   b.Location,
		VerifiedMS: b.VerifiedUnixMS,
		NoteCount:  b.NoteCount,
		SizeBytes:  b.SizeBytes,
		CreatedMS:  b.CreatedUnixMS,
		Corrupt:    b.Corrupt,
	}
}

// CreateManualBackup creates a user-requested backup set under destRoot.
func (a *App) CreateManualBackup(destRoot string) (BackupDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return BackupDTO{}, mapError(err)
	}
	backup, err := acc.CreateBackup(a.requestContext(), destRoot, store.BackupKindManual, time.Now())
	if err != nil {
		return BackupDTO{}, mapError(err)
	}
	return backupDTO(backup), nil
}

// EnsureDailyBackup creates today's daily backup under destRoot if one
// does not already exist, and rotates old daily backups down to the
// retained seven. The desktop shell calls this once at every startup.
func (a *App) EnsureDailyBackup(destRoot string) (bool, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return false, mapError(err)
	}
	created, err := acc.EnsureDailyBackup(a.requestContext(), destRoot, time.Now())
	return created, mapError(err)
}

// ListBackups returns every catalog entry of one kind ("daily",
// "pre_migration", "pre_restore", or "manual"), newest first.
func (a *App) ListBackups(kind string) ([]BackupDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return nil, mapError(err)
	}
	k, err := parseBackupKind(kind)
	if err != nil {
		return nil, mapError(err)
	}
	backups, err := acc.ListBackups(a.requestContext(), k)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]BackupDTO, len(backups))
	for i, b := range backups {
		out[i] = backupDTO(b)
	}
	return out, nil
}

// VerifyBackup re-verifies one backup's manifest and file hashes,
// recording it verified or corrupt.
func (a *App) VerifyBackup(backupID string) error {
	acc, err := a.currentAccount()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(backupID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.VerifyBackup(a.requestContext(), id, time.Now()))
}

// VerifyAllBackups re-verifies every backup in the catalog. The desktop
// shell calls this once at every startup.
func (a *App) VerifyAllBackups() error {
	acc, err := a.currentAccount()
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.VerifyAllBackups(a.requestContext(), time.Now()))
}

// BackupPreviewDTO summarizes one backup's content without mutating
// current data.
type BackupPreviewDTO struct {
	Backup     BackupDTO `json:"backup"`
	NoteTitles []string  `json:"note_titles"`
}

// PreviewBackup opens a backup read-only and returns its catalog entry
// plus every note's title.
func (a *App) PreviewBackup(backupID string) (BackupPreviewDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return BackupPreviewDTO{}, mapError(err)
	}
	id, err := parseID(backupID)
	if err != nil {
		return BackupPreviewDTO{}, mapError(err)
	}
	preview, err := acc.PreviewBackup(a.requestContext(), id)
	if err != nil {
		return BackupPreviewDTO{}, mapError(err)
	}
	return BackupPreviewDTO{Backup: backupDTO(preview.Backup), NoteTitles: preview.NoteTitles}, nil
}

// restoreChangeKindNames maps account.RestoreChangeKind to its JS-facing
// name.
var restoreChangeKindNames = map[account.RestoreChangeKind]string{
	account.RestoreChangeAddition:  "addition",
	account.RestoreChangeUpdate:    "update",
	account.RestoreChangeUnchanged: "unchanged",
}

// RestorePlanEntryDTO is one note's classification in a restore dry run.
type RestorePlanEntryDTO struct {
	NoteID string `json:"note_id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
}

// RestorePlanDTO is the result of a dry-run restore preview.
type RestorePlanDTO struct {
	Entries              []RestorePlanEntryDTO `json:"entries"`
	RequiredStorageBytes uint64                `json:"required_storage_bytes"`
}

// PlanRestore computes, without mutating current data, what restoring
// noteIDs (or the whole backup, when noteIDs is empty) from backupID
// would do.
func (a *App) PlanRestore(backupID string, noteIDs []string) (RestorePlanDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return RestorePlanDTO{}, mapError(err)
	}
	id, err := parseID(backupID)
	if err != nil {
		return RestorePlanDTO{}, mapError(err)
	}
	notes, err := parseIDs(noteIDs)
	if err != nil {
		return RestorePlanDTO{}, mapError(err)
	}
	plan, err := acc.PlanRestore(a.requestContext(), id, notes)
	if err != nil {
		return RestorePlanDTO{}, mapError(err)
	}
	entries := make([]RestorePlanEntryDTO, len(plan.Entries))
	for i, e := range plan.Entries {
		entries[i] = RestorePlanEntryDTO{NoteID: idString(e.NoteID), Title: e.Title, Kind: restoreChangeKindNames[e.Kind]}
	}
	return RestorePlanDTO{Entries: entries, RequiredStorageBytes: plan.RequiredStorageBytes}, nil
}

// RestoreResultDTO reports what a restore actually did.
type RestoreResultDTO struct {
	SafetyBackup BackupDTO `json:"safety_backup"`
	NewNoteIDs   []string  `json:"new_note_ids"`
}

// RestoreSelective imports noteIDs from backupID as new local notes,
// always taking a mandatory pre-restore safety backup under destRoot
// first.
func (a *App) RestoreSelective(backupID string, noteIDs []string, destRoot string) (RestoreResultDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return RestoreResultDTO{}, mapError(err)
	}
	id, err := parseID(backupID)
	if err != nil {
		return RestoreResultDTO{}, mapError(err)
	}
	notes, err := parseIDs(noteIDs)
	if err != nil {
		return RestoreResultDTO{}, mapError(err)
	}
	result, err := acc.RestoreSelective(a.requestContext(), id, notes, destRoot, time.Now())
	if err != nil {
		return RestoreResultDTO{SafetyBackup: backupDTO(result.SafetyBackup)}, mapError(err)
	}
	return RestoreResultDTO{SafetyBackup: backupDTO(result.SafetyBackup), NewNoteIDs: idStrings(result.NewNoteIDs)}, nil
}

// RestoreWhole atomically replaces the entire local database with
// backupID's content, always taking a mandatory pre-restore safety backup
// under destRoot first.
func (a *App) RestoreWhole(backupID string, destRoot string) (RestoreResultDTO, error) {
	acc, err := a.currentAccount()
	if err != nil {
		return RestoreResultDTO{}, mapError(err)
	}
	id, err := parseID(backupID)
	if err != nil {
		return RestoreResultDTO{}, mapError(err)
	}
	result, err := acc.RestoreWhole(a.requestContext(), id, destRoot, time.Now())
	if err != nil {
		return RestoreResultDTO{SafetyBackup: backupDTO(result.SafetyBackup)}, mapError(err)
	}
	return RestoreResultDTO{SafetyBackup: backupDTO(result.SafetyBackup)}, nil
}
