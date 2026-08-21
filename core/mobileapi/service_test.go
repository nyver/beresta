package mobileapi

import (
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"
)

func newTestServiceDeviceSecret(t *testing.T) []byte {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	return secret
}

func decodeJSON[T any](t *testing.T, encoded string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(encoded), &value); err != nil {
		t.Fatalf("decode %q: %v", encoded, err)
	}
	return value
}

// TestServiceFullAccountLifecycle exercises the gomobile-facing Service
// facade end to end: account creation, notes, search, revisions, backups,
// events, and locking. This is the primary boundary Android calls through
// (see docs/architecture.md), so one broad happy-path pass through it
// catches JSON-shape and wiring regressions a lower-level core/account test
// would not.
func TestServiceFullAccountLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)
	must := func(value string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	statusBefore := decodeJSON[map[string]any](t, must(service.Status()))
	if statusBefore["unlocked"] != false {
		t.Fatalf("expected a fresh service to report locked, got %v", statusBefore)
	}

	created, err := service.CreateAccount("create-1", dbPath, "correct horse battery staple")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	createdInfo := decodeJSON[map[string]any](t, created)
	if createdInfo["workspace_id"] == "" || createdInfo["account_id"] == "" {
		t.Fatalf("unexpected CreateAccount response: %v", createdInfo)
	}

	// A request ID cannot be reused while still active, but begin/done
	// releases it immediately for a synchronous call like this one, so a
	// second call with the same ID must succeed.
	if _, err := service.CreateNote("reuse-check", "", "first"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := service.ListNotes("reuse-check"); err != nil {
		t.Fatalf("expected request ID reuse after completion to succeed: %v", err)
	}

	noteJSON, err := service.CreateNote("create-note", "", "My Note")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	note := decodeJSON[map[string]any](t, noteJSON)
	noteID, _ := note["id"].(string)
	if noteID == "" {
		t.Fatalf("unexpected CreateNote response: %v", note)
	}

	if err := service.SaveNote("save-note", noteID, "My Note", "hello from the mobile facade"); err != nil {
		t.Fatalf("SaveNote: %v", err)
	}

	fetched := decodeJSON[map[string]any](t, must(service.GetNote("get-note", noteID)))
	fetchedNote, _ := fetched["note"].(map[string]any)
	if fetchedNote["title"] != "My Note" || fetched["body"] != "hello from the mobile facade" {
		t.Fatalf("unexpected GetNote response: %v", fetched)
	}

	listed := decodeJSON[[]map[string]any](t, must(service.ListNotes("list-notes")))
	if len(listed) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(listed))
	}

	searchResults := decodeJSON[[]map[string]any](t, must(service.Search("search", "hello", 10)))
	if len(searchResults) != 1 || searchResults[0]["id"] != noteID {
		t.Fatalf("unexpected search results: %v", searchResults)
	}

	if _, err := service.ListNotebooks("list-notebooks"); err != nil {
		t.Fatalf("ListNotebooks: %v", err)
	}
	if _, err := service.ListTags("list-tags"); err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	revisions := decodeJSON[[]map[string]any](t, must(service.ListRevisions("list-revisions", noteID)))
	if len(revisions) == 0 {
		t.Fatalf("expected at least one revision after SaveNote")
	}
	firstRevisionID, _ := revisions[0]["id"].(string)
	if err := service.RestoreRevision("restore-revision", noteID, firstRevisionID); err != nil {
		t.Fatalf("RestoreRevision: %v", err)
	}

	if err := service.AddAttachmentData("add-attachment", noteID, "note.txt", "text/plain", []byte("attachment bytes")); err != nil {
		t.Fatalf("AddAttachmentData: %v", err)
	}

	if err := service.DeleteNote("delete-note", noteID, true); err != nil {
		t.Fatalf("DeleteNote (soft delete): %v", err)
	}
	if err := service.DeleteNote("restore-note", noteID, false); err != nil {
		t.Fatalf("DeleteNote (restore): %v", err)
	}

	backupDestination := filepath.Join(t.TempDir(), "backups")
	backupJSON, err := service.CreateBackup("create-backup", backupDestination)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	backupInfo := decodeJSON[map[string]any](t, backupJSON)
	backupID, _ := backupInfo["id"].(string)
	if backupID == "" {
		t.Fatalf("unexpected CreateBackup response: %v", backupInfo)
	}

	backups := decodeJSON[[]map[string]any](t, must(service.ListBackups("list-backups")))
	if len(backups) == 0 {
		t.Fatal("expected at least one backup after CreateBackup")
	}

	if _, err := service.PreviewBackup("preview-backup", backupID); err != nil {
		t.Fatalf("PreviewBackup: %v", err)
	}

	created2, err := service.EnsureDailyBackup("daily-backup", backupDestination)
	if err != nil {
		t.Fatalf("EnsureDailyBackup: %v", err)
	}
	_ = created2 // may be false if a daily backup already exists for today; both are valid.

	events := decodeJSON[[]map[string]any](t, must(service.PollEvents(0, 128)))
	if len(events) == 0 {
		t.Fatal("expected at least one emitted event (account_unlocked, notes_changed, ...)")
	}

	service.Cancel("no-such-request") // must not panic or error for an unknown ID

	if err := service.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	statusAfterLock := decodeJSON[map[string]any](t, must(service.Status()))
	if statusAfterLock["unlocked"] != false {
		t.Fatalf("expected locked status after Lock, got %v", statusAfterLock)
	}
	if _, err := service.ListNotes("after-lock"); err == nil {
		t.Fatal("expected ListNotes to fail once locked")
	}

	if _, err := service.UnlockAccount("unlock", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("UnlockAccount: %v", err)
	}
	relisted := decodeJSON[[]map[string]any](t, must(service.ListNotes("list-after-unlock")))
	if len(relisted) != 2 {
		t.Fatalf("expected notes to persist across lock/unlock, got %d", len(relisted))
	}
}

func TestServiceRejectsInvalidOrLockedCalls(t *testing.T) {
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)

	if _, err := service.ListNotes("locked-call"); err == nil {
		t.Fatal("expected an error before any account is unlocked")
	}
	if _, err := service.CreateAccount("", filepath.Join(t.TempDir(), "beresta.db"), "correct horse battery staple"); err == nil {
		t.Fatal("expected an empty request ID to be rejected")
	}
	if _, err := service.PollEvents(-1, 10); err == nil {
		t.Fatal("expected a negative event cursor to be rejected")
	}
	if _, err := service.PollEvents(0, 0); err == nil {
		t.Fatal("expected a zero event limit to be rejected")
	}
}

func TestServiceCreateAccountFailsClosedOnWrongDeviceSecretLength(t *testing.T) {
	if _, err := NewService(make([]byte, 16)); err == nil {
		t.Fatal("expected a non-32-byte device secret to be rejected")
	}
}

