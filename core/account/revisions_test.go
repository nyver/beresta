package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

func commitInsert(t *testing.T, a *Account, workspaceID, noteID model.ID, text string) {
	t.Helper()
	doc, err := loadNoteDocument(context.Background(), a.db, a.workspaceKeys[workspaceID], workspaceID, noteID)
	if err != nil {
		t.Fatalf("loadNoteDocument: %v", err)
	}
	current, err := doc.Text(noteBodyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Insert(noteBodyRoot, utf16Len(current), text, nil); err != nil {
		doc.Close()
		t.Fatal(err)
	}
	update, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
	doc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CommitNoteBody(context.Background(), NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: noteID, Update: update, UpdateFormat: noteSnapshotFormat,
	}); err != nil {
		t.Fatalf("CommitNoteBody: %v", err)
	}
}

func TestRevisionHistoryChecksPointsPeriodically(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Journal")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < noteRevisionCheckpointInterval; i++ {
		commitInsert(t, created, workspaceID, note.ID, "x")
	}

	revisions, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != noteRevisionCheckpointInterval+1 {
		t.Fatalf("revision count = %d, want %d (deltas + one checkpoint)", len(revisions), noteRevisionCheckpointInterval+1)
	}
	if revisions[len(revisions)-1].Kind != store.RevisionKindCheckpoint {
		t.Fatalf("last revision kind = %d, want checkpoint", revisions[len(revisions)-1].Kind)
	}
	for _, r := range revisions[:len(revisions)-1] {
		if r.Kind != store.RevisionKindDelta {
			t.Fatalf("expected only the last revision to be a checkpoint, got kind=%d earlier", r.Kind)
		}
	}
}

func TestRevisionMarkdownReconstructsHistoricalContent(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Draft")
	if err != nil {
		t.Fatal(err)
	}

	commitInsert(t, created, workspaceID, note.ID, "hello")
	commitInsert(t, created, workspaceID, note.ID, " world")

	revisions, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("ListRevisions = %v, err = %v", revisions, err)
	}

	firstText, err := created.RevisionMarkdown(ctx, workspaceID, note.ID, revisions[0].ID)
	if err != nil {
		t.Fatalf("RevisionMarkdown (first): %v", err)
	}
	if firstText != "hello" {
		t.Fatalf("first revision text = %q, want %q", firstText, "hello")
	}

	secondText, err := created.RevisionMarkdown(ctx, workspaceID, note.ID, revisions[1].ID)
	if err != nil {
		t.Fatalf("RevisionMarkdown (second): %v", err)
	}
	if secondText != "hello world" {
		t.Fatalf("second revision text = %q, want %q", secondText, "hello world")
	}
}

func TestRevisionMarkdownReconstructsAcrossCheckpoint(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Log")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < noteRevisionCheckpointInterval; i++ {
		commitInsert(t, created, workspaceID, note.ID, "a")
	}
	// One more edit after the checkpoint, so reconstruction must start from
	// the checkpoint and replay just this one delta on top of it.
	commitInsert(t, created, workspaceID, note.ID, "b")

	revisions, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := revisions[len(revisions)-1]
	if last.Kind != store.RevisionKindDelta {
		t.Fatalf("last revision kind = %d, want delta", last.Kind)
	}

	text, err := created.RevisionMarkdown(ctx, workspaceID, note.ID, last.ID)
	if err != nil {
		t.Fatalf("RevisionMarkdown: %v", err)
	}
	want := ""
	for i := 0; i < noteRevisionCheckpointInterval; i++ {
		want += "a"
	}
	want += "b"
	if text != want {
		t.Fatalf("reconstructed text = %q, want %q", text, want)
	}
}

func TestRestoreRevisionCreatesNewRevisionWithoutErasingHistory(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}

	commitInsert(t, created, workspaceID, note.ID, "first draft")
	revisions, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("ListRevisions = %v, err = %v", revisions, err)
	}
	firstRevisionID := revisions[0].ID

	commitInsert(t, created, workspaceID, note.ID, " and more")

	if err := created.RestoreRevision(ctx, workspaceID, note.ID, firstRevisionID); err != nil {
		t.Fatalf("RestoreRevision: %v", err)
	}

	doc, err := loadNoteDocument(ctx, created.db, created.workspaceKeys[workspaceID], workspaceID, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	currentText, err := doc.Text(noteBodyRoot)
	doc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if currentText != "first draft" {
		t.Fatalf("current text after restore = %q, want %q", currentText, "first draft")
	}

	after, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 3 {
		t.Fatalf("revision count after restore = %d, want 3 (original history preserved plus one new revision)", len(after))
	}
	// The original first revision must still reconstruct to its own content,
	// proving restore did not rewrite or erase history.
	original, err := created.RevisionMarkdown(ctx, workspaceID, note.ID, firstRevisionID)
	if err != nil || original != "first draft" {
		t.Fatalf("original first revision = %q, err = %v", original, err)
	}
}

