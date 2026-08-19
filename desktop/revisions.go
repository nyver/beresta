package main

import (
	"github.com/beresta-app/beresta/core/account"
	"github.com/beresta-app/beresta/core/store"
)

// RevisionDTO is the JS-facing shape of an account.RevisionSummary.
type RevisionDTO struct {
	ID         string `json:"id"`
	Checkpoint bool   `json:"checkpoint"`
	CreatedMS  int64  `json:"created_unix_ms"`
}

func revisionDTO(r account.RevisionSummary) RevisionDTO {
	return RevisionDTO{ID: idString(r.ID), Checkpoint: r.Kind == store.RevisionKindCheckpoint, CreatedMS: r.CreatedUnixMS}
}

// ListRevisions returns every retained revision for a note, oldest first.
func (a *App) ListRevisions(noteID string) ([]RevisionDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return nil, mapError(err)
	}
	revisions, err := acc.ListRevisions(a.requestContext(), workspaceID, note)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]RevisionDTO, len(revisions))
	for i, r := range revisions {
		out[i] = revisionDTO(r)
	}
	return out, nil
}

// RevisionMarkdown reconstructs and returns one revision's canonical
// Markdown content.
func (a *App) RevisionMarkdown(noteID, revisionID string) (string, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return "", mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return "", mapError(err)
	}
	revision, err := parseID(revisionID)
	if err != nil {
		return "", mapError(err)
	}
	markdown, err := acc.RevisionMarkdown(a.requestContext(), workspaceID, note, revision)
	if err != nil {
		return "", mapError(err)
	}
	return markdown, nil
}

// DiffLineDTO is one line of a line-based revision diff.
type DiffLineDTO struct {
	// Op is "equal", "delete", or "insert".
	Op   string `json:"op"`
	Text string `json:"text"`
}

var diffOpNames = [...]string{"equal", "delete", "insert"}

// DiffRevisions returns a line-based diff from fromRevisionID to
// toRevisionID's Markdown content. An empty fromRevisionID diffs against
// the note's state before its first revision.
func (a *App) DiffRevisions(noteID, fromRevisionID, toRevisionID string) ([]DiffLineDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return nil, mapError(err)
	}
	from, err := parseID(fromRevisionID)
	if err != nil {
		return nil, mapError(err)
	}
	to, err := parseID(toRevisionID)
	if err != nil {
		return nil, mapError(err)
	}
	lines, err := acc.DiffRevisions(a.requestContext(), workspaceID, note, from, to)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]DiffLineDTO, len(lines))
	for i, l := range lines {
		out[i] = DiffLineDTO{Op: diffOpNames[l.Op], Text: l.Text}
	}
	return out, nil
}

// RestoreRevision creates a new current revision whose plain text content
// matches a selected historical revision, without erasing intervening
// history.
func (a *App) RestoreRevision(noteID, revisionID string) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return mapError(err)
	}
	revision, err := parseID(revisionID)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.RestoreRevision(a.requestContext(), workspaceID, note, revision))
}
