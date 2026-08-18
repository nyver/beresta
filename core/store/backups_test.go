package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestExportPlaintextSnapshotProducesReadablePlaintext(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO workspaces (id, created_physical_ms, created_logical, created_device_id) VALUES (?, 1, 0, ?)`,
		bytesOf(0x01, 16), bytesOf(0x02, 16),
	); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "plaintext-snapshot.db")
	if err := ExportPlaintextSnapshot(ctx, db, destPath); err != nil {
		t.Fatalf("ExportPlaintextSnapshot: %v", err)
	}

	plain, err := sql.Open("sqlite3", destPath)
	if err != nil {
		t.Fatalf("open exported snapshot: %v", err)
	}
	defer plain.Close()

	var count int
	if err := plain.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&count); err != nil {
		t.Fatalf("query exported snapshot without any key (must be plaintext): %v", err)
	}
	if count != 1 {
		t.Fatalf("workspace count in exported snapshot = %d, want 1", count)
	}
}
