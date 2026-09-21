package mobileapi

import (
	"time"

	"github.com/beresta-app/beresta/core/presentation"
)

// MarshalLocalSaveState renders state as the strict JSON string value a
// gomobile caller uses for the editor's save-status display.
func MarshalLocalSaveState(state presentation.LocalSaveState) (string, error) {
	return marshal(state)
}

// syncSummaryDTO is the gomobile-safe JSON projection of
// presentation.SyncSummary. Its field names and JSON schema match the
// desktop bridge's SyncSummaryDTO (see desktop/presentation_dto.go) so
// both clients derive identical semantic state from identical wire
// shapes.
type syncSummaryDTO struct {
	State             presentation.SyncState      `json:"state"`
	PendingCount      int                         `json:"pending_count"`
	LastSuccessUnixMS int64                       `json:"last_success_unix_ms"`
	RetryInMS         int64                       `json:"retry_in_ms"`
	UnsafeCount       int                         `json:"unsafe_count"`
	ActionRequired    presentation.RecoveryAction `json:"action_required"`
}

func newSyncSummaryDTO(summary presentation.SyncSummary) syncSummaryDTO {
	return syncSummaryDTO{
		State:             summary.State,
		PendingCount:      summary.PendingCount,
		LastSuccessUnixMS: unixMS(summary.LastSuccess),
		RetryInMS:         summary.RetryIn.Milliseconds(),
		UnsafeCount:       summary.UnsafeCount,
		ActionRequired:    summary.ActionRequired,
	}
}

// MarshalSyncSummary renders summary as the strict JSON string a gomobile
// caller polls for synchronization status.
func MarshalSyncSummary(summary presentation.SyncSummary) (string, error) {
	return marshal(newSyncSummaryDTO(summary))
}

// userErrorDTO is the gomobile-safe JSON projection of
// presentation.UserError, matching the desktop bridge's UserErrorDTO.
type userErrorDTO struct {
	Category           presentation.UserErrorCategory `json:"category"`
	LocalDataSaved     bool                           `json:"local_data_saved"`
	AutomaticRetry     bool                           `json:"automatic_retry"`
	RequiresUserAction bool                           `json:"requires_user_action"`
	PrimaryAction      presentation.RecoveryAction    `json:"primary_action"`
}

func newUserErrorDTO(value presentation.UserError) userErrorDTO {
	return userErrorDTO{
		Category:           value.Category,
		LocalDataSaved:     value.LocalDataSaved,
		AutomaticRetry:     value.AutomaticRetry,
		RequiresUserAction: value.RequiresUserAction,
		PrimaryAction:      value.PrimaryAction,
	}
}

// MarshalUserError renders value as the strict JSON string a gomobile
// caller uses to present an actionable error.
func MarshalUserError(value presentation.UserError) (string, error) {
	return marshal(newUserErrorDTO(value))
}

// backupStatusDTO is the gomobile-safe JSON projection of
// presentation.BackupStatus, matching the desktop bridge's
// BackupStatusDTO.
type backupStatusDTO struct {
	Health             presentation.BackupHealth `json:"health"`
	LastVerifiedUnixMS int64                     `json:"last_verified_unix_ms"`
	Location           string                    `json:"location"`
}

func newBackupStatusDTO(status presentation.BackupStatus) backupStatusDTO {
	return backupStatusDTO{
		Health:             status.Health,
		LastVerifiedUnixMS: unixMS(status.LastVerified),
		Location:           status.Location,
	}
}

// MarshalBackupStatus renders status as the strict JSON string a gomobile
// caller uses to present backup health.
func MarshalBackupStatus(status presentation.BackupStatus) (string, error) {
	return marshal(newBackupStatusDTO(status))
}

// diagnosticSummaryDTO is the gomobile-safe JSON projection of
// presentation.DiagnosticSummary, matching the desktop bridge's
// DiagnosticSummaryDTO.
type diagnosticSummaryDTO struct {
	AppVersion               string                      `json:"app_version"`
	Platform                 string                      `json:"platform"`
	SyncConfigured           bool                        `json:"sync_configured"`
	LastSuccessfulSyncUnixMS int64                       `json:"last_successful_sync_unix_ms"`
	PendingCount             int                         `json:"pending_count"`
	ConnectionState          presentation.SyncState      `json:"connection_state"`
	Backup                   backupStatusDTO             `json:"backup"`
	StorageUsageBytes        int64                       `json:"storage_usage_bytes"`
	Database                 presentation.DatabaseHealth `json:"database"`
	Update                   presentation.UpdateStatus   `json:"update"`
}

func newDiagnosticSummaryDTO(summary presentation.DiagnosticSummary) diagnosticSummaryDTO {
	return diagnosticSummaryDTO{
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

// MarshalDiagnosticSummary renders summary as the strict JSON string a
// gomobile caller uses to populate the user diagnostics screen.
func MarshalDiagnosticSummary(summary presentation.DiagnosticSummary) (string, error) {
	return marshal(newDiagnosticSummaryDTO(summary))
}

// technicalDiagnosticsDTO is the gomobile-safe JSON projection of
// presentation.TechnicalDiagnostics, matching the desktop bridge's
// TechnicalDiagnosticsDTO.
type technicalDiagnosticsDTO struct {
	WorkspaceID             string   `json:"workspace_id"`
	DeviceID                string   `json:"device_id"`
	LastErrorClass          string   `json:"last_error_class"`
	PendingOperationCount   int      `json:"pending_operation_count"`
	QuarantinedOperationIDs []string `json:"quarantined_operation_ids"`
	CursorSequence          uint64   `json:"cursor_sequence"`
	CursorEpoch             uint32   `json:"cursor_epoch"`
	RetryCount              int      `json:"retry_count"`
	RetryInMS               int64    `json:"retry_in_ms"`
	TransportProtocol       string   `json:"transport_protocol"`
	TransportSecurityMode   string   `json:"transport_security_mode"`
	TransportURL            string   `json:"transport_url"`
	MigrationVersion        int      `json:"migration_version"`
	RotationPending         bool     `json:"rotation_pending"`
}

func newTechnicalDiagnosticsDTO(technical presentation.TechnicalDiagnostics) technicalDiagnosticsDTO {
	quarantined := technical.QuarantinedOperationIDs
	if quarantined == nil {
		quarantined = []string{}
	}
	return technicalDiagnosticsDTO{
		WorkspaceID:             technical.WorkspaceID,
		DeviceID:                technical.DeviceID,
		LastErrorClass:          technical.LastErrorClass,
		PendingOperationCount:   technical.PendingOperationCount,
		QuarantinedOperationIDs: quarantined,
		CursorSequence:          technical.CursorSequence,
		CursorEpoch:             technical.CursorEpoch,
		RetryCount:              technical.RetryCount,
		RetryInMS:               technical.RetryIn.Milliseconds(),
		TransportProtocol:       technical.TransportProtocol,
		TransportSecurityMode:   technical.TransportSecurityMode,
		TransportURL:            technical.TransportURL,
		MigrationVersion:        technical.MigrationVersion,
		RotationPending:         technical.RotationPending,
	}
}

// MarshalTechnicalDiagnostics renders technical as the strict JSON string a
// gomobile caller uses to populate the "expand technical details" layer of
// the user diagnostics screen.
func MarshalTechnicalDiagnostics(technical presentation.TechnicalDiagnostics) (string, error) {
	return marshal(newTechnicalDiagnosticsDTO(technical))
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
