package main

import (
	"time"

	"github.com/beresta-app/beresta/core/presentation"
)

// SyncSummaryDTO is the desktop JS-bridge projection of
// presentation.SyncSummary. Time and duration fields use the bridge's
// millisecond-epoch convention (see desktop/notes.go, desktop/sync.go)
// instead of Go's time.Time/time.Duration JSON encoding.
type SyncSummaryDTO struct {
	State             presentation.SyncState      `json:"state"`
	PendingCount      int                         `json:"pending_count"`
	LastSuccessUnixMS int64                       `json:"last_success_unix_ms"`
	RetryInMS         int64                       `json:"retry_in_ms"`
	UnsafeCount       int                         `json:"unsafe_count"`
	ActionRequired    presentation.RecoveryAction `json:"action_required"`
}

func newSyncSummaryDTO(summary presentation.SyncSummary) SyncSummaryDTO {
	return SyncSummaryDTO{
		State:             summary.State,
		PendingCount:      summary.PendingCount,
		LastSuccessUnixMS: unixMS(summary.LastSuccess),
		RetryInMS:         summary.RetryIn.Milliseconds(),
		UnsafeCount:       summary.UnsafeCount,
		ActionRequired:    summary.ActionRequired,
	}
}

// UserErrorDTO is the desktop JS-bridge projection of
// presentation.UserError.
type UserErrorDTO struct {
	Category           presentation.UserErrorCategory `json:"category"`
	LocalDataSaved     bool                           `json:"local_data_saved"`
	AutomaticRetry     bool                           `json:"automatic_retry"`
	RequiresUserAction bool                           `json:"requires_user_action"`
	PrimaryAction      presentation.RecoveryAction    `json:"primary_action"`
}

func newUserErrorDTO(value presentation.UserError) UserErrorDTO {
	return UserErrorDTO{
		Category:           value.Category,
		LocalDataSaved:     value.LocalDataSaved,
		AutomaticRetry:     value.AutomaticRetry,
		RequiresUserAction: value.RequiresUserAction,
		PrimaryAction:      value.PrimaryAction,
	}
}

// BackupStatusDTO is the desktop JS-bridge projection of
// presentation.BackupStatus.
type BackupStatusDTO struct {
	Health             presentation.BackupHealth `json:"health"`
	LastVerifiedUnixMS int64                     `json:"last_verified_unix_ms"`
	Location           string                    `json:"location"`
}

func newBackupStatusDTO(status presentation.BackupStatus) BackupStatusDTO {
	return BackupStatusDTO{
		Health:             status.Health,
		LastVerifiedUnixMS: unixMS(status.LastVerified),
		Location:           status.Location,
	}
}

// DiagnosticSummaryDTO is the desktop JS-bridge projection of
// presentation.DiagnosticSummary.
type DiagnosticSummaryDTO struct {
	AppVersion               string                      `json:"app_version"`
	Platform                 string                      `json:"platform"`
	SyncConfigured           bool                        `json:"sync_configured"`
	LastSuccessfulSyncUnixMS int64                       `json:"last_successful_sync_unix_ms"`
	PendingCount             int                         `json:"pending_count"`
	ConnectionState          presentation.SyncState      `json:"connection_state"`
	Backup                   BackupStatusDTO             `json:"backup"`
	StorageUsageBytes        int64                       `json:"storage_usage_bytes"`
	Database                 presentation.DatabaseHealth `json:"database"`
	Update                   presentation.UpdateStatus   `json:"update"`
}

func newDiagnosticSummaryDTO(summary presentation.DiagnosticSummary) DiagnosticSummaryDTO {
	return DiagnosticSummaryDTO{
		AppVersion:               summary.AppVersion,
		Platform:                 summary.Platform,
		SyncConfigured:           summary.SyncConfigured,
		LastSuccessfulSyncUnixMS: unixMS(summary.LastSuccessfulSync),
		PendingCount:             summary.PendingCount,
		ConnectionState:          summary.ConnectionState,
		Backup:                   newBackupStatusDTO(summary.Backup),
		StorageUsageBytes:        summary.StorageUsageBytes,
		Database:                 summary.Database,
		Update:                   summary.Update,
	}
}

// unixMS renders t in the bridge's millisecond-epoch convention. The zero
// time.Time (never happened) renders as 0, matching the "never" sentinel
// every other bridge timestamp field already uses.
func unixMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
