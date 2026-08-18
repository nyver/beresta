package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

// seedSearchNote creates a note, its tags, and its FTS row in one call so
// search tests can build small fixtures without repeating the plumbing
// task 3.7's note-command layer normally handles.
func seedSearchNote(t *testing.T, db *sql.DB, workspaceID model.ID, title, body string, tagIDs []model.ID, createdMS uint64) model.Note {
	t.Helper()
	ctx := context.Background()
	clock := repoClock(t, createdMS, 0, 0x03)
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, title, clock)
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if err := ReplaceNoteFTS(ctx, db, note.ID, title, body); err != nil {
		t.Fatalf("ReplaceNoteFTS() error = %v", err)
	}
	for _, tagID := range tagIDs {
		if err := SetNoteTag(ctx, db, note.ID, tagID, true, clock); err != nil {
			t.Fatalf("SetNoteTag() error = %v", err)
		}
	}
	return note
}

func TestSearchNotesMatchesTitleAndBody(t *testing.T) {
	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)

	needle := seedSearchNote(t, db, workspaceID, "Grocery list", "buy oat milk and rye bread", nil, 100)
	seedSearchNote(t, db, workspaceID, "Unrelated", "nothing to see here", nil, 100)

	results, err := SearchNotes(context.Background(), db, workspaceID, SearchQuery{Text: "oat milk"})
	if err != nil {
		t.Fatalf("SearchNotes() error = %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != needle.ID {
		t.Fatalf("SearchNotes() = %+v, want just %v", results, needle.ID)
	}
}

func TestSearchNotesRanksTitleAboveBody(t *testing.T) {
	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)

	titleMatch := seedSearchNote(t, db, workspaceID, "widget design notes", "nothing relevant here", nil, 100)
	bodyMatch := seedSearchNote(t, db, workspaceID, "unrelated note", "a passing mention of widget somewhere", nil, 100)

	results, err := SearchNotes(context.Background(), db, workspaceID, SearchQuery{Text: "widget"})
	if err != nil {
		t.Fatalf("SearchNotes() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("SearchNotes() = %d results, want 2", len(results))
	}
	if results[0].Note.ID != titleMatch.ID || results[1].Note.ID != bodyMatch.ID {
		t.Fatalf("SearchNotes() order = [%v, %v], want title match ranked first", results[0].Note.ID, results[1].Note.ID)
	}
}