func TestRestoreRevisionIsNoOpWhenContentAlreadyMatches(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	commitInsert(t, created, workspaceID, note.ID, "only version")
	revisions, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(revisions) != 1 {
		t.Fatal(err)
	}

	if err := created.RestoreRevision(ctx, workspaceID, note.ID, revisions[0].ID); err != nil {
		t.Fatalf("RestoreRevision: %v", err)
	}

	after, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(after) != 1 {
		t.Fatalf("restoring identical content should not add a revision: got %d", len(after))
	}
}

func TestDiffRevisionsReportsLineChanges(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	commitInsert(t, created, workspaceID, note.ID, "one")
	revisions, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(revisions) != 1 {
		t.Fatal(err)
	}

	diff, err := created.DiffRevisions(ctx, workspaceID, note.ID, model.Nil, revisions[0].ID)
	if err != nil {
		t.Fatalf("DiffRevisions: %v", err)
	}
	if len(diff) != 1 || diff[0].Op != DiffInsert || diff[0].Text != "one" {
		t.Fatalf("diff from empty = %+v", diff)
	}
}

func TestDiffLinesLCSBehavior(t *testing.T) {
	diff := diffLines("a\nb\nc", "a\nx\nc")
	want := []DiffLine{
		{Op: DiffEqual, Text: "a"},
		{Op: DiffDelete, Text: "b"},
		{Op: DiffInsert, Text: "x"},
		{Op: DiffEqual, Text: "c"},
	}
	if len(diff) != len(want) {
		t.Fatalf("diff = %+v, want %+v", diff, want)
	}
	for i := range want {
		if diff[i] != want[i] {
			t.Fatalf("diff[%d] = %+v, want %+v", i, diff[i], want[i])
		}
	}
}

func TestPruneRevisionHistoryKeepsSevenDaysAndCheckpointBase(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < noteRevisionCheckpointInterval; i++ {
		commitInsert(t, created, workspaceID, note.ID, "a")
	}
	before, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(before) != noteRevisionCheckpointInterval+1 {
		t.Fatalf("before = %v, err = %v", before, err)
	}

	// Everything so far is "old"; pruning far enough in the future must keep
	// only the checkpoint (the youngest revision at or before the cutoff)
	// and drop every delta before it, since they are no longer needed to
	// reconstruct anything at or after the checkpoint.
	future := time.UnixMilli(int64(before[len(before)-1].CreatedUnixMS)).Add(8 * 24 * time.Hour)
	affected, err := created.PruneRevisionHistory(ctx, future)
	if err != nil {
		t.Fatalf("PruneRevisionHistory: %v", err)
	}
	if affected != noteRevisionCheckpointInterval {
		t.Fatalf("pruned %d revisions, want %d (all deltas before the checkpoint)", affected, noteRevisionCheckpointInterval)
	}

	after, err := created.ListRevisions(ctx, workspaceID, note.ID)
	if err != nil || len(after) != 1 || after[0].Kind != store.RevisionKindCheckpoint {
		t.Fatalf("after = %v, err = %v", after, err)
	}
}

func TestPruneRevisionHistoryKeepsEverythingWithoutACheckpoint(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	commitInsert(t, created, workspaceID, note.ID, "only one edit, no checkpoint yet")

	affected, err := created.PruneRevisionHistory(ctx, time.Now().Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("PruneRevisionHistory: %v", err)
	}
	if affected != 0 {
		t.Fatalf("pruned %d revisions, want 0 (no checkpoint to serve as a safe base)", affected)
	}
}

func TestRevisionOperationsRejectWrongWorkspace(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	other, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.ListRevisions(ctx, other, note.ID); !errors.Is(err, ErrUnknownWorkspace) {
		t.Fatalf("ListRevisions error = %v, want ErrUnknownWorkspace", err)
	}
}
