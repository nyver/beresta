package server

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// TestApplyMigrationsAppliesEmbeddedSchemaAndRecordsChecksums covers task
// 5.8's server migration coverage: every embedded migration is applied
// exactly once and recorded in server_schema_migrations with the checksum
// of its own embedded SQL, matching readMigrations' own accounting.
func TestApplyMigrationsAppliesEmbeddedSchemaAndRecordsChecksums(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()

	migrations, err := readMigrations()
	if err != nil {
		t.Fatalf("readMigrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("readMigrations returned no migrations")
	}

	for _, item := range migrations {
		var name, checksum string
		if err := runtime.Database.QueryRowContext(ctx,
			"SELECT name, checksum FROM server_schema_migrations WHERE version = ?", item.version,
		).Scan(&name, &checksum); err != nil {
			t.Fatalf("migration %d not recorded: %v", item.version, err)
		}
		if name != item.name {
			t.Fatalf("migration %d recorded name = %q, want %q", item.version, name, item.name)
		}
		if checksum != item.checksum {
			t.Fatalf("migration %d recorded checksum = %q, want %q", item.version, checksum, item.checksum)
		}
	}
}

// TestApplyMigrationsIsIdempotent covers task 5.8's restart-safe migration
// maintenance: applying the same embedded migration set to an already
// up-to-date database (mirroring every server restart) must be a no-op
// that neither re-runs any migration's SQL nor duplicates its ledger row.
func TestApplyMigrationsIsIdempotent(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()

	before := countMigrationLedgerRows(t, runtime.Database)
	if err := applyMigrations(ctx, runtime.Database); err != nil {
		t.Fatalf("second applyMigrations: %v", err)
	}
	after := countMigrationLedgerRows(t, runtime.Database)
	if before != after {
		t.Fatalf("server_schema_migrations row count = %d after re-applying, want unchanged %d", after, before)
	}
}

// TestApplyOneMigrationRollsBackFailedMigrationLeavingLedgerAndSchemaUnchanged
// covers task 5.8's transactional/recoverable migration requirement: a
// migration whose SQL fails partway through (a malformed later statement)
// must leave neither its ledger row nor any of its earlier statements'
// schema changes behind, because applyOneMigration runs the whole
// migration - SQL and ledger insert - inside one transaction it rolls back
// on any error.
func TestApplyOneMigrationRollsBackFailedMigrationLeavingLedgerAndSchemaUnchanged(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()

	before := countMigrationLedgerRows(t, runtime.Database)
	broken := migration{
		version:  999999,
		name:     "broken_migration_for_test",
		checksum: "test-checksum",
		sql: `CREATE TABLE test_txn_rollback_marker (id INTEGER PRIMARY KEY);
THIS IS NOT VALID SQL;`,
	}
	if err := applyOneMigration(ctx, runtime.Database, broken); err == nil {
		t.Fatal("applyOneMigration with malformed SQL succeeded, want an error")
	}

	if countMigrationLedgerRows(t, runtime.Database) != before {
		t.Fatal("a failed migration must not add a ledger row")
	}
	var name string
	err := runtime.Database.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'test_txn_rollback_marker'`,
	).Scan(&name)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a failed migration must not leave its earlier statements' schema changes behind: sqlite_master lookup err = %v", err)
	}
}

// TestApplyMigrationsRecoversAfterAPriorFailedAttempt covers task 5.8's
// recoverable migration requirement from the operator's side: after one
// migration attempt fails and rolls back (simulated directly, since the
// embedded migration set itself is always well-formed), the database is
// left exactly as it was, so the ordinary embedded migration set still
// applies cleanly - nothing about the failed attempt corrupts or blocks
// recovery.
func TestApplyMigrationsRecoversAfterAPriorFailedAttempt(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()

	broken := migration{
		version:  999999,
		name:     "broken_migration_for_test",
		checksum: "test-checksum",
		sql:      `NOT VALID SQL AT ALL;`,
	}
	if err := applyOneMigration(ctx, runtime.Database, broken); err == nil {
		t.Fatal("applyOneMigration with malformed SQL succeeded, want an error")
	}

	if err := applyMigrations(ctx, runtime.Database); err != nil {
		t.Fatalf("applyMigrations after a prior failed attempt: %v", err)
	}
}

// TestApplyMigrationsDetectsChecksumMismatch covers task 5.8's migration
// safety net: if a ledger row's recorded checksum no longer matches the
// embedded migration's own content - the embedded binary and the
// previously-migrated database disagree about what was actually applied -
// startup must fail loudly instead of silently trusting a stale or
// tampered ledger entry.
func TestApplyMigrationsDetectsChecksumMismatch(t *testing.T) {
	runtime := newTestRuntime(t)
	ctx := context.Background()

	migrations, err := readMigrations()
	if err != nil {
		t.Fatalf("readMigrations: %v", err)
	}
	target := migrations[0]
	if _, err := runtime.Database.ExecContext(ctx,
		`UPDATE server_schema_migrations SET checksum = 'tampered' WHERE version = ?`, target.version,
	); err != nil {
		t.Fatalf("tamper with recorded checksum: %v", err)
	}

	if err := applyMigrations(ctx, runtime.Database); err == nil {
		t.Fatal("applyMigrations with a tampered ledger checksum succeeded, want an error")
	}
}

func countMigrationLedgerRows(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM server_schema_migrations`,
	).Scan(&count); err != nil {
		t.Fatalf("count server_schema_migrations rows: %v", err)
	}
	return count
}
