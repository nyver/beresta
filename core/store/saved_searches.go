package store

import (
	"context"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

const maxSavedSearchNameBytes = 128

// SavedSearch is one named, reusable local search query. Saved searches are
// a local convenience and are not currently synchronized between devices.
type SavedSearch struct {
	ID            model.ID
	WorkspaceID   model.ID
	Name          string
	Query         string
	CreatedUnixMS int64
	UpdatedUnixMS int64
}

// CreateSavedSearch inserts a new saved search. Names are unique per
// workspace.
func CreateSavedSearch(ctx context.Context, exec Executor, workspaceID model.ID, name, query string, nowUnixMS int64) (SavedSearch, error) {
	if err := validateName(name, maxSavedSearchNameBytes); err != nil {
		return SavedSearch{}, err
	}
	if len(query) == 0 {
		return SavedSearch{}, fmt.Errorf("%w: query", ErrInvalidName)
	}
	id, err := model.NewID()
	if err != nil {
		return SavedSearch{}, err
	}
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO saved_searches (id, workspace_id, name, query, created_unix_ms, updated_unix_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		id.Bytes(), workspaceID.Bytes(), name, query, nowUnixMS, nowUnixMS,
	); err != nil {
		return SavedSearch{}, fmt.Errorf("store: insert saved search: %w", err)
	}
	return SavedSearch{ID: id, WorkspaceID: workspaceID, Name: name, Query: query, CreatedUnixMS: nowUnixMS, UpdatedUnixMS: nowUnixMS}, nil
}

// UpdateSavedSearch overwrites an existing saved search's name and query.
func UpdateSavedSearch(ctx context.Context, exec Executor, id model.ID, name, query string, nowUnixMS int64) error {
	if err := validateName(name, maxSavedSearchNameBytes); err != nil {
		return err
	}
	if len(query) == 0 {
		return fmt.Errorf("%w: query", ErrInvalidName)
	}
	result, err := exec.ExecContext(ctx,
		`UPDATE saved_searches SET name = ?, query = ?, updated_unix_ms = ? WHERE id = ?`,
		name, query, nowUnixMS, id.Bytes(),
	)
	if err != nil {
		return fmt.Errorf("store: update saved search: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update saved search: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSavedSearch permanently removes a saved search. Saved searches are
// local convenience state, not synchronized history, so this is a hard
// delete rather than a tombstone.
func DeleteSavedSearch(ctx context.Context, exec Executor, id model.ID) error {
	result, err := exec.ExecContext(ctx, `DELETE FROM saved_searches WHERE id = ?`, id.Bytes())
	if err != nil {
		return fmt.Errorf("store: delete saved search: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete saved search: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListSavedSearches returns every saved search in a workspace.
func ListSavedSearches(ctx context.Context, exec Executor, workspaceID model.ID) ([]SavedSearch, error) {
	rows, err := exec.QueryContext(ctx,
		`SELECT id, workspace_id, name, query, created_unix_ms, updated_unix_ms FROM saved_searches WHERE workspace_id = ?`,
		workspaceID.Bytes(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: list saved searches: %w", err)
	}
	defer rows.Close()

	var searches []SavedSearch
	for rows.Next() {
		var s SavedSearch
		var idBytes, workspaceIDBytes []byte
		if err := rows.Scan(&idBytes, &workspaceIDBytes, &s.Name, &s.Query, &s.CreatedUnixMS, &s.UpdatedUnixMS); err != nil {
			return nil, fmt.Errorf("store: scan saved search: %w", err)
		}
		id, err := model.ParseID(idBytes)
		if err != nil {
			return nil, fmt.Errorf("store: stored saved search ID: %w", err)
		}
		wsID, err := model.ParseID(workspaceIDBytes)
		if err != nil {
			return nil, fmt.Errorf("store: stored saved search workspace ID: %w", err)
		}
		s.ID, s.WorkspaceID = id, wsID
		searches = append(searches, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list saved searches: %w", err)
	}
	return searches, nil
}
