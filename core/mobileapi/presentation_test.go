package mobileapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/presentation"
)

func TestMarshalSyncSummary(t *testing.T) {
	summary := presentation.SyncSummary{
		State:          presentation.SyncStateRetrying,
		PendingCount:   3,
		LastSuccess:    time.UnixMilli(1_700_000_000_000).UTC(),
		RetryIn:        2500 * time.Millisecond,
		UnsafeCount:    1,
		ActionRequired: presentation.RecoveryActionNone,
	}
	encoded, err := MarshalSyncSummary(summary)
	if err != nil {
		t.Fatalf("MarshalSyncSummary: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	want := map[string]any{
		"state":                "retrying",
		"pending_count":        float64(3),
		"last_success_unix_ms": float64(1_700_000_000_000),
		"retry_in_ms":          float64(2500),
		"unsafe_count":         float64(1),
		"action_required":      "none",
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Errorf("field %q = %v, want %v", key, got[key], wantValue)
		}
	}
}

func TestMarshalSyncSummaryZeroLastSuccess(t *testing.T) {
	encoded, err := MarshalSyncSummary(presentation.SyncSummary{State: presentation.SyncStateLocalOnly})
	if err != nil {
		t.Fatalf("MarshalSyncSummary: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got["last_success_unix_ms"] != float64(0) {
		t.Errorf("last_success_unix_ms = %v, want 0", got["last_success_unix_ms"])
	}
}

func TestMarshalUserError(t *testing.T) {
	value := presentation.NewUserError(presentation.ErrorCategoryDeviceRevoked, true, false, true)
	encoded, err := MarshalUserError(value)
	if err != nil {
		t.Fatalf("MarshalUserError: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	want := map[string]any{
		"category":             "device_revoked",
		"local_data_saved":     true,
		"automatic_retry":      false,
		"requires_user_action": true,
		"primary_action":       "reconnect_device",
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Errorf("field %q = %v, want %v", key, got[key], wantValue)
		}
	}
}

func TestMarshalBackupStatus(t *testing.T) {
	status := presentation.BackupStatus{
		Health:       presentation.BackupHealthWarning,
		LastVerified: time.UnixMilli(1_700_000_000_000).UTC(),
		Location:     "Documents\\Beresta Backups",
	}
	encoded, err := MarshalBackupStatus(status)
	if err != nil {
		t.Fatalf("MarshalBackupStatus: %v", err)
	}
	var got backupStatusDTO
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.Health != status.Health || got.Location != status.Location {
		t.Errorf("got %+v, want Health=%q Location=%q", got, status.Health, status.Location)
	}
	if got.LastVerifiedUnixMS != status.LastVerified.UnixMilli() {
		t.Errorf("LastVerifiedUnixMS = %d, want %d", got.LastVerifiedUnixMS, status.LastVerified.UnixMilli())
	}
}

func TestMarshalDiagnosticSummaryExcludesUnknownFields(t *testing.T) {
	summary := presentation.DiagnosticSummary{
		AppVersion:      "1.2.3",
		Platform:        "android",
		SyncConfigured:  true,
		PendingCount:    5,
		ConnectionState: presentation.SyncStateOffline,
		Backup: presentation.BackupStatus{
			Health: presentation.BackupHealthHealthy,
		},
		StorageUsageBytes: 1024,
		Database:          presentation.DatabaseHealthOK,
		Update:            presentation.UpdateStatusUpToDate,
	}
	encoded, err := MarshalDiagnosticSummary(summary)
	if err != nil {
		t.Fatalf("MarshalDiagnosticSummary: %v", err)
	}
	// Decode strictly: any field not in diagnosticSummaryDTO (for example a
	// stray note title or identifier) fails this test.
	if err := strictJSON(encoded, new(diagnosticSummaryDTO)); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
}
