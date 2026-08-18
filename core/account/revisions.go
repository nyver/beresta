package account

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync/yjsadapter"
)

// noteRevisionRetention is the notes-management spec's minimum note
// revision history window ("retain space-efficient note revisions for at
// least the preceding seven days").
const noteRevisionRetention = 7 * 24 * time.Hour

// RevisionSummary is one entry in a note's revision history list, without
// the potentially large decrypted content ListRevisions callers usually
// don't need up front (see RevisionMarkdown).
type RevisionSummary struct {
	ID            model.ID
	Kind          int
	CreatedUnixMS int64
}

// ListRevisions returns every retained revision for a note, oldest first.
// Pruning (PruneRevisionHistory) is the only thing that ever removes an
// entry; every revision returned here is fully reconstructible.
func (a *Account) ListRevisions(ctx context.Context, workspaceID, noteID model.ID) ([]RevisionSummary, error) {
	db, _, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return nil, err
	}
	if err := verifyNoteInWorkspace(ctx, db, workspaceID, noteID); err != nil {
		return nil, err
	}
	revisions, err := store.ListRevisions(ctx, db, noteID)
	if err != nil {
		return nil, err
	}
	summaries := make([]RevisionSummary, len(revisions))
	for i, r := range revisions {
		summaries[i] = RevisionSummary{ID: r.ID, Kind: r.Kind, CreatedUnixMS: r.CreatedUnixMS}
	}
	return summaries, nil
}

// RevisionMarkdown reconstructs and returns one revision's canonical
// Markdown content.
func (a *Account) RevisionMarkdown(ctx context.Context, workspaceID, noteID, revisionID model.ID) (string, error) {
	doc, err := a.reconstructNoteDocument(ctx, workspaceID, noteID, revisionID)
	if err != nil {
		return "", err
	}
	defer doc.Close()
	return doc.Markdown(noteBodyRoot)
}

// DiffOp identifies one line's role in a DiffRevisions result.
type DiffOp uint8

const (
	// DiffEqual marks a line present, unchanged, in both revisions.
	DiffEqual DiffOp = iota
	// DiffDelete marks a line present only in the "from" revision.
	DiffDelete
	// DiffInsert marks a line present only in the "to" revision.
	DiffInsert
)

// DiffLine is one line of a line-based revision diff.
type DiffLine struct {
	Op   DiffOp
	Text string
}

// DiffRevisions returns a line-based diff of two revisions' canonical
// Markdown content, from fromRevisionID to toRevisionID. A zero
// fromRevisionID diffs against empty content (the note before its first
// revision).
func (a *Account) DiffRevisions(ctx context.Context, workspaceID, noteID, fromRevisionID, toRevisionID model.ID) ([]DiffLine, error) {
	var fromText string
	if !fromRevisionID.IsZero() {
		text, err := a.RevisionMarkdown(ctx, workspaceID, noteID, fromRevisionID)
		if err != nil {
			return nil, err
		}
		fromText = text
	}
	toText, err := a.RevisionMarkdown(ctx, workspaceID, noteID, toRevisionID)
	if err != nil {
		return nil, err
	}
	return diffLines(fromText, toText), nil
}

// RestoreRevision creates a new current revision whose body content matches
// a selected historical revision's plain text, without erasing any
// intervening history (the notes-management spec's "restore a selected
// revision without erasing the intervening history"). Formatting is not
// preserved across rollback: Yjs has no built-in point-in-time revert, so
// this reconstructs the historical revision's plain text and replaces the
// note's current plain text with it as one new edit, recorded through the
// normal CommitNoteBody path (see ASSUMPTIONS.md).
func (a *Account) RestoreRevision(ctx context.Context, workspaceID, noteID, revisionID model.ID) error {
	oldDoc, err := a.reconstructNoteDocument(ctx, workspaceID, noteID, revisionID)
	if err != nil {
		return err
	}
	oldText, err := oldDoc.Text(noteBodyRoot)
	oldDoc.Close()
	if err != nil {
		return err
	}

	db, entry, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return err
	}
	workingDoc, err := loadNoteDocument(ctx, db, entry, workspaceID, noteID)
	if err != nil {
		return err
	}
	defer workingDoc.Close()

	currentText, err := workingDoc.Text(noteBodyRoot)
	if err != nil {
		return err
	}
	if currentText == oldText {
		return nil
	}
	// Y.Text indexes in UTF-16 code units (matching JS Yjs), not Go runes or
	// bytes.
	if length := utf16Len(currentText); length > 0 {
		if err := workingDoc.Delete(noteBodyRoot, 0, length); err != nil {
			return err
		}
	}
	if oldText != "" {
		if err := workingDoc.Insert(noteBodyRoot, 0, oldText, nil); err != nil {
			return err
		}
	}
	update, err := workingDoc.EncodeStateAsUpdate(noteSnapshotFormat)
	if err != nil {
		return err
	}

	return a.CommitNoteBody(ctx, NoteBodyCommand{
		WorkspaceID: workspaceID, NoteID: noteID, Update: update, UpdateFormat: noteSnapshotFormat,
	})
}

