package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/beresta-app/beresta/core/model"
)

const (
	searchDefaultLimit = 50
	searchMaxLimit     = 500
)

// ErrEmptySearchQuery reports a SearchQuery with no text and no filters,
// which SearchNotes rejects rather than silently returning an unbounded
// listing of the workspace under the guise of a search.
var ErrEmptySearchQuery = errors.New("store: search query has no text or filters")

// ErrUnknownSearchTag reports a `tag:` filter token, in free text or a saved
// search, that does not name any current tag in the workspace.
var ErrUnknownSearchTag = errors.New("store: unknown search tag")

// SearchQuery describes one full-text search request against a workspace's
// notes. A zero value matches nothing (see ErrEmptySearchQuery); at least
// one of Text, TagIDs, CreatedFromMS, or CreatedToMS must be set.
type SearchQuery struct {
	// Text is matched against note titles and bodies via FTS5. Empty means
	// the query is filter-only and results are ordered by creation date
	// instead of relevance rank.
	Text string
	// TagIDs restricts results to notes carrying every listed tag (AND
	// semantics, not OR).
	TagIDs []model.ID
	// CreatedFromMS and CreatedToMS bound the note's creation date as
	// inclusive Unix milliseconds; zero means unbounded on that side.
	CreatedFromMS int64
	CreatedToMS   int64
	// IncludeDeleted includes tombstoned notes; excluded by default.
	IncludeDeleted bool
	// Limit caps the number of results. Non-positive or oversized values
	// fall back to searchDefaultLimit / searchMaxLimit.
	Limit int
}

// SearchResult is one matched note plus its relevance rank. Rank follows
// SQLite FTS5's bm25 convention: lower is more relevant. Filter-only
// searches (empty Text) have no meaningful rank and report zero.
type SearchResult struct {
	Note model.Note
	Rank float64
}

// SearchNotes runs a full-text and/or filtered search over a workspace's
// notes. It rejects a context canceled before or during the query and stops
// promptly rather than materializing the full result set first: ctx is
// checked once before issuing SQL and again on every row while scanning, so
// a caller that cancels an in-flight interactive search (e.g. because the
// user kept typing) is not left waiting for the previous keystroke's query
// to finish.
func SearchNotes(ctx context.Context, exec Executor, workspaceID model.ID, q SearchQuery) ([]SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	text := strings.TrimSpace(q.Text)
	if text == "" && len(q.TagIDs) == 0 && q.CreatedFromMS == 0 && q.CreatedToMS == 0 {
		return nil, ErrEmptySearchQuery
	}

	limit := q.Limit
	if limit <= 0 {
		limit = searchDefaultLimit
	} else if limit > searchMaxLimit {
		limit = searchMaxLimit
	}

	var conds []string
	var args []any
	if !q.IncludeDeleted {
		conds = append(conds, "n.deleted = 0")
	}
	if q.CreatedFromMS != 0 {
		conds = append(conds, "n.created_physical_ms >= ?")
		args = append(args, q.CreatedFromMS)
	}
	if q.CreatedToMS != 0 {
		conds = append(conds, "n.created_physical_ms <= ?")
		args = append(args, q.CreatedToMS)
	}
	for _, tagID := range q.TagIDs {
		conds = append(conds, "EXISTS (SELECT 1 FROM note_tags WHERE note_id = n.id AND tag_id = ? AND present = 1)")
		args = append(args, tagID.Bytes())
	}

	var (
		query    string
		rowArgs  []any
		withRank bool
	)
	if text != "" {
		withRank = true
		where := append([]string{"notes_fts MATCH ?", "n.workspace_id = ?"}, conds...)
		query = fmt.Sprintf(
			`SELECT %s, bm25(notes_fts, 0.0, 5.0, 1.0) AS rank
			 FROM notes_fts JOIN notes n ON n.id = notes_fts.note_id
			 WHERE %s
			 ORDER BY rank ASC
			 LIMIT ?`,
			noteSearchColumns, strings.Join(where, " AND "),
		)
		rowArgs = append(rowArgs, ftsMatchQuery(text), workspaceID.Bytes())
		rowArgs = append(rowArgs, args...)
		rowArgs = append(rowArgs, limit)
	} else {
		where := append([]string{"n.workspace_id = ?"}, conds...)
		query = fmt.Sprintf(
			`SELECT %s
			 FROM notes n
			 WHERE %s
			 ORDER BY n.created_physical_ms DESC
			 LIMIT ?`,
			noteSearchColumns, strings.Join(where, " AND "),
		)
		rowArgs = append(rowArgs, workspaceID.Bytes())
		rowArgs = append(rowArgs, args...)
		rowArgs = append(rowArgs, limit)
	}

	rows, err := exec.QueryContext(ctx, query, rowArgs...)
	if err != nil {
		return nil, fmt.Errorf("store: search notes: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var rank float64
		var note model.Note
		if withRank {
			note, err = scanNote(rows, &rank)
		} else {
			note, err = scanNote(rows)
		}
		if err != nil {
			return nil, err
		}
		results = append(results, SearchResult{Note: note, Rank: rank})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: search notes: %w", err)
	}
	return results, nil
}

// noteSearchColumns is noteSelectColumns' field list qualified with the "n"
// alias search's queries use for the notes table, so it stays scan-compatible
// with scanNote while remaining unambiguous when joined against notes_fts
// (which also has a "title" column).
const noteSearchColumns = `n.id, n.workspace_id, n.notebook_id, n.notebook_physical_ms, n.notebook_logical, n.notebook_device_id,
	n.title, n.title_physical_ms, n.title_logical, n.title_device_id,
	n.flags, n.flags_physical_ms, n.flags_logical, n.flags_device_id,
	n.deleted, n.deleted_physical_ms, n.deleted_logical, n.deleted_device_id,
	n.created_physical_ms, n.created_logical, n.created_device_id`

// ftsMatchQuery turns free-form user text into an FTS5 MATCH expression that
// treats every word as a literal phrase prefix (implicit AND between them),
// so FTS5 query-syntax characters typed by the user (-, ", *, NEAR, OR, ...)
// never change the query's meaning or fail to parse, and a partial word (for
// example "run") matches any note containing a token that starts with it
// (for example "running"), not only an exact token match. The trailing "*"
// must sit outside the closing quote - FTS5 parses `"word"*` as a
// phrase-prefix query, but `"word*"` as a literal (and never indexed)
// asterisk character inside the phrase.
func ftsMatchQuery(text string) string {
	fields := strings.Fields(text)
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, `"`+strings.ReplaceAll(f, `"`, `""`)+`"*`)
	}
	return strings.Join(parts, " ")
}

