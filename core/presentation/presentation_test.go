package presentation

import "testing"

func TestLocalSaveStateValid(t *testing.T) {
	tests := []struct {
		name string
		s    LocalSaveState
		want bool
	}{
		{"saving", SaveStateSaving, true},
		{"saved", SaveStateSaved, true},
		{"could not save", SaveStateCouldNotSave, true},
		{"empty", LocalSaveState(""), false},
		{"unknown", LocalSaveState("uploading"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Valid(); got != tt.want {
				t.Fatalf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSyncStateValid(t *testing.T) {
	valid := []SyncState{
		SyncStateLocalOnly, SyncStateCurrent, SyncStateActive, SyncStateOffline,
		SyncStatePending, SyncStateRetrying, SyncStateActionRequired,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("SyncState(%q).Valid() = false, want true", s)
		}
	}
	invalid := []SyncState{"", "syncing", "connected"}
	for _, s := range invalid {
		if s.Valid() {
			t.Errorf("SyncState(%q).Valid() = true, want false", s)
		}
	}
}

func TestUserErrorCategoryValid(t *testing.T) {
	valid := []UserErrorCategory{
		ErrorCategoryNoNetwork, ErrorCategoryServerUnavailable,
		ErrorCategoryTrustOrConfiguration, ErrorCategoryDeviceRevoked,
		ErrorCategoryUnsafeIncomingChange,
	}
	for _, c := range valid {
		if !c.Valid() {
			t.Errorf("UserErrorCategory(%q).Valid() = false, want true", c)
		}
	}
	if (UserErrorCategory("timeout")).Valid() {
		t.Error("UserErrorCategory(\"timeout\").Valid() = true, want false")
	}
}

func TestPrimaryRecoveryActionCoversEveryCategory(t *testing.T) {
	tests := []struct {
		category UserErrorCategory
		want     RecoveryAction
	}{
		{ErrorCategoryNoNetwork, RecoveryActionRetryNow},
		{ErrorCategoryServerUnavailable, RecoveryActionRetryNow},
		{ErrorCategoryTrustOrConfiguration, RecoveryActionReviewConnection},
		{ErrorCategoryDeviceRevoked, RecoveryActionReconnectDevice},
		{ErrorCategoryUnsafeIncomingChange, RecoveryActionReviewUnsafeChange},
		{UserErrorCategory("unknown"), RecoveryActionNone},
	}
	for _, tt := range tests {
		t.Run(string(tt.category), func(t *testing.T) {
			got := PrimaryRecoveryAction(tt.category)
			if got != tt.want {
				t.Fatalf("PrimaryRecoveryAction(%q) = %q, want %q", tt.category, got, tt.want)
			}
			if !got.Valid() {
				t.Fatalf("PrimaryRecoveryAction(%q) = %q, not a valid RecoveryAction", tt.category, got)
			}
		})
	}
}

func TestNewUserErrorFillsPrimaryAction(t *testing.T) {
	err := NewUserError(ErrorCategoryDeviceRevoked, true, false, true)
	if err.Category != ErrorCategoryDeviceRevoked {
		t.Fatalf("Category = %q, want %q", err.Category, ErrorCategoryDeviceRevoked)
	}
	if err.PrimaryAction != RecoveryActionReconnectDevice {
		t.Fatalf("PrimaryAction = %q, want %q", err.PrimaryAction, RecoveryActionReconnectDevice)
	}
	if !err.LocalDataSaved || err.AutomaticRetry || !err.RequiresUserAction {
		t.Fatalf("unexpected flags: %+v", err)
	}
}

func TestBackupHealthValid(t *testing.T) {
	valid := []BackupHealth{BackupHealthHealthy, BackupHealthWarning, BackupHealthCorrupt, BackupHealthUnknown}
	for _, h := range valid {
		if !h.Valid() {
			t.Errorf("BackupHealth(%q).Valid() = false, want true", h)
		}
	}
	if (BackupHealth("missing")).Valid() {
		t.Error("BackupHealth(\"missing\").Valid() = true, want false")
	}
}

func TestDatabaseHealthValid(t *testing.T) {
	valid := []DatabaseHealth{DatabaseHealthOK, DatabaseHealthDegraded, DatabaseHealthUnknown}
	for _, h := range valid {
		if !h.Valid() {
			t.Errorf("DatabaseHealth(%q).Valid() = false, want true", h)
		}
	}
	if (DatabaseHealth("broken")).Valid() {
		t.Error("DatabaseHealth(\"broken\").Valid() = true, want false")
	}
}

func TestUpdateStatusValid(t *testing.T) {
	valid := []UpdateStatus{
		UpdateStatusUpToDate, UpdateStatusAvailable, UpdateStatusDownloading,
		UpdateStatusReadyToInstall, UpdateStatusFailed, UpdateStatusUnknown,
	}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("UpdateStatus(%q).Valid() = false, want true", s)
		}
	}
	if (UpdateStatus("installing")).Valid() {
		t.Error("UpdateStatus(\"installing\").Valid() = true, want false")
	}
}

func TestDiagnosticSummaryZeroValueHasNoNoteData(t *testing.T) {
	var summary DiagnosticSummary
	if summary.AppVersion != "" || summary.Platform != "" {
		t.Fatalf("zero-value DiagnosticSummary should have empty identity fields, got %+v", summary)
	}
	if summary.PendingCount != 0 || summary.StorageUsageBytes != 0 {
		t.Fatalf("zero-value DiagnosticSummary should have zero counters, got %+v", summary)
	}
	if !summary.LastSuccessfulSync.IsZero() || !summary.Backup.LastVerified.IsZero() {
		t.Fatalf("zero-value DiagnosticSummary should have zero-time fields, got %+v", summary)
	}
}