// PruneRevisionHistory deletes revisions older than the notes-management
// spec's seven-day retention floor, across every note in the account's
// database, keeping the newest checkpoint at or before the cutoff (and
// everything after it) for any note that has one. It is safe to call
// repeatedly, for example once per client startup.
func (a *Account) PruneRevisionHistory(ctx context.Context, now time.Time) (int64, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return 0, ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()

	cutoff := now.Add(-noteRevisionRetention).UnixMilli()
	return store.PruneRevisions(ctx, db, cutoff)
}

// reconstructNoteDocument replays a note's revision history up to and
// including throughRevisionID and returns the resulting document, starting
// from the youngest checkpoint at or before it (or from empty, if none
// exists) rather than always replaying from the note's first revision. The
// caller must Close the returned document.
func (a *Account) reconstructNoteDocument(ctx context.Context, workspaceID, noteID, throughRevisionID model.ID) (*yjsadapter.Document, error) {
	db, entry, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return nil, err
	}
	if err := verifyNoteInWorkspace(ctx, db, workspaceID, noteID); err != nil {
		return nil, err
	}

	revisions, err := store.ListRevisions(ctx, db, noteID)
	if err != nil {
		return nil, err
	}
	cutoff := len(revisions)
	if !throughRevisionID.IsZero() {
		cutoff = -1
		for i, r := range revisions {
			if r.ID == throughRevisionID {
				cutoff = i + 1
				break
			}
		}
		if cutoff < 0 {
			return nil, store.ErrNotFound
		}
	}

	start := 0
	for i := cutoff - 1; i >= 0; i-- {
		if revisions[i].Kind == store.RevisionKindCheckpoint {
			start = i
			break
		}
	}

	doc := yjsadapter.New()
	for i := start; i < cutoff; i++ {
		if err := applyRevision(entry, workspaceID, noteID, revisions[i], &doc); err != nil {
			doc.Close()
			return nil, err
		}
	}
	return doc, nil
}

// applyRevision decrypts one revision and either restores *doc from it (a
// checkpoint) or applies it as an incremental update (a delta), replacing
// *doc's old value with a restored one only in the checkpoint case.
func applyRevision(entry workspaceKeyEntry, workspaceID, noteID model.ID, r store.Revision, doc **yjsadapter.Document) error {
	metadata := corecrypto.ObjectMetadata{
		SchemaVersion: corecrypto.SchemaVersionV1,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		ObjectID:      noteID.Bytes(),
		ObjectType:    corecrypto.ObjectTypeRevision,
	}
	plaintext, err := corecrypto.UnpackAndOpenObject(entry.Key, metadata, r.Data)
	if err != nil {
		return fmt.Errorf("account: open revision: %w", err)
	}
	defer plaintext.Close()

	return plaintext.Use(func(data []byte) error {
		switch r.Kind {
		case store.RevisionKindCheckpoint:
			restored, err := yjsadapter.Restore(yjsadapter.Format(r.Format), data)
			if err != nil {
				return fmt.Errorf("account: restore revision checkpoint: %w", err)
			}
			(*doc).Close()
			*doc = restored
			return nil
		case store.RevisionKindDelta:
			if err := (*doc).ApplyUpdate(yjsadapter.Format(r.Format), data); err != nil {
				return fmt.Errorf("account: apply revision delta: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("account: revision has unknown kind %d", r.Kind)
		}
	})
}

func verifyNoteInWorkspace(ctx context.Context, exec store.Executor, workspaceID, noteID model.ID) error {
	note, err := store.GetNote(ctx, exec, noteID)
	if err != nil {
		return err
	}
	if note.WorkspaceID != workspaceID {
		return store.ErrWrongWorkspace
	}
	return nil
}

// diffLines computes a line-based diff between a and b using a classic
// O(n*m) longest-common-subsequence backtrack. Note bodies are small enough
// (the fixed home-use ceiling is 20,000 notes, not 20,000-line notes) that
// this is not a hot path worth a linear-space algorithm.
func diffLines(a, b string) []DiffLine {
	aLines := splitLinesKeepEmpty(a)
	bLines := splitLinesKeepEmpty(b)
	n, m := len(aLines), len(bLines)

	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if aLines[i] == bLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	result := make([]DiffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case aLines[i] == bLines[j]:
			result = append(result, DiffLine{Op: DiffEqual, Text: aLines[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			result = append(result, DiffLine{Op: DiffDelete, Text: aLines[i]})
			i++
		default:
			result = append(result, DiffLine{Op: DiffInsert, Text: bLines[j]})
			j++
		}
	}
	for ; i < n; i++ {
		result = append(result, DiffLine{Op: DiffDelete, Text: aLines[i]})
	}
	for ; j < m; j++ {
		result = append(result, DiffLine{Op: DiffInsert, Text: bLines[j]})
	}
	return result
}

func splitLinesKeepEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// utf16Len returns the length of s in UTF-16 code units, matching Y.Text's
// indexing unit (core/sync/yjsadapter wraps a Yjs-compatible CRDT whose
// text indices are UTF-16 code units for JS interoperability, not Go runes
// or bytes).
func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}
