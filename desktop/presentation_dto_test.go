package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/mobileapi"
	"github.com/beresta-app/beresta/core/presentation"
	"github.com/beresta-app/beresta/core/presentation/presentationtest"
)

// TestSyncStateParityAcrossPlatforms proves that, for every named
// presentation.SyncState fixture (local-only, current, active, offline,
// pending, retrying, action-required), the desktop bridge and the
// gomobile-safe Android adapter derive the identical semantic state from
// the identical shared presentation.SyncSummary value. Wails serializes a
// bound method's struct return value with the same encoding/json used
// here, so encoding the desktop DTO directly is equivalent to what
// crosses the real JS bridge.
func TestSyncStateParityAcrossPlatforms(t *testing.T) {
	for _, tt := range presentationtest.SyncCases() {
		t.Run(tt.Name, func(t *testing.T) {
			desktopEncoded, err := json.Marshal(newSyncSummaryDTO(tt.Summary))
			if err != nil {
				t.Fatalf("json.Marshal(desktop DTO): %v", err)
			}
			var desktopGot SyncSummaryDTO
			if err := json.Unmarshal(desktopEncoded, &desktopGot); err != nil {
				t.Fatalf("json.Unmarshal(desktop JSON): %v", err)
			}

			mobileEncoded, err := mobileapi.MarshalSyncSummary(tt.Summary)
			if err != nil {
				t.Fatalf("mobileapi.MarshalSyncSummary: %v", err)
			}
			var mobileGot SyncSummaryDTO
			if err := json.Unmarshal([]byte(mobileEncoded), &mobileGot); err != nil {
				t.Fatalf("json.Unmarshal(mobile JSON): %v", err)
			}

			if desktopGot != mobileGot {
				t.Fatalf("desktop = %+v, mobile = %+v", desktopGot, mobileGot)
			}
			if desktopGot.State != tt.Summary.State {
				t.Fatalf("State = %q, want fixture state %q", desktopGot.State, tt.Summary.State)
			}
		})
	}
}

// TestLocalSaveStateParityAcrossPlatforms proves the same parity for
// every named presentation.LocalSaveState fixture (saving, saved,
// could-not-save).
func TestLocalSaveStateParityAcrossPlatforms(t *testing.T) {
	for _, tt := range presentationtest.LocalSaveCases() {
		t.Run(tt.Name, func(t *testing.T) {
			desktopEncoded, err := json.Marshal(tt.State)
			if err != nil {
				t.Fatalf("json.Marshal(desktop LocalSaveState): %v", err)
			}

			mobileEncoded, err := mobileapi.MarshalLocalSaveState(tt.State)
			if err != nil {
				t.Fatalf("mobileapi.MarshalLocalSaveState: %v", err)
			}

			if string(desktopEncoded) != mobileEncoded {
				t.Fatalf("desktop = %s, mobile = %s", desktopEncoded, mobileEncoded)
			}
		})
	}
}

func TestNewSyncSummaryDTO(t *testing.T) {
	summary := presentation.SyncSummary{
		State:          presentation.SyncStatePending,
		PendingCount:   7,
		LastSuccess:    time.UnixMilli(1_700_000_000_000).UTC(),
		RetryIn:        1500 * time.Millisecond,
		UnsafeCount:    2,
		ActionRequired: presentation.RecoveryActionNone,
	}
	dto := newSyncSummaryDTO(summary)
	if dto.State != summary.State || dto.PendingCount != summary.PendingCount || dto.UnsafeCount != summary.UnsafeCount {
		t.Fatalf("dto = %+v, source = %+v", dto, summary)
	}
	if dto.LastSuccessUnixMS != summary.LastSuccess.UnixMilli() {
		t.Errorf("LastSuccessUnixMS = %d, want %d", dto.LastSuccessUnixMS, summary.LastSuccess.UnixMilli())
	}
	if dto.RetryInMS != summary.RetryIn.Milliseconds() {
		t.Errorf("RetryInMS = %d, want %d", dto.RetryInMS, summary.RetryIn.Milliseconds())
	}
}

func TestNewSyncSummaryDTOZeroLastSuccess(t *testing.T) {
	dto := newSyncSummaryDTO(presentation.SyncSummary{State: presentation.SyncStateLocalOnly})
	if dto.LastSuccessUnixMS != 0 {
		t.Errorf("LastSuccessUnixMS = %d, want 0 for a never-synced summary", dto.LastSuccessUnixMS)
	}
}

func TestNewUserErrorDTO(t *testing.T) {
	value := presentation.NewUserError(presentation.ErrorCategoryUnsafeIncomingChange, true, false, true)
	dto := newUserErrorDTO(value)
	if dto.Category != value.Category || dto.PrimaryAction != value.PrimaryAction {
		t.Fatalf("dto = %+v, source = %+v", dto, value)
	}
}

func TestNewBackupStatusDTO(t *testing.T) {
	status := presentation.BackupStatus{
		Health:       presentation.BackupHealthCorrupt,
		LastVerified: time.UnixMilli(1_690_000_000_000).UTC(),
		Location:     "\\\\NAS\\beresta-backups",
	}
	dto := newBackupStatusDTO(status)
	if dto.Health != status.Health || dto.Location != status.Location {
		t.Fatalf("dto = %+v, source = %+v", dto, status)
	}
	if dto.LastVerifiedUnixMS != status.LastVerified.UnixMilli() {
		t.Errorf("LastVerifiedUnixMS = %d, want %d", dto.LastVerifiedUnixMS, status.LastVerified.UnixMilli())
	}
}

