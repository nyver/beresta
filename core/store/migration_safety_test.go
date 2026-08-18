package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store/sqlcipherdb"
)

func openSQLCipherForTest(t *testing.T, path string, key *corecrypto.Secret) *sql.DB {
	t.Helper()
	var db *sql.DB
	err := key.Use(func(k []byte) error {
		opened, err := sqlcipherdb.Open(path, k)
		if err != nil {
			return err
		}
		db = opened
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestBackupAndRestoreDatabaseFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beresta.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()

	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	workspaceID := seedWorkspace(t, db)

	backupPath := filepath.Join(dir, "beresta.pre-migration.bak")
	if err := BackupDatabaseFile(ctx, db, path, backupPath); err != nil {
		t.Fatalf("BackupDatabaseFile() error = %v", err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	// Simulate later damage: the live database loses its data (a bad
	// migration, an operator mistake, whatever the specific cause).
	if _, err := db.ExecContext(ctx, `DELETE FROM workspaces`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	if err := RestoreDatabaseFile(ctx, backupPath, path); err != nil {
		t.Fatalf("RestoreDatabaseFile() error = %v", err)
	}

	// Check for a leftover -wal/-shm sidecar immediately after restoring,
	// before opening any connection: opening a WAL-mode connection recreates
	// them as part of normal operation, so checking afterward would not
	// prove RestoreDatabaseFile itself cleaned up the stale pair from the
	// database it just overwrote.
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			t.Fatalf("stale %s sidecar still present immediately after restore", suffix)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	restored, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	restored.SetMaxOpenConns(1)
	defer restored.Close()

	var count int
	if err := restored.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id = ?`, workspaceID.Bytes()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("workspace count after restore = %d, want 1 (the pre-backup workspace must be back)", count)
	}
}

func TestBackupDatabaseFileRejectsCanceledContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beresta.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = BackupDatabaseFile(ctx, db, path, filepath.Join(dir, "backup.db"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BackupDatabaseFile() error = %v, want context.Canceled", err)
	}
}

func TestOpenTakesSafetyBackupOnlyForAPreExistingSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beresta.db")
	key := testDatabaseKey(t, 0x60)
	defer key.Close()
	ctx := context.Background()

	// A brand-new database (schema version 0 before Open) has nothing yet
	// worth protecting, so Open must not create a backup file for it even
	// though every embedded migration is "pending" on first run.
	db, version, err := Open(ctx, path, key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	db.Close()
	if version == 0 {
		t.Fatalf("Open() version = %d, want a positive schema version", version)
	}
	assertNoBackupFiles(t, dir)

	// Reopening an already-up-to-date database has no migration pending,
	// so it must not create a backup either.
	db2, _, err := Open(ctx, path, key)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	db2.Close()
	assertNoBackupFiles(t, dir)
}

// TestOpenTakesSafetyBackupBeforeApplyingAPendingMigration builds a
// database that only has migration 0001 applied (standing in for a real
// installation that predates migration 0002) and proves a second Open call
// — the one that actually has a migration to run — leaves a pre-migration
// backup file behind before applying it.
func TestOpenTakesSafetyBackupBeforeApplyingAPendingMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beresta.db")
	key := testDatabaseKey(t, 0x61)
	defer key.Close()
	ctx := context.Background()

	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 2 {
		t.Skip("this test needs at least two embedded migrations to exercise a pending upgrade")
	}

	rawDB := openSQLCipherForTest(t, path, key)
	if _, err := rawDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version         INTEGER PRIMARY KEY,
		name            TEXT NOT NULL,
		applied_unix_ms INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, migrations[0].SQL); err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_unix_ms) VALUES (?, ?, ?)`,
		migrations[0].Version, migrations[0].Name, 1000,
	); err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, rawDB)
	rawDB.Close()

	db, version, err := Open(ctx, path, key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if version != migrations[len(migrations)-1].Version {
		t.Fatalf("Open() version = %d, want %d", version, migrations[len(migrations)-1].Version)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE id = ?`, workspaceID.Bytes()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("pre-existing workspace data did not survive the migration")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if matchesPreMigrationBackupName(e.Name()) {
			found = true
		}
	}
	if !found {
		t.Fatal("Open() did not leave a pre-migration safety backup file behind")
	}
}

func TestRebuildFTSIndex(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "Grocery list", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, db, note.ID, "Grocery list", "buy oat milk"); err != nil {
		t.Fatal(err)
	}

	if err := RebuildFTSIndex(ctx, db); err != nil {
		t.Fatalf("RebuildFTSIndex() error = %v", err)
	}

	results, err := SearchNotes(ctx, db, workspaceID, SearchQuery{Text: "oat milk"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Note.ID != note.ID {
		t.Fatalf("SearchNotes() after RebuildFTSIndex() = %+v, want just %v", results, note.ID)
	}
}

func assertNoBackupFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if matchesPreMigrationBackupName(e.Name()) {
			t.Fatalf("unexpected pre-migration backup file %q", e.Name())
		}
	}
}

func matchesPreMigrationBackupName(name string) bool {
	return len(name) > len(".pre-migration-v") && filepath.Ext(name) == ".bak"
}
