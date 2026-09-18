package main

import "testing"

// TestEstimateBackupSizeIsPositiveForANonEmptyAccount covers task 5.7's
// storage-pressure estimate bridge method: a freshly created account
// already has a database, so the estimate must be a positive number of
// bytes usable for display before the user commits to a backup.
func TestEstimateBackupSizeIsPositiveForANonEmptyAccount(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	estimated, err := a.EstimateBackupSize()
	if err != nil {
		t.Fatalf("EstimateBackupSize: %v", err)
	}
	if estimated <= 0 {
		t.Fatalf("EstimateBackupSize = %d, want > 0", estimated)
	}
}
