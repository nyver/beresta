package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
)

// searchFixtureWords are recycled across the fixture's synthetic bodies so
// FTS5 has a realistic vocabulary to rank against, instead of every note
// being trivially unique.
var searchFixtureWords = []string{
	"project", "meeting", "recipe", "travel", "invoice", "garden", "budget", "workout",
	"reading", "notes", "plan", "review", "draft", "summary", "idea", "reminder",
	"family", "client", "vendor", "report",
}

// seedSearchFixture populates a workspace with n notes for benchmarking
// SearchNotes at scale: every note gets a synthetic title/body drawn from a
// shared word pool, and exactly one note (the "needle") contains a unique
// token so a benchmark can search for it and know precisely how many rows
// should match. All inserts run in one transaction to keep fixture setup
// itself off the per-search critical path being measured.
func seedSearchFixture(tb testing.TB, db *sql.DB, workspaceID model.ID, n int) model.ID {
	tb.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		tb.Fatalf("BeginTx() error = %v", err)
	}
	defer tx.Rollback()

	const needleIndex = 12345
	var needleID model.ID
	for i := 0; i < n; i++ {
		w1 := searchFixtureWords[i%len(searchFixtureWords)]
		w2 := searchFixtureWords[(i*7+3)%len(searchFixtureWords)]
		title := fmt.Sprintf("%s %s %d", w1, w2, i)
		body := fmt.Sprintf("This note is about %s and %s, item number %d in the fixture.", w1, w2, i)
		if i == needleIndex {
			title = "zzzneedlezzz " + title
			body = "zzzneedlezzz " + body
		}
		clock := repoClock(tb, uint64(1000+i), 0, 0x04)
		note, err := CreateNote(ctx, tx, workspaceID, model.Nil, title, clock)
		if err != nil {
			tb.Fatalf("CreateNote(%d) error = %v", i, err)
		}
		if err := ReplaceNoteFTS(ctx, tx, note.ID, title, body); err != nil {
			tb.Fatalf("ReplaceNoteFTS(%d) error = %v", i, err)
		}
		if i == needleIndex {
			needleID = note.ID
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatalf("Commit() error = %v", err)
	}
	return needleID
}

// TestSearch20000NoteBudget proves SearchNotes meets the release-quality
// spec's 150ms local-search budget at the documented 20,000-note ceiling
// (openspec/changes/build-e2ee-notes-app/specs/release-quality/spec.md,
// "Target-scale performance budgets"). It is skipped in -short mode because
// seeding 20,000 notes dominates the run time of an already-slow suite.
func TestSearch20000NoteBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 20,000-note search benchmark fixture in -short mode")
	}

	db := repoTestDB(t)
	workspaceID := seedWorkspace(t, db)
	needleID := seedSearchFixture(t, db, workspaceID, 20000)

	start := time.Now()
	results, err := SearchNotes(context.Background(), db, workspaceID, SearchQuery{Text: "zzzneedlezzz"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("SearchNotes() error = %v", err)
	}
	if len(results) != 1 || results[0].Note.ID != needleID {
		t.Fatalf("SearchNotes() = %+v, want exactly the needle note %v", results, needleID)
	}

	const budget = 150 * time.Millisecond
	if elapsed > budget {
		t.Fatalf("SearchNotes() took %v at 20,000 notes, want <= %v", elapsed, budget)
	}
}

// BenchmarkSearchNotes measures SearchNotes' steady-state cost at the
// 20,000-note ceiling; run with `go test -bench=SearchNotes -run=^$`.
func BenchmarkSearchNotes(b *testing.B) {
	db := repoTestDB(b)
	workspaceID := seedWorkspace(b, db)
	seedSearchFixture(b, db, workspaceID, 20000)

	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := SearchNotes(ctx, db, workspaceID, SearchQuery{Text: "project meeting"}); err != nil {
			b.Fatalf("SearchNotes() error = %v", err)
		}
	}
}
