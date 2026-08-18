package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
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

func TestExportEncryptedSnapshotRoundTripsThroughOpen(t *testing.T) {
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

	dir := t.TempDir()
	plaintextPath := filepath.Join(dir, "plaintext-snapshot.db")
	if err := ExportPlaintextSnapshot(ctx, db, plaintextPath); err != nil {
		t.Fatalf("ExportPlaintextSnapshot: %v", err)
	}
	plainDB, err := sql.Open("sqlite3", plaintextPath)
	if err != nil {
		t.Fatalf("open plaintext snapshot: %v", err)
	}
	defer plainDB.Close()

	key := bytesOf(0x99, 32)
	encryptedPath := filepath.Join(dir, "re-encrypted.db")
	if err := ExportEncryptedSnapshot(ctx, plainDB, encryptedPath, key); err != nil {
		t.Fatalf("ExportEncryptedSnapshot: %v", err)
	}

	keySecret, err := corecrypto.TakeSecret(append([]byte(nil), key...))
	if err != nil {
		t.Fatal(err)
	}
	reopened, version, err := Open(ctx, encryptedPath, keySecret)
	keySecret.Close()
	if err != nil {
		t.Fatalf("Open re-encrypted snapshot with the fresh key: %v", err)
	}
	defer reopened.Close()
	if version == 0 {
		t.Fatalf("re-encrypted snapshot version = %d, want a positive schema version", version)
	}
	var count int
	if err := reopened.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&count); err != nil {
		t.Fatalf("query re-encrypted snapshot: %v", err)
	}
	if count != 1 {
		t.Fatalf("workspace count = %d, want 1", count)
	}

	// The wrong key must not open it.
	wrongKeySecret, err := corecrypto.TakeSecret(bytesOf(0x77, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer wrongKeySecret.Close()
	if _, _, err := Open(ctx, encryptedPath, wrongKeySecret); err == nil {
		t.Fatal("Open with the wrong key succeeded, want an error")
	}
}
