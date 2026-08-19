package account

import (
	"context"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// Search runs a full-text and/or filtered search over workspaceID's notes.
// It is a thin, lock-checked wrapper around store.SearchNotes so callers
// never need direct access to the account's database handle.
func (a *Account) Search(ctx context.Context, workspaceID model.ID, q store.SearchQuery) ([]store.SearchResult, error) {
	db, _, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return nil, err
	}
	return store.SearchNotes(ctx, db, workspaceID, q)
}

// ParseSearchQuery parses the small search-box filter language (see
// store.ParseSearchQueryText) against workspaceID's current tags.
func (a *Account) ParseSearchQuery(ctx context.Context, workspaceID model.ID, text string) (store.SearchQuery, error) {
	db, _, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return store.SearchQuery{}, err
	}
	return store.ParseSearchQueryText(ctx, db, workspaceID, text)
}

// ListSavedSearches returns every saved search in workspaceID.
func (a *Account) ListSavedSearches(ctx context.Context, workspaceID model.ID) ([]store.SavedSearch, error) {
	db, _, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return nil, err
	}
	return store.ListSavedSearches(ctx, db, workspaceID)
}

// CreateSavedSearch saves a new named query in workspaceID.
func (a *Account) CreateSavedSearch(ctx context.Context, workspaceID model.ID, name, query string, now time.Time) (store.SavedSearch, error) {
	db, _, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return store.SavedSearch{}, err
	}
	return store.CreateSavedSearch(ctx, db, workspaceID, name, query, now.UnixMilli())
}

// UpdateSavedSearch overwrites an existing saved search's name and query.
func (a *Account) UpdateSavedSearch(ctx context.Context, id model.ID, name, query string, now time.Time) error {
	db, _, err := a.accountSession()
	if err != nil {
		return err
	}
	return store.UpdateSavedSearch(ctx, db, id, name, query, now.UnixMilli())
}

// DeleteSavedSearch permanently removes a saved search.
func (a *Account) DeleteSavedSearch(ctx context.Context, id model.ID) error {
	db, _, err := a.accountSession()
	if err != nil {
		return err
	}
	return store.DeleteSavedSearch(ctx, db, id)
}

// ListBackups returns every catalog entry of one kind, newest first,
// including corrupt ones (see store.ListBackups).
func (a *Account) ListBackups(ctx context.Context, kind int) ([]store.Backup, error) {
	db, _, err := a.accountSession()
	if err != nil {
		return nil, err
	}
	return store.ListBackups(ctx, db, kind)
}
