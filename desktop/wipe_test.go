package main

import (
	"os"
	"testing"
)

func TestWipeLocalAccountRemovesFilesLocksAndClearsLastDatabasePath(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)

	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if got := a.GetSettings().LastDatabasePath; got != dbPath {
		t.Fatalf("LastDatabasePath after create = %q, want %q", got, dbPath)
	}

	if err := a.WipeLocalAccount(dbPath); err != nil {
		t.Fatalf("WipeLocalAccount: %v", err)
	}

	status, err := a.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Unlocked {
		t.Fatalf("Status after wipe = %+v, want locked", status)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("Stat(dbPath) after wipe error = %v, want os.IsNotExist", err)
	}
	if got := a.GetSettings().LastDatabasePath; got != "" {
		t.Fatalf("LastDatabasePath after wipe = %q, want empty", got)
	}
	if _, err := a.UnlockAccount(UnlockAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); !isAppErrorCode(err, ErrCodeNoLocalAccount) {
		t.Fatalf("UnlockAccount after wipe error = %v, want %s", err, ErrCodeNoLocalAccount)
	}
}

func TestWipeLocalAccountOnANeverCreatedPathIsNotAnError(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)

	if err := a.WipeLocalAccount(dbPath); err != nil {
		t.Fatalf("WipeLocalAccount on a never-created path error = %v, want nil", err)
	}
}