func TestNewDiagnosticSummaryDTO(t *testing.T) {
	summary := presentation.DiagnosticSummary{
		AppVersion:        "2.0.0",
		Platform:          "windows",
		SyncConfigured:    true,
		PendingCount:      4,
		ConnectionState:   presentation.SyncStateActive,
		Backup:            presentation.BackupStatus{Health: presentation.BackupHealthHealthy},
		StorageUsageBytes: 2048,
		Database:          presentation.DatabaseHealthDegraded,
		Update:            presentation.UpdateStatusAvailable,
	}
	dto := newDiagnosticSummaryDTO(summary)
	if dto.AppVersion != summary.AppVersion || dto.Platform != summary.Platform {
		t.Fatalf("dto = %+v, source = %+v", dto, summary)
	}
	if dto.Backup.Health != summary.Backup.Health {
		t.Errorf("Backup.Health = %q, want %q", dto.Backup.Health, summary.Backup.Health)
	}
}

// TestPresentationDTOCompatibility proves the desktop typed DTOs and the
// core/mobileapi gomobile-safe JSON adapters serialize the same shared
// presentation.* fixtures to the identical JSON schema, so Windows and
// Android derive equivalent semantic state from equivalent wire shapes
// (design.md decision 1: "A contract test will feed common fixtures
// through both adapters and assert equivalent semantic output").
func TestPresentationDTOCompatibility(t *testing.T) {
	fixtureTime := time.UnixMilli(1_700_000_000_000).UTC()

	syncSummary := presentation.SyncSummary{
		State:          presentation.SyncStateActionRequired,
		PendingCount:   9,
		LastSuccess:    fixtureTime,
		RetryIn:        4 * time.Second,
		UnsafeCount:    3,
		ActionRequired: presentation.RecoveryActionReviewUnsafeChange,
	}
	userError := presentation.NewUserError(presentation.ErrorCategoryTrustOrConfiguration, true, false, true)
	backupStatus := presentation.BackupStatus{
		Health:       presentation.BackupHealthWarning,
		LastVerified: fixtureTime,
		Location:     "D:\\Backups",
	}
	diagnosticSummary := presentation.DiagnosticSummary{
		AppVersion:         "3.1.4",
		Platform:           "windows",
		SyncConfigured:     true,
		LastSuccessfulSync: fixtureTime,
		PendingCount:       9,
		ConnectionState:    presentation.SyncStateActionRequired,
		Backup:             backupStatus,
		StorageUsageBytes:  123456,
		Database:           presentation.DatabaseHealthOK,
		Update:             presentation.UpdateStatusReadyToInstall,
	}
	technicalDiagnostics := presentation.TechnicalDiagnostics{
		WorkspaceID: "ws-1", DeviceID: "device-1", LastErrorClass: "transient_transport",
		PendingOperationCount: 9, QuarantinedOperationIDs: []string{"op-1", "op-2"},
		CursorSequence: 42, CursorEpoch: 1, RetryCount: 3, RetryIn: 6 * time.Second,
		TransportProtocol: "https", TransportSecurityMode: "pinned", TransportURL: "https://home.example:8443",
		MigrationVersion: 12,
	}

	assertEquivalentJSON(t, "SyncSummary", newSyncSummaryDTO(syncSummary), func() (string, error) {
		return mobileapi.MarshalSyncSummary(syncSummary)
	})
	assertEquivalentJSON(t, "UserError", newUserErrorDTO(userError), func() (string, error) {
		return mobileapi.MarshalUserError(userError)
	})
	assertEquivalentJSON(t, "BackupStatus", newBackupStatusDTO(backupStatus), func() (string, error) {
		return mobileapi.MarshalBackupStatus(backupStatus)
	})
	assertEquivalentJSON(t, "DiagnosticSummary", newDiagnosticSummaryDTO(diagnosticSummary), func() (string, error) {
		return mobileapi.MarshalDiagnosticSummary(diagnosticSummary)
	})
	assertEquivalentJSON(t, "TechnicalDiagnostics", newTechnicalDiagnosticsDTO(technicalDiagnostics), func() (string, error) {
		return mobileapi.MarshalTechnicalDiagnostics(technicalDiagnostics)
	})
}

// assertEquivalentJSON marshals the desktop DTO, calls the mobile adapter,
// and asserts both produce byte-for-byte equal JSON key/value sets.
func assertEquivalentJSON(t *testing.T, label string, desktopDTO any, mobileMarshal func() (string, error)) {
	t.Helper()

	desktopEncoded, err := json.Marshal(desktopDTO)
	if err != nil {
		t.Fatalf("%s: json.Marshal(desktop DTO): %v", label, err)
	}
	var desktopFields map[string]any
	if err := json.Unmarshal(desktopEncoded, &desktopFields); err != nil {
		t.Fatalf("%s: json.Unmarshal(desktop JSON): %v", label, err)
	}

	mobileEncoded, err := mobileMarshal()
	if err != nil {
		t.Fatalf("%s: mobile marshal: %v", label, err)
	}
	var mobileFields map[string]any
	if err := json.Unmarshal([]byte(mobileEncoded), &mobileFields); err != nil {
		t.Fatalf("%s: json.Unmarshal(mobile JSON): %v", label, err)
	}

	if len(desktopFields) != len(mobileFields) {
		t.Fatalf("%s: desktop has %d fields, mobile has %d\ndesktop: %s\nmobile:  %s",
			label, len(desktopFields), len(mobileFields), desktopEncoded, mobileEncoded)
	}
	for key, desktopValue := range desktopFields {
		mobileValue, ok := mobileFields[key]
		if !ok {
			t.Errorf("%s: field %q present on desktop but missing on mobile", label, key)
			continue
		}
		if !jsonEqual(desktopValue, mobileValue) {
			t.Errorf("%s: field %q = %v (desktop) vs %v (mobile)", label, key, desktopValue, mobileValue)
		}
	}
}

func jsonEqual(a, b any) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(encodedA) == string(encodedB)
}
