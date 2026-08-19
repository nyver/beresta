package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/beresta-app/beresta/locales"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	a := newApp()
	// Tests never call startup(ctx): a.ready stays false, so emit and
	// runtimeContext behave as they would before the Wails window exists,
	// and requestContext falls back to context.Background(). The fake
	// factory avoids a real (slow, and potentially interactive) Windows
	// Hello/DPAPI round trip.
	a.keyWrapperFactory = fakeKeyWrapperFactory
	return a
}

// testDatabasePath returns a fresh temp-directory database path for a and
// registers a's Lock as a cleanup *after* that directory's own removal
// cleanup, so t.Cleanup's LIFO order closes the open SQLite file handle
// before Windows tries to delete the directory containing it.
func testDatabasePath(t *testing.T, a *App) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { _ = a.lockAccount() })
	return filepath.Join(dir, "beresta.db")
}

func TestCreateAccountThenStatusThenLock(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)

	info, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if info.AccountID == "" || info.DeviceID == "" || info.WorkspaceID == "" {
		t.Fatalf("CreateAccount info = %+v", info)
	}

	status, err := a.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !status.Unlocked || status.Account == nil || status.Account.AccountID != info.AccountID {
		t.Fatalf("Status = %+v", status)
	}

	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}
	status, err = a.Status()
	if err != nil {
		t.Fatalf("Status after lock: %v", err)
	}
	if status.Unlocked {
		t.Fatalf("Status after lock = %+v, want locked", status)
	}

	// Idempotent.
	if err := a.LockAccount(); err != nil {
		t.Fatalf("second LockAccount: %v", err)
	}
}

func TestCreateAccountTwiceAtSamePathFails(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)

	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}

	_, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "another passphrase"})
	assertAppErrorCode(t, err, ErrCodeAccountExists)
}

func TestUnlockAccountWithWrongPassphraseReportsUniformError(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)

	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}

	_, err := a.UnlockAccount(UnlockAccountRequest{DatabasePath: dbPath, Passphrase: "wrong passphrase"})
	assertAppErrorCode(t, err, ErrCodeUnlockFailed)
}

func TestUnlockAccountRoundTrip(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)

	created, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := a.LockAccount(); err != nil {
		t.Fatalf("LockAccount: %v", err)
	}

	unlocked, err := a.UnlockAccount(UnlockAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"})
	if err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}
	if unlocked.AccountID != created.AccountID || unlocked.WorkspaceID != created.WorkspaceID {
		t.Fatalf("UnlockAccount = %+v, want matching %+v", unlocked, created)
	}
}

func TestBoundMethodsRequireUnlockedAccount(t *testing.T) {
	a := newTestApp(t)

	if _, err := a.ListNotes(); !isAppErrorCode(err, ErrCodeLocked) {
		t.Fatalf("ListNotes on locked app error = %v, want %s", err, ErrCodeLocked)
	}
	if _, err := a.CreateNote("", "Untitled"); !isAppErrorCode(err, ErrCodeLocked) {
		t.Fatalf("CreateNote on locked app error = %v, want %s", err, ErrCodeLocked)
	}
	if _, err := a.Search("anything"); !isAppErrorCode(err, ErrCodeLocked) {
		t.Fatalf("Search on locked app error = %v, want %s", err, ErrCodeLocked)
	}
}

func TestNoteAndSearchLifecycle(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	note, err := a.CreateNote("", "Grocery list")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if note.ID == "" || note.WorkspaceID == "" || note.Title != "Grocery list" {
		t.Fatalf("CreateNote = %+v", note)
	}

	notes, err := a.ListNotes()
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].ID != note.ID {
		t.Fatalf("ListNotes = %+v", notes)
	}

	results, err := a.Search("Grocery")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != note.ID {
		t.Fatalf("Search = %+v", results)
	}

	if err := a.SetNoteFlags(note.ID, true, false); err != nil {
		t.Fatalf("SetNoteFlags: %v", err)
	}
	got, err := a.GetNote(note.ID)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if !got.Pinned || got.Archived {
		t.Fatalf("GetNote after SetNoteFlags = %+v", got)
	}

	if err := a.DeleteNote(note.ID); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	got, err = a.GetNote(note.ID)
	if err != nil {
		t.Fatalf("GetNote after delete: %v", err)
	}
	if !got.Deleted {
		t.Fatalf("GetNote after delete = %+v", got)
	}
	if err := a.RestoreNote(note.ID); err != nil {
		t.Fatalf("RestoreNote: %v", err)
	}
}

