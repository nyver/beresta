package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"testing"

	_ "github.com/AnoRebel/go-sqlcipher"
)

func openTestDB(t testing.TB) *sql.DB {
	t.Helper()
	path := filepath.Join(tempDBDir(t), "beresta.db")
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsAreSortedAndWellFormed(t *testing.T) {
	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations() error = %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("Migrations() returned no migrations")
	}
	if !sort.SliceIsSorted(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version }) {
		t.Fatal("Migrations() is not sorted by version")
	}
	if migrations[0].Version != 1 || migrations[0].Name != "initial_schema" {
		t.Fatalf("first migration = %+v, want version=1 name=initial_schema", migrations[0])
	}
	for _, m := range migrations {
		if m.SQL == "" {
			t.Fatalf("migration %d (%s) has empty SQL", m.Version, m.Name)
		}
	}
}

func TestParseMigrationFilename(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		wantVersion int
		wantName    string
		wantErr     bool
	}{
		{"well formed", "0001_initial_schema.sql", 1, "initial_schema", false},
		{"multi-word name", "12_add_saved_searches.sql", 12, "add_saved_searches", false},
		{"missing underscore", "0001.sql", 0, "", true},
		{"empty name", "0001_.sql", 0, "", true},
		{"non-numeric version", "abc_initial.sql", 0, "", true},
		{"zero version", "0_initial.sql", 0, "", true},
		{"negative version", "-1_initial.sql", 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, name, err := parseMigrationFilename(tt.filename)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidMigration) {
					t.Fatalf("parseMigrationFilename(%q) error = %v, want ErrInvalidMigration", tt.filename, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMigrationFilename(%q) error = %v", tt.filename, err)
			}
			if version != tt.wantVersion || name != tt.wantName {
				t.Fatalf("parseMigrationFilename(%q) = (%d, %q), want (%d, %q)", tt.filename, version, name, tt.wantVersion, tt.wantName)
			}
		})
	}
}

func TestMigrateAppliesSchema(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	version, err := Migrate(ctx, db)
	if err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if want := latestMigrationVersion(t); version != want {
		t.Fatalf("Migrate() version = %d, want %d", version, want)
	}

	expectedTables := []string{
		"accounts", "devices", "workspaces", "workspace_keys", "notebooks", "tags",
		"notes", "note_tags", "crdt_states", "crdt_updates", "revisions",
		"attachments", "note_attachments", "outbox", "inbox", "sync_cursors",
		"snapshots", "backups", "saved_searches", "notes_fts", "schema_migrations",
	}
	for _, table := range expectedTables {
		var name string
		err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing after migration: %v", table, err)
		}
	}

	var recordedVersion int
	var recordedName string
	if err := db.QueryRowContext(ctx, `SELECT version, name FROM schema_migrations WHERE version = 1`).Scan(&recordedVersion, &recordedName); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if recordedName != "initial_schema" {
		t.Fatalf("recorded migration name = %q, want initial_schema", recordedName)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	first, err := Migrate(ctx, db)
	if err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	second, err := Migrate(ctx, db)
	if err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	if first != second {
		t.Fatalf("second Migrate() version = %d, want %d", second, first)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if want := migrationCount(t); count != want {
		t.Fatalf("schema_migrations row count = %d, want %d (no re-application)", count, want)
	}
}

// latestMigrationVersion and migrationCount let assertions track the
// embedded migration set instead of hardcoding a version number that a
// future migration would silently make stale.
func latestMigrationVersion(t *testing.T) int {
	t.Helper()
	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	return migrations[len(migrations)-1].Version
}

func migrationCount(t *testing.T) int {
	t.Helper()
	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	return len(migrations)
}

func TestMigrateFTS5RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	noteID := []byte{0x01, 0x02, 0x03}
	if _, err := db.ExecContext(ctx, `INSERT INTO notes_fts (note_id, title, body) VALUES (?, ?, ?)`,
		noteID, "Shopping list", "Buy milk and bread"); err != nil {
		t.Fatalf("insert into notes_fts: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT note_id FROM notes_fts WHERE notes_fts MATCH ?`, "milk")
	if err != nil {
		t.Fatalf("query notes_fts: %v", err)
	}
	defer rows.Close()

	var matched [][]byte
	for rows.Next() {
		var id []byte
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		matched = append(matched, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(matched) != 1 {
		t.Fatalf("matched %d rows, want 1", len(matched))
	}
}

func TestMigrateEnforcesForeignKeys(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	deviceID := []byte{0x02}
	unknownWorkspaceID := []byte{0xff}
	_, err := db.ExecContext(ctx,
		`INSERT INTO notes (id, workspace_id, title, title_physical_ms, title_logical, title_device_id, created_physical_ms, created_logical, created_device_id)
		 VALUES (?, ?, '', 1, 0, ?, 1, 0, ?)`,
		[]byte{0x03}, unknownWorkspaceID, deviceID, deviceID,
	)
	if err == nil {
		t.Fatal("expected a foreign key violation for an unknown workspace_id")
	}
}
