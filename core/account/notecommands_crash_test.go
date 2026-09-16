package account

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

// berestaCommitWorkerDBPath, when set, tells
// TestE2EForcedTerminationDuringCommitWorker to run as the forced-
// termination test's worker subprocess instead of acting as a normal
// (skipped) test. Mirrors e2e_test.go's berestaE2EWorkerDBPath convention.
const berestaCommitWorkerDBPath = "BERESTA_COMMIT_WORKER_DB_PATH"

// berestaCommitWorkerTitle names the one note the worker repeatedly edits,
// so the driver can find it again after reopening the database.
const berestaCommitWorkerTitle = "Crash target"

// TestE2EForcedTerminationDuringCommitWorker is not a real test: it is the
// subprocess entry point
// TestForcedTerminationAfterCommitNoteBodyPreservesEveryAcknowledgedEdit
// launches and kills partway through. Run directly by `go test`, without
// the trigger environment variable set, it does nothing.
func TestE2EForcedTerminationDuringCommitWorker(t *testing.T) {
	dbPath := os.Getenv(berestaCommitWorkerDBPath)
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
	workspaceID := defaultWorkspaceID(t, acc)
	note, err := acc.CreateNote(ctx, workspaceID, model.Nil, berestaCommitWorkerTitle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker CreateNote:", err)
		os.Exit(1)
	}
	// Signal readiness only once the note the driver will look for actually
	// exists, so the driver's kill timing does not have to guess how long
	// account creation took on this machine: it waits for this file, then
	// kills partway through the commit loop below.
	if err := os.WriteFile(dbPath+".worker-ready", []byte("ready"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "worker ready signal:", err)
		os.Exit(1)
	}

	for i := 0; i < 200; i++ {
		doc, err := loadNoteDocument(ctx, acc.db, acc, workspaceID, note.ID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "worker loadNoteDocument:", err)
			os.Exit(1)
		}
		current, err := doc.Text(noteBodyRoot)
		if err != nil {
			doc.Close()
			fmt.Fprintln(os.Stderr, "worker doc.Text:", err)
			os.Exit(1)
		}
		if err := doc.Insert(noteBodyRoot, utf16Len(current), fmt.Sprintf("line %d ", i), nil); err != nil {
			doc.Close()
			fmt.Fprintln(os.Stderr, "worker doc.Insert:", err)
			os.Exit(1)
		}
		update, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
		doc.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, "worker EncodeStateAsUpdate:", err)
			os.Exit(1)
		}
		if err := acc.CommitNoteBody(ctx, NoteBodyCommand{
			WorkspaceID: workspaceID, NoteID: note.ID, Update: update, UpdateFormat: noteSnapshotFormat,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "worker CommitNoteBody:", err)
			os.Exit(1)
		}
		// Record that this commit was acknowledged (returned successfully -
		// the exact moment a real UI would display "Saved") only after
		// CommitNoteBody itself returned, so the driver can later tell
		// exactly how many edits it may require to have survived versus the
		// one that may have been interrupted mid-flight.
		if err := os.WriteFile(dbPath+".worker-progress", []byte(strconv.Itoa(i+1)), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "worker progress signal:", err)
			os.Exit(1)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestForcedTerminationAfterCommitNoteBodyPreservesEveryAcknowledgedEdit
// proves both crash-safety claims task 2.4/2.5's design require of the
// generation-tagged local commit model under an actual process kill, not
// just in-process fault injection:
//   - every edit whose CommitNoteBody call already returned - the moment a
//     real UI would have displayed "Saved" - is still present after the
//     database reopens, however abruptly the process died a moment later;
//   - an edit interrupted mid-commit (never acknowledged) leaves the
//     database in a valid, readable, still-writable state rather than a
//     torn or corrupt one.
//
// It launches the worker subprocess above, kills it forcibly partway
// through a run of body commits to the same note, then reopens the same
// database and confirms every commit the worker's progress file recorded
// as acknowledged survived intact, that the recovered document still
// decodes cleanly (a torn snapshot write would fail here instead), and
// that the reopened account accepts further commits.
func TestForcedTerminationAfterCommitNoteBodyPreservesEveryAcknowledgedEdit(t *testing.T) {
	if os.Getenv(berestaCommitWorkerDBPath) != "" {
		t.Skip("this is the worker subprocess entry point, not a standalone test")
	}

	dbPath := tempDBPath(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestE2EForcedTerminationDuringCommitWorker$", "-test.v")
	cmd.Env = append(os.Environ(), berestaCommitWorkerDBPath+"="+dbPath)
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
			_ = cmd.Process.Kill()
			t.Fatal("worker subprocess never signaled readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Give the loop a little time to acknowledge several commits, now that
	// the note is known to exist.
	time.Sleep(150 * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill worker subprocess: %v", err)
	}
	_ = cmd.Wait() // expected to report a kill-related error; not asserted

	progressBytes, err := os.ReadFile(dbPath + ".worker-progress")
	if err != nil {
		t.Fatalf("read worker progress sentinel: %v", err)
	}
	acknowledgedCount, err := strconv.Atoi(strings.TrimSpace(string(progressBytes)))
	if err != nil || acknowledgedCount == 0 {
		t.Fatalf("worker subprocess never acknowledged a single commit before being killed (progress = %q)", progressBytes)
	}

	ctx := context.Background()
	reopened, err := Unlock(ctx, UnlockOptions{
		DatabasePath: dbPath,
		Passphrase:   []byte("correct horse battery staple"),
		Wrapper:      newFakeWrapper(),
	})
	if err != nil {
		t.Fatalf("Unlock after forced termination: %v", err)
	}
	defer func() { _ = reopened.Lock() }()

	workspaceID := defaultWorkspaceID(t, reopened)
	notes, err := reopened.ListNotes(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListNotes after forced termination: %v", err)
	}
	var noteID model.ID
	for _, n := range notes {
		if n.Title.Value == berestaCommitWorkerTitle {
			noteID = n.ID
			break
		}
	}
	if noteID.IsZero() {
		t.Fatalf("recovered account has no note titled %q", berestaCommitWorkerTitle)
	}

	// A torn/corrupt snapshot from an interrupted commit would fail to
	// restore here instead of decoding into readable text - this is the
	// "recovers to a valid boundary" half of the claim.
	doc, err := loadNoteDocument(ctx, reopened.db, reopened, workspaceID, noteID)
	if err != nil {
		t.Fatalf("loadNoteDocument after forced termination: %v", err)
	}
	body, err := doc.Text(noteBodyRoot)
	doc.Close()
	if err != nil {
		t.Fatalf("recovered note body did not decode cleanly: %v", err)
	}

	// Every commit CommitNoteBody had already acknowledged before the kill
	// must be present - the "content remains after every displayed saved
	// acknowledgement" half of the claim. The in-flight commit at index
	// acknowledgedCount (if any) is deliberately not asserted either way:
	// whether it landed depends on exactly when the kill interrupted it,
	// and both outcomes are valid as long as nothing above it is missing
	// and the document still decodes cleanly, which the check above
	// already covers.
	for i := 0; i < acknowledgedCount; i++ {
		want := fmt.Sprintf("line %d ", i)
		if !strings.Contains(body, want) {
			t.Fatalf("recovered body is missing acknowledged edit %q (worker acknowledged %d commits): %q", want, acknowledgedCount, body)
		}
	}
	t.Logf("recovered all %d acknowledged commits after forced termination", acknowledgedCount)

	commitInsert(t, reopened, workspaceID, noteID, "after recovery")
}