func TestSearchNotesTagFilterRequiresAllTags(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	urgent, err := CreateTag(ctx, db, workspaceID, "urgent", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	home, err := CreateTag(ctx, db, workspaceID, "home", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	both := seedSearchNote(t, db, workspaceID, "Fix sink", "call the plumber", []model.ID{urgent.ID, home.ID}, 100)
	seedSearchNote(t, db, workspaceID, "Fix bug", "urgent production issue", []model.ID{urgent.ID}, 100)

	results, err := SearchNotes(ctx, db, workspaceID, SearchQuery{TagIDs: []model.ID{urgent.ID, home.ID}})
	if err != nil {
		t.Fatalf("SearchNotes() error = %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != both.ID {
		t.Fatalf("SearchNotes() = %+v, want just %v", results, both.ID)
	}
}

func TestSearchNotesDateFilter(t *testing.T) {
	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)

	seedSearchNote(t, db, workspaceID, "Old note", "irrelevant", nil, 100)
	recent := seedSearchNote(t, db, workspaceID, "New note", "irrelevant", nil, 900)

	results, err := SearchNotes(context.Background(), db, workspaceID, SearchQuery{CreatedFromMS: 500})
	if err != nil {
		t.Fatalf("SearchNotes() error = %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != recent.ID {
		t.Fatalf("SearchNotes() = %+v, want just %v", results, recent.ID)
	}
}

func TestSearchNotesExcludesDeletedByDefault(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)

	note := seedSearchNote(t, db, workspaceID, "Trashed note", "some body text", nil, 100)
	if err := SetNoteDeleted(ctx, db, note.ID, true, repoClock(t, 200, 0, 0x03)); err != nil {
		t.Fatal(err)
	}

	results, err := SearchNotes(ctx, db, workspaceID, SearchQuery{Text: "body"})
	if err != nil {
		t.Fatalf("SearchNotes() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("SearchNotes() = %+v, want no results for a deleted note", results)
	}

	results, err = SearchNotes(ctx, db, workspaceID, SearchQuery{Text: "body", IncludeDeleted: true})
	if err != nil {
		t.Fatalf("SearchNotes() error = %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != note.ID {
		t.Fatalf("SearchNotes(IncludeDeleted) = %+v, want just %v", results, note.ID)
	}
}

func TestSearchNotesEmptyQueryRejected(t *testing.T) {
	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)

	_, err := SearchNotes(context.Background(), db, workspaceID, SearchQuery{})
	if !errors.Is(err, ErrEmptySearchQuery) {
		t.Fatalf("SearchNotes() error = %v, want ErrEmptySearchQuery", err)
	}
}

func TestSearchNotesRejectsCanceledContext(t *testing.T) {
	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)
	seedSearchNote(t, db, workspaceID, "Any note", "any body", nil, 100)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := SearchNotes(ctx, db, workspaceID, SearchQuery{Text: "any"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SearchNotes() error = %v, want context.Canceled", err)
	}
}

func TestSearchNotesLiteralFTSSyntaxDoesNotError(t *testing.T) {
	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)
	seedSearchNote(t, db, workspaceID, `Quote "test" - and NEAR/OR tokens`, "body", nil, 100)

	results, err := SearchNotes(context.Background(), db, workspaceID, SearchQuery{Text: `"test" - NEAR OR`})
	if err != nil {
		t.Fatalf("SearchNotes() with FTS operator characters error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchNotes() = %d results, want 1", len(results))
	}
}

func TestParseSearchQueryText(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	urgent, err := CreateTag(ctx, db, workspaceID, "urgent", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	q, err := ParseSearchQueryText(ctx, db, workspaceID, "grocery list tag:urgent after:100 before:900 deleted:true")
	if err != nil {
		t.Fatalf("ParseSearchQueryText() error = %v", err)
	}
	want := SearchQuery{
		Text:           "grocery list",
		TagIDs:         []model.ID{urgent.ID},
		CreatedFromMS:  100,
		CreatedToMS:    900,
		IncludeDeleted: true,
	}
	if q.Text != want.Text || q.CreatedFromMS != want.CreatedFromMS || q.CreatedToMS != want.CreatedToMS || q.IncludeDeleted != want.IncludeDeleted {
		t.Fatalf("ParseSearchQueryText() = %+v, want %+v", q, want)
	}
	if len(q.TagIDs) != 1 || q.TagIDs[0] != urgent.ID {
		t.Fatalf("ParseSearchQueryText() TagIDs = %v, want [%v]", q.TagIDs, urgent.ID)
	}
}

func TestParseSearchQueryTextUnknownTag(t *testing.T) {
	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)

	_, err := ParseSearchQueryText(context.Background(), db, workspaceID, "tag:does-not-exist")
	if !errors.Is(err, ErrUnknownSearchTag) {
		t.Fatalf("ParseSearchQueryText() error = %v, want ErrUnknownSearchTag", err)
	}
}

func TestParseSearchQueryTextIgnoresDeletedTagName(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	tag, err := CreateTag(ctx, db, workspaceID, "stale", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetTagDeleted(ctx, db, tag.ID, true, repoClock(t, 2, 0, 0x02)); err != nil {
		t.Fatal(err)
	}

	_, err = ParseSearchQueryText(ctx, db, workspaceID, "tag:stale")
	if !errors.Is(err, ErrUnknownSearchTag) {
		t.Fatalf("ParseSearchQueryText() error = %v, want ErrUnknownSearchTag for a deleted tag name", err)
	}
}
