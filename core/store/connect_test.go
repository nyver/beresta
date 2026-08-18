package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
)

// tempDBDir behaves like t.TempDir but tolerates a brief delay before a cgo
// SQLite connection's Windows file handle on a -wal/-shm sidecar file is
// released, retrying cleanup instead of failing the test on a removal race
// unrelated to the behavior under test.
func tempDBDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "beresta-store-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for attempt := 0; attempt < 10; attempt++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	return dir
}

func testDatabaseKey(t *testing.T, seed byte) *corecrypto.Secret {
	t.Helper()
	raw := make([]byte, databaseKeyBytes)
	for i := range raw {
		raw[i] = seed
	}
	key, err := corecrypto.TakeSecret(raw)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestOpenAppliesMigrationsAndPassesIntegrityCheck(t *testing.T) {
	path := filepath.Join(tempDBDir(t), "beresta.db")
	key := testDatabaseKey(t, 0x41)
	defer key.Close()

	db, version, err := Open(context.Background(), path, key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if version != 1 {
		t.Fatalf("Open() version = %d, want 1", version)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'notes'`).Scan(&name); err != nil {
		t.Fatalf("notes table missing after Open(): %v", err)
	}
}

func TestOpenReopensWithSameKey(t *testing.T) {
	path := filepath.Join(tempDBDir(t), "beresta.db")
	key := testDatabaseKey(t, 0x42)
	defer key.Close()
	ctx := context.Background()

	first, _, err := Open(ctx, path, key)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if _, err := first.ExecContext(ctx,
		`INSERT INTO workspaces (id, created_physical_ms, created_logical, created_device_id) VALUES (?, 1, 0, ?)`,
		bytesOf(0x01, 16), bytesOf(0x02, 16)); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	first.Close()

	second, version, err := Open(ctx, path, key)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer second.Close()
	if version != 1 {
		t.Fatalf("second Open() version = %d, want 1", version)
	}
	var count int
	if err := second.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("workspace count = %d, want 1 (data must survive reopen)", count)
	}
}

func TestOpenRejectsWrongKey(t *testing.T) {
	path := filepath.Join(tempDBDir(t), "beresta.db")
	right := testDatabaseKey(t, 0x43)
	defer right.Close()
	ctx := context.Background()

	db, _, err := Open(ctx, path, right)
	if err != nil {
		t.Fatalf("Open() with the correct key error = %v", err)
	}
	db.Close()

	// A wrong SQLCipher key can fail as early as the connection-open page
	// read or, if that read is ever satisfied, at the explicit integrity
	// check; both are acceptable rejections of the wrong key.
	wrong := testDatabaseKey(t, 0x44)
	defer wrong.Close()
	if _, _, err := Open(ctx, path, wrong); err == nil {
		t.Fatal("Open() with the wrong key succeeded, want an error")
	}
}

func TestOpenRejectsClosedKey(t *testing.T) {
	path := filepath.Join(tempDBDir(t), "beresta.db")
	if _, _, err := Open(context.Background(), path, nil); !errors.Is(err, corecrypto.ErrSecretClosed) {
		t.Fatalf("Open() with a nil key error = %v, want ErrSecretClosed", err)
	}
}

func TestDeviceKeyIntegratesWithOpen(t *testing.T) {
	path := filepath.Join(tempDBDir(t), "beresta.db")
	wrapper := &fakeWrapper{protection: keystore.ProtectionWindowsDPAPI}
	ctx := context.Background()

	key, envelope, err := LoadOrCreateDatabaseKey(ctx, wrapper, "device-seven", nil)
	if err != nil {
		t.Fatal(err)
	}
	db, version, err := Open(ctx, path, key)
	key.Close()
	if err != nil {
		t.Fatalf("Open() with a keystore-wrapped key error = %v", err)
	}
	db.Close()
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}

	restoredKey, _, err := LoadOrCreateDatabaseKey(ctx, wrapper, "device-seven", envelope)
	if err != nil {
		t.Fatal(err)
	}
	defer restoredKey.Close()
	db2, _, err := Open(ctx, path, restoredKey)
	if err != nil {
		t.Fatalf("Open() with the restored key error = %v", err)
	}
	db2.Close()
}

func bytesOf(value byte, length int) []byte {
	out := make([]byte, length)
	for i := range out {
		out[i] = value
	}
	return out
}
