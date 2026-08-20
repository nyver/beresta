package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInitializeCreatesDurableServerStateAndIsIdempotent(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	cfg := DefaultConfig()
	cfg.Server.DataDirectory = directory

	first, err := Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstFingerprint := first.TLSIdentity.Fingerprint
	var migrations int
	if err := first.Database.QueryRow("SELECT count(*) FROM server_schema_migrations").Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 2 {
		t.Fatalf("migration count = %d, want 2", migrations)
	}
	if concurrent, err := Initialize(context.Background(), cfg); err == nil {
		concurrent.Close()
		t.Fatal("second process acquired the active server data directory")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.TLSIdentity.Fingerprint != firstFingerprint {
		t.Fatal("TLS identity changed on restart")
	}
	for _, path := range []string{
		filepath.Join(directory, "beresta.db"),
		filepath.Join(directory, "blobs"),
		filepath.Join(directory, "backups"),
		filepath.Join(directory, "tls", "server.crt"),
		filepath.Join(directory, "tls", "server.key"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected initialized path %s: %v", path, err)
		}
	}
	if runtime.GOOS != "windows" {
		assertMode(t, directory, 0o700)
		assertMode(t, filepath.Join(directory, "beresta.db"), 0o600)
		assertMode(t, filepath.Join(directory, "tls", "server.key"), 0o600)
	}
}

func assertMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual := info.Mode().Perm(); actual != expected {
		t.Fatalf("mode for %s = %o, want %o", path, actual, expected)
	}
}
