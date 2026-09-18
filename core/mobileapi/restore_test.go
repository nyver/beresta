package mobileapi

import (
	"path/filepath"
	"testing"
)

// TestPlanAndRestoreSelectiveImportsChosenNotesAsNew covers task 5.5's
// Android restore planner: PlanRestore must classify each backed-up note
// (addition/update/unchanged) without mutating current data, and
// RestoreSelective must then import only the chosen notes as new local
// notes with fresh IDs - mirroring desktop's identical PlanRestore/
// RestoreSelective flow (specs/backup-and-recovery.md, "Selective
// dry-run").
func TestPlanAndRestoreSelectiveImportsChosenNotesAsNew(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "beresta.db")
	service, err := NewService(newTestServiceDeviceSecret(t))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(service.Close)

	if _, err := service.CreateAccount("create", dbPath, "correct horse battery staple"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	noteAJSON, err := service.CreateNote("create-a", "", "Note A")
	if err != nil {
		t.Fatalf("CreateNote A: %v", err)
	}
	noteA := decodeJSON[map[string]any](t, noteAJSON)
	if err := service.SaveNote("save-a", noteA["id"].(string), "Note A", "content a"); err != nil {
		t.Fatalf("SaveNote A: %v", err)
	}

	noteBJSON, err := service.CreateNote("create-b", "", "Note B")
	if err != nil {
		t.Fatalf("CreateNote B: %v", err)
	}
	noteB := decodeJSON[map[string]any](t, noteBJSON)
	if err := service.SaveNote("save-b", noteB["id"].(string), "Note B", "content b"); err != nil {
		t.Fatalf("SaveNote B: %v", err)
	}

	backupDestination := filepath.Join(t.TempDir(), "backups")
	backupJSON, err := service.CreateBackup("create-backup", backupDestination)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	backup := decodeJSON[map[string]any](t, backupJSON)
	backupID, _ := backup["id"].(string)
	if backupID == "" {
		t.Fatalf("unexpected CreateBackup response: %v", backup)
	}

	// Change Note B locally after the backup, so the backup's copy of it
	// now differs from the live one - PlanRestore must classify it as an
	// update while Note A, untouched since the backup, stays unchanged.
	if err := service.DeleteNote("delete-b", noteB["id"].(string), true); err != nil {
		t.Fatalf("DeleteNote B: %v", err)
	}

	planJSON, err := service.PlanRestore("plan-restore", backupID, "")
	if err != nil {
		t.Fatalf("PlanRestore: %v", err)
	}
	plan := decodeJSON[map[string]any](t, planJSON)
	entries := plan["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("expected 2 plan entries, got %d: %v", len(entries), entries)
	}
	kinds := map[string]string{}
	for _, raw := range entries {
		entry := raw.(map[string]any)
		kinds[entry["title"].(string)] = entry["kind"].(string)
	}
	if kinds["Note A"] != "unchanged" {
		t.Fatalf("Note A classified %q, want unchanged", kinds["Note A"])
	}
	if kinds["Note B"] != "update" {
		t.Fatalf("Note B classified %q, want update", kinds["Note B"])
	}

	// PlanRestore must not have mutated anything: Note B must still be
	// listed as deleted, not silently restored by the dry run.
	notesAfterPlan := decodeJSON[[]map[string]any](t, must(service.ListNotes("list-after-plan")))
	for _, n := range notesAfterPlan {
		if n["id"] == noteB["id"] && n["deleted"] != true {
			t.Fatalf("PlanRestore must not mutate current data, but Note B is no longer deleted: %v", n)
		}
	}

	restoreJSON, err := service.RestoreSelective("restore-selective", backupID, `["`+noteB["id"].(string)+`"]`, backupDestination)
	if err != nil {
		t.Fatalf("RestoreSelective: %v", err)
	}
	result := decodeJSON[map[string]any](t, restoreJSON)
	newNoteIDs, _ := result["new_note_ids"].([]any)
	if len(newNoteIDs) != 1 {
		t.Fatalf("expected exactly 1 new note ID, got %v", result)
	}
	if newNoteIDs[0].(string) == noteB["id"].(string) {
		t.Fatal("RestoreSelective must assign a fresh note ID, not reuse the original")
	}
	safetyBackup, _ := result["safety_backup"].(map[string]any)
	if safetyBackup["id"] == "" || safetyBackup["id"] == nil {
		t.Fatalf("expected a mandatory pre-restore safety backup, got %v", result)
	}

	notesAfterRestore := decodeJSON[[]map[string]any](t, must(service.ListNotes("list-after-restore")))
	var restoredTitles []string
	for _, n := range notesAfterRestore {
		restoredTitles = append(restoredTitles, n["title"].(string))
	}
	found := false
	for _, title := range restoredTitles {
		if title == "Note B" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a restored Note B among current notes, got %v", restoredTitles)
	}
}
