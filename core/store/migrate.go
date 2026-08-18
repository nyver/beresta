package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// ErrInvalidMigration reports a malformed embedded migration file. Since
// migrations are compiled into the binary, this can only be a build-time
// packaging defect, not a runtime or user-data condition.
var ErrInvalidMigration = errors.New("store: invalid embedded migration")

// Migration is one embedded schema migration, applied in one transaction.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations returns every embedded migration sorted by version. It parses
// eagerly so a malformed embedded file is reported through the same
// sentinel whether the caller inspects the list or calls Migrate.
func Migrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("%w: read embedded migrations directory: %v", ErrInvalidMigration, err)
	}

	migrations := make([]Migration, 0, len(entries))
	seenVersions := make(map[int]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationFilename(entry.Name())
		if err != nil {
			return nil, err
		}
		if existing, exists := seenVersions[version]; exists {
			return nil, fmt.Errorf("%w: duplicate migration version %d (%s and %s)", ErrInvalidMigration, version, existing, entry.Name())
		}
		seenVersions[version] = entry.Name()

		content, err := migrationFiles.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidMigration, entry.Name(), err)
		}
		sqlText := strings.TrimSpace(string(content))
		if sqlText == "" {
			return nil, fmt.Errorf("%w: %s is empty", ErrInvalidMigration, entry.Name())
		}
		migrations = append(migrations, Migration{Version: version, Name: name, SQL: sqlText})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

func parseMigrationFilename(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, name, found := strings.Cut(base, "_")
	if !found || name == "" {
		return 0, "", fmt.Errorf("%w: migration filename %q must be <version>_<name>.sql", ErrInvalidMigration, filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("%w: migration filename %q has an invalid version prefix", ErrInvalidMigration, filename)
	}
	return version, name, nil
}

// Migrate applies every embedded migration newer than the database's
// recorded schema version, each inside its own transaction, and records the
// applied version and name in schema_migrations. It is idempotent and
// returns the resulting schema version. The caller is responsible for
// connection-level pragmas (WAL, foreign keys, busy timeout) and integrity
// checks before and after migration.
func Migrate(ctx context.Context, db *sql.DB) (int, error) {
	migrations, err := Migrations()
	if err != nil {
		return 0, err
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version         INTEGER PRIMARY KEY,
		name            TEXT NOT NULL,
		applied_unix_ms INTEGER NOT NULL
	)`); err != nil {
		return 0, fmt.Errorf("store: create schema_migrations table: %w", err)
	}

	current, err := schemaVersion(ctx, db)
	if err != nil {
		return 0, err
	}

	applied := current
	for _, migration := range migrations {
		if migration.Version <= current {
			continue
		}
		if err := applyMigration(ctx, db, migration); err != nil {
			return applied, err
		}
		applied = migration.Version
	}
	return applied, nil
}

// schemaVersion reads the database's current recorded schema version, or
// zero for a database that has never been migrated.
func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
	return int(version.Int64), nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("store: apply migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_unix_ms) VALUES (?, ?, ?)`,
		migration.Version, migration.Name, time.Now().UnixMilli(),
	); err != nil {
		return fmt.Errorf("store: record migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	return nil
}
