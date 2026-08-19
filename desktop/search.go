package main

import (
	"time"

	"github.com/beresta-app/beresta/core/store"
)

// SearchResultDTO is one matched note plus its relevance rank (lower is
// more relevant; see store.SearchResult).
type SearchResultDTO struct {
	Note NoteDTO `json:"note"`
	Rank float64 `json:"rank"`
}

// Search parses text using the search-box filter language (bare words,
// `tag:`, `after:`, `before:`, `deleted:true`; see
// store.ParseSearchQueryText) and runs it against the account's
// workspace.
func (a *App) Search(text string) ([]SearchResultDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	ctx := a.requestContext()
	q, err := acc.ParseSearchQuery(ctx, workspaceID, text)
	if err != nil {
		return nil, mapError(err)
	}
	results, err := acc.Search(ctx, workspaceID, q)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]SearchResultDTO, len(results))
	for i, r := range results {
		out[i] = SearchResultDTO{Note: noteDTO(r.Note), Rank: r.Rank}
	}
	return out, nil
}

// SavedSearchDTO is the JS-facing shape of a store.SavedSearch.
type SavedSearchDTO struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Query       string `json:"query"`
	CreatedMS   int64  `json:"created_unix_ms"`
	UpdatedMS   int64  `json:"updated_unix_ms"`
}

func savedSearchDTO(s store.SavedSearch) SavedSearchDTO {
	return SavedSearchDTO{
		ID:          idString(s.ID),
		WorkspaceID: idString(s.WorkspaceID),
		Name:        s.Name,
		Query:       s.Query,
		CreatedMS:   s.CreatedUnixMS,
		UpdatedMS:   s.UpdatedUnixMS,
	}
}

// ListSavedSearches returns every saved search in the account's
// workspace.
func (a *App) ListSavedSearches() ([]SavedSearchDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	searches, err := acc.ListSavedSearches(a.requestContext(), workspaceID)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]SavedSearchDTO, len(searches))
	for i, s := range searches {
		out[i] = savedSearchDTO(s)
	}
	return out, nil
}

// CreateSavedSearch saves a new named query, verbatim as typed, in the
// account's workspace.
func (a *App) CreateSavedSearch(name, query string) (SavedSearchDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return SavedSearchDTO{}, mapError(err)
	}
	saved, err := acc.CreateSavedSearch(a.requestContext(), workspaceID, name, query, time.Now())
	if err != nil {
		return SavedSearchDTO{}, mapError(err)
	}
	return savedSearchDTO(saved), nil
}

// UpdateSavedSearch overwrites an existing saved search's name and query.
func (a *App) UpdateSavedSearch(savedSearchID, name, query string) error {
	acc, err := a.currentAccount()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(savedSearchID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.UpdateSavedSearch(a.requestContext(), id, name, query, time.Now()))
}

// DeleteSavedSearch permanently removes a saved search.
func (a *App) DeleteSavedSearch(savedSearchID string) error {
	acc, err := a.currentAccount()
	if err != nil {
		return mapError(err)
	}
	id, err := parseID(savedSearchID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.DeleteSavedSearch(a.requestContext(), id))
}
