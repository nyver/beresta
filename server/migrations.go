package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var serverMigrations embed.FS

type migration struct {
	version  int64
	name     string
	checksum string
	sql      string
}

func applyMigrations(ctx context.Context, database *sql.DB) error {
	migrations, err := readMigrations()
	if err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS server_schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("initialize server migration ledger: %w", err)
	}

	for _, item := range migrations {
		var appliedName, appliedChecksum string
		err := database.QueryRowContext(ctx,
			"SELECT name, checksum FROM server_schema_migrations WHERE version = ?", item.version,
		).Scan(&appliedName, &appliedChecksum)
		switch {
		case err == nil:
			if appliedName != item.name {
				return fmt.Errorf("migration version %d was applied as %q, expected %q", item.version, appliedName, item.name)
			}
			if appliedChecksum != item.checksum {
				return fmt.Errorf("applied migration %q does not match its embedded checksum", item.name)
			}
			continue
		case err != sql.ErrNoRows:
			return fmt.Errorf("inspect migration %d: %w", item.version, err)
		}

		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.version, err)
		}
		if _, err := transaction.ExecContext(ctx, item.sql); err != nil {
			transaction.Rollback()
			return fmt.Errorf("apply migration %s: %w", item.name, err)
		}
		if _, err := transaction.ExecContext(ctx,
			"INSERT INTO server_schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, unixepoch())",
			item.version, item.name, item.checksum,
		); err != nil {
			transaction.Rollback()
			return fmt.Errorf("record migration %s: %w", item.name, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", item.name, err)
		}
	}
	return nil
}

func readMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(serverMigrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded server migrations: %w", err)
	}
	items := make([]migration, 0, len(entries))
	versions := make(map[int64]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q has no numeric version prefix", entry.Name())
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration %q has an invalid version", entry.Name())
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("migrations %q and %q use duplicate version %d", previous, entry.Name(), version)
		}
		contents, err := fs.ReadFile(serverMigrations, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		versions[version] = entry.Name()
		items = append(items, migration{
			version:  version,
			name:     entry.Name(),
			checksum: hex.EncodeToString(digest[:]),
			sql:      string(contents),
		})
	}
	sort.Slice(items, func(left, right int) bool { return items[left].version < items[right].version })
	return items, nil
}
