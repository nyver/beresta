package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

func TestSearchFindsCreatedNotes(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Grocery list"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if _, err := created.CreateNote(ctx, workspaceID, model.Nil, "Unrelated"); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	results, err := created.Search(ctx, workspaceID, store.SearchQuery{Text: "Grocery"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Note.Title.Value != "Grocery list" {
		t.Fatalf("Search results = %+v", results)
	}

	if _, err := created.Search(ctx, workspaceID, store.SearchQuery{}); !errors.Is(err, store.ErrEmptySearchQuery) {
		t.Fatalf("Search(empty) error = %v, want ErrEmptySearchQuery", err)
	}
}

func TestSearchRejectsWhenLocked(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	if err := created.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := created.Search(ctx, workspaceID, store.SearchQuery{Text: "x"}); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Search after Lock error = %v, want ErrAccountLocked", err)
	}
}

func TestParseSearchQueryResolvesTagFilter(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	tag, err := created.CreateTag(ctx, workspaceID, "urgent")
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}

	q, err := created.ParseSearchQuery(ctx, workspaceID, "tag:urgent deadline")
	if err != nil {
		t.Fatalf("ParseSearchQuery: %v", err)
	}
	if len(q.TagIDs) != 1 || q.TagIDs[0] != tag.ID || q.Text != "deadline" {
		t.Fatalf("ParseSearchQuery = %+v", q)
	}

	if _, err := created.ParseSearchQuery(ctx, workspaceID, "tag:missing"); !errors.Is(err, store.ErrUnknownSearchTag) {
		t.Fatalf("ParseSearchQuery(unknown tag) error = %v, want ErrUnknownSearchTag", err)
	}
}

func TestSavedSearchLifecycle(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	now := time.Now()

	saved, err := created.CreateSavedSearch(ctx, workspaceID, "Urgent", "tag:urgent", now)
	if err != nil {
		t.Fatalf("CreateSavedSearch: %v", err)
	}

	list, err := created.ListSavedSearches(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListSavedSearches: %v", err)
	}
	if len(list) != 1 || list[0].ID != saved.ID {
		t.Fatalf("ListSavedSearches = %+v", list)
	}

	if err := created.UpdateSavedSearch(ctx, saved.ID, "Urgent items", "tag:urgent", now); err != nil {
		t.Fatalf("UpdateSavedSearch: %v", err)
	}
	list, err = created.ListSavedSearches(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListSavedSearches: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Urgent items" {
		t.Fatalf("ListSavedSearches after update = %+v", list)
	}

	if err := created.DeleteSavedSearch(ctx, saved.ID); err != nil {
		t.Fatalf("DeleteSavedSearch: %v", err)
	}
	list, err = created.ListSavedSearches(ctx, workspaceID)
	if err != nil {
		t.Fatalf("ListSavedSearches: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListSavedSearches after delete = %+v", list)
	}
}

func TestListBackupsReturnsCreatedBackup(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	now := time.Now()

	backup, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, now)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	list, err := created.ListBackups(ctx, store.BackupKindManual)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(list) != 1 || list[0].ID != backup.ID {
		t.Fatalf("ListBackups = %+v", list)
	}

	other, err := created.ListBackups(ctx, store.BackupKindDaily)
	if err != nil {
		t.Fatalf("ListBackups(daily): %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("ListBackups(daily) = %+v, want none", other)
	}
}

func TestWorkspacesReturnsInitialWorkspace(t *testing.T) {
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	ids, err := created.Workspaces()
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}
	if len(ids) != 1 || ids[0] != workspaceID {
		t.Fatalf("Workspaces = %+v, want [%s]", ids, workspaceID)
	}

	if err := created.Lock(); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := created.Workspaces(); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("Workspaces after Lock error = %v, want ErrAccountLocked", err)
	}
}
