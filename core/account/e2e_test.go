package account

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// TestEndToEndLocalOnlyLifecycle drives the complete no-server client
// surface built by phase 3 (task 4.10's required headless end-to-end
// suite) as one continuous scenario: account creation, notebook/tag
// organization, note editing, attachments, search, revision history,
// backup, destruction and whole restore, selective restore, and portable
// export/import into a second account. Every step uses only the Account
// API, so this doubles as the headless client harness a UI-less driver
// (or a future platform's own headless smoke test) would use.
func TestEndToEndLocalOnlyLifecycle(t *testing.T) {
	ctx := context.Background()

	dbPath := tempDBPath(t)
	wrapper := newFakeWrapper()
	acc, err := Create(ctx, CreateOptions{
		DatabasePath: dbPath,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      wrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	workspaceID := defaultWorkspaceID(t, acc)

	notebook, err := acc.CreateNotebook(ctx, workspaceID, model.Nil, "Projects")
	if err != nil {
		t.Fatalf("CreateNotebook: %v", err)
	}
	tag, err := acc.CreateTag(ctx, workspaceID, "important")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	note, err := acc.CreateNote(ctx, workspaceID, notebook.ID, "Launch checklist")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if err := acc.SetNoteTag(ctx, workspaceID, note.ID, tag.ID, true); err != nil {
		t.Fatalf("SetNoteTag: %v", err)
	}
	commitInsert(t, acc, workspaceID, note.ID, "ship the release notes")
	if _, err := acc.AddAttachment(ctx, workspaceID, note.ID, "plan.txt", "text/plain", bytes.NewReader([]byte("step by step plan"))); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	results, err := store.SearchNotes(ctx, acc.db, workspaceID, store.SearchQuery{Text: "release"})
	if err != nil || len(results) != 1 || results[0].Note.ID != note.ID {
		t.Fatalf("SearchNotes = %v, err = %v", results, err)
	}

	revisions, err := acc.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(revisions) == 0 {
		t.Fatalf("ListRevisions = %v, err = %v", revisions, err)
	}

	backupsRoot := filepath.Join(filepath.Dir(dbPath), "backups")
	backup, err := acc.CreateBackup(ctx, backupsRoot, store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	// Diverge after the backup, then destroy it by whole restore.
	strayNote, err := acc.CreateNote(ctx, workspaceID, model.Nil, "Should not survive restore")
	if err != nil {
		t.Fatalf("CreateNote (stray): %v", err)
	}
	if _, err := acc.RestoreWhole(ctx, backup.ID, backupsRoot, time.Now()); err != nil {
		t.Fatalf("RestoreWhole: %v", err)
	}
	if _, err := acc.GetNote(ctx, strayNote.ID); err == nil {
		t.Fatal("stray note should not exist after whole restore")
	}
	if _, err := acc.GetNote(ctx, note.ID); err != nil {
		t.Fatalf("original note missing after whole restore: %v", err)
	}

	// Selective restore still works against the same backup right after a
	// whole restore, proving the account (Root Key, workspace keys, blob
	// store) is left fully consistent by RestoreWhole, not just its
	// database file.
	selectiveResult, err := acc.RestoreSelective(ctx, backup.ID, []model.ID{note.ID}, backupsRoot, time.Now())
	if err != nil {
		t.Fatalf("RestoreSelective: %v", err)
	}
	if len(selectiveResult.NewNoteIDs) != 1 {
		t.Fatalf("RestoreSelective NewNoteIDs = %v", selectiveResult.NewNoteIDs)
	}

	exportDir := filepath.Join(t.TempDir(), "export")
	if _, err := acc.ExportNotes(ctx, workspaceID, exportDir, nil, time.Now()); err != nil {
		t.Fatalf("ExportNotes: %v", err)
	}
	if err := acc.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	otherDBPath := tempDBPath(t)
	otherWrapper := newFakeWrapper()
	other, err := Create(ctx, CreateOptions{
		DatabasePath: otherDBPath,
		Passphrase:   []byte("a different passphrase entirely"),
		Wrapper:      otherWrapper,
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		t.Fatalf("Create (import target): %v", err)
	}
	defer other.Lock()
	otherWorkspace := defaultWorkspaceID(t, other)
	importResult, err := other.ImportBerestaArchive(ctx, otherWorkspace, exportDir)
	if err != nil {
		t.Fatalf("ImportBerestaArchive: %v", err)
	}
	if len(importResult.NewNoteIDs) == 0 {
		t.Fatal("import produced no notes")
	}
}

// berestaE2EWorkerDBPath, when set, tells TestE2EForcedTerminationWorker to
// run as the forced-termination test's worker subprocess instead of acting
// as a normal (skipped) test.
const berestaE2EWorkerDBPath = "BERESTA_E2E_WORKER_DB_PATH"

// TestE2EForcedTerminationWorker is not a real test: it is the subprocess
// entry point TestForcedTerminationDuringNoteCreationRecoversCleanly
// launches and kills partway through. Run directly by `go test`, without
// the trigger environment variable set, it does nothing.
func TestE2EForcedTerminationWorker(t *testing.T) {
	dbPath := os.Getenv(berestaE2EWorkerDBPath)
	if dbPath == "" {
		t.Skip("not running as the forced-termination worker subprocess")
	}

	ctx := context.Background()
	acc, err := Create(ctx, CreateOptions{
		DatabasePath: dbPath,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      newFakeWrapper(),
		KDFOptions:   fastKDF(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker Create:", err)
		os.Exit(1)
	}
	// Signal readiness only once account creation (KDF calibration,
	// database file creation) has actually finished, so the parent's kill
	// timing does not have to guess how long that took on this machine: it
	// waits for this file, then kills partway through the loop below,
	// never before the account exists at all.
	if err := os.WriteFile(dbPath+".worker-ready", []byte("ready"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "worker ready signal:", err)
		os.Exit(1)
	}
	workspaceID := defaultWorkspaceID(t, acc)
	for i := 0; i < 200; i++ {
		if _, err := acc.CreateNote(ctx, workspaceID, model.Nil, fmt.Sprintf("Note %d", i)); err != nil {
			fmt.Fprintln(os.Stderr, "worker CreateNote:", err)
			os.Exit(1)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestForcedTerminationDuringNoteCreationRecoversCleanly proves the
// architecture's crash-safety claim ("SQLite transactions ... provide
// crash safety. No operation is partially visible after restart") holds
// under an actual process kill, not just an in-process fault injection: it
// launches the worker subprocess above, kills it forcibly partway through
// a run of note creations, then reopens the same database and confirms it
// is intact, contains only fully committed notes, and is still writable.
func TestForcedTerminationDuringNoteCreationRecoversCleanly(t *testing.T) {
	if os.Getenv(berestaE2EWorkerDBPath) != "" {
		t.Skip("this is the worker subprocess entry point, not a standalone test")
	}

	dbPath := tempDBPath(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestE2EForcedTerminationWorker$", "-test.v")
	cmd.Env = append(os.Environ(), berestaE2EWorkerDBPath+"="+dbPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker subprocess: %v", err)
	}

	readyPath := dbPath + ".worker-ready"
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatal("worker subprocess never signaled readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Give the loop a little time to get partway through, now that the
	// account is known to exist.
	time.Sleep(150 * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill worker subprocess: %v", err)
	}
	_ = cmd.Wait() // expected to report a kill-related error; not asserted

	ctx := context.Background()
	reopened, err := Unlock(ctx, UnlockOptions{
		DatabasePath: dbPath,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      newFakeWrapper(),
	})
	if err != nil {
		t.Fatalf("Unlock after forced termination: %v", err)
	}
	defer reopened.Lock()

	workspaceID := defaultWorkspaceID(t, reopened)
	notes, err := reopened.ListNotes(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListNotes after forced termination: %v", err)
	}
	for _, n := range notes {
		if n.Title.Value == "" {
			t.Fatalf("recovered a note with no title, suggesting a partial write survived: %+v", n)
		}
	}
	t.Logf("recovered %d notes after forced termination", len(notes))

	if _, err := reopened.CreateNote(ctx, workspaceID, model.Nil, "After recovery"); err != nil {
		t.Fatalf("CreateNote after forced termination: %v", err)
	}
}