// ParseSearchQueryText parses the small filter language accepted by search
// boxes and stored verbatim in SavedSearch.Query: bare words become
// full-text search terms; `tag:<name>` requires the named workspace tag
// (AND semantics, resolved by name so the language is what a user typed and
// stayed readable when saved); `after:<unix-ms>` and `before:<unix-ms>`
// bound the note's creation date; `deleted:true` includes tombstoned notes.
// A `tag:` token naming a tag that no longer exists (renamed or deleted
// since the query was saved) is rejected with ErrUnknownSearchTag rather
// than silently matching nothing.
func ParseSearchQueryText(ctx context.Context, exec Executor, workspaceID model.ID, text string) (SearchQuery, error) {
	var q SearchQuery
	var words []string
	for _, tok := range strings.Fields(text) {
		switch {
		case strings.HasPrefix(tok, "tag:"):
			name := tok[len("tag:"):]
			if name == "" {
				return SearchQuery{}, fmt.Errorf("%w: empty tag filter", ErrInvalidName)
			}
			tag, err := GetTagByName(ctx, exec, workspaceID, name)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return SearchQuery{}, fmt.Errorf("%w: %q", ErrUnknownSearchTag, name)
				}
				return SearchQuery{}, err
			}
			q.TagIDs = append(q.TagIDs, tag.ID)
		case strings.HasPrefix(tok, "after:"):
			ms, err := strconv.ParseInt(tok[len("after:"):], 10, 64)
			if err != nil {
				return SearchQuery{}, fmt.Errorf("store: invalid after: filter: %w", err)
			}
			q.CreatedFromMS = ms
		case strings.HasPrefix(tok, "before:"):
			ms, err := strconv.ParseInt(tok[len("before:"):], 10, 64)
			if err != nil {
				return SearchQuery{}, fmt.Errorf("store: invalid before: filter: %w", err)
			}
			q.CreatedToMS = ms
		case tok == "deleted:true":
			q.IncludeDeleted = true
		default:
			words = append(words, tok)
		}
	}
	q.Text = strings.Join(words, " ")
	return q, nil
}