func TestManualBackupAndListBackups(t *testing.T) {
	a := newTestApp(t)
	dbPath := testDatabasePath(t, a)
	if _, err := a.CreateAccount(CreateAccountRequest{DatabasePath: dbPath, Passphrase: "correct horse battery staple"}); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	backup, err := a.CreateManualBackup(t.TempDir())
	if err != nil {
		t.Fatalf("CreateManualBackup: %v", err)
	}
	if backup.ID == "" || backup.Kind != "manual" {
		t.Fatalf("CreateManualBackup = %+v", backup)
	}

	list, err := a.ListBackups("manual")
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(list) != 1 || list[0].ID != backup.ID {
		t.Fatalf("ListBackups = %+v", list)
	}

	if _, err := a.ListBackups("not-a-kind"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("ListBackups(invalid) error = %v, want %s", err, ErrCodeInvalidInput)
	}
}

func TestSettingsRoundTripAndValidation(t *testing.T) {
	a := newTestApp(t)

	got := a.GetSettings()
	if got.Language != locales.English {
		t.Fatalf("GetSettings() default language = %q", got.Language)
	}

	updated, err := a.UpdateSettings(AppSettings{Language: locales.Russian, AutoLockMinutes: 5, BackupDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if updated.Language != locales.Russian || updated.AutoLockMinutes != 5 {
		t.Fatalf("UpdateSettings = %+v", updated)
	}
	if got := a.GetSettings(); got.Language != locales.Russian {
		t.Fatalf("GetSettings after update = %+v", got)
	}

	if _, err := a.UpdateSettings(AppSettings{Language: "fr"}); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("UpdateSettings(unsupported language) error = %v, want %s", err, ErrCodeInvalidInput)
	}
	if _, err := a.UpdateSettings(AppSettings{Language: locales.English, AutoLockMinutes: -1}); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("UpdateSettings(negative auto-lock) error = %v, want %s", err, ErrCodeInvalidInput)
	}
}

func TestCatalogReturnsRequestedLocale(t *testing.T) {
	a := newTestApp(t)

	catalog, err := a.Catalog(locales.Russian)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if catalog.Locale != locales.Russian || len(catalog.Strings) == 0 {
		t.Fatalf("Catalog(ru) = %+v", catalog)
	}

	if _, err := a.Catalog("fr"); !isAppErrorCode(err, ErrCodeInvalidInput) {
		t.Fatalf("Catalog(fr) error = %v, want %s", err, ErrCodeInvalidInput)
	}
}

func TestSyncStatusReportsDisabledForLocalTransport(t *testing.T) {
	a := newTestApp(t)
	if got := a.SyncStatus(); got != "disabled" {
		t.Fatalf("SyncStatus() = %q, want %q", got, "disabled")
	}
}

func TestEmitIsNoOpBeforeStartup(t *testing.T) {
	a := newTestApp(t)
	// Must not panic or call log.Fatalf (see events.go's doc comment):
	// a.ready is false because startup(ctx) was never called.
	a.emit(EventAccountLocked)
}

func TestRuntimeContextFailsBeforeStartup(t *testing.T) {
	a := newTestApp(t)
	if _, err := a.runtimeContext(); err == nil {
		t.Fatal("runtimeContext() before startup: error = nil, want error")
	}
}

func TestRequestContextFallsBackToBackground(t *testing.T) {
	a := newTestApp(t)
	if ctx := a.requestContext(); ctx != context.Background() {
		t.Fatalf("requestContext() before startup = %v, want context.Background()", ctx)
	}
}

func assertAppErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if !isAppErrorCode(err, code) {
		t.Fatalf("error = %v, want AppError code %q", err, code)
	}
}

func isAppErrorCode(err error, code string) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Code == code
}
