package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
	if migrations != 5 {
		t.Fatalf("migration count = %d, want 5", migrations)
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

// TestServeKeepsServingWhenTheStartupBackupFails guards against a failed
// startup backup (for example a full disk) stopping the whole sync service:
// under a container restart policy that became an endless crash loop that
// left every client offline.
func TestServeKeepsServingWhenTheStartupBackupFails(t *testing.T) {
	root := t.TempDir()
	// A regular file where the backup directory should be makes every
	// backup attempt fail without depending on platform permissions.
	blocked := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := probe.Addr().String()
	probe.Close()

	cfg := DefaultConfig()
	cfg.Server.DataDirectory = filepath.Join(root, "data")
	cfg.Server.Listen = listen
	cfg.Backups.Enabled = true
	cfg.Backups.Directory = blocked
	runtime, err := Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- runtime.Serve(ctx) }()

	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}, // #nosec G402 -- liveness probe of a self-signed test identity; pinning is covered elsewhere.
	}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-served:
			cancel()
			t.Fatalf("Serve returned %v after the startup backup failed, want it to keep serving", err)
		default:
		}
		response, err := client.Get("https://" + listen + "/health")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server never answered /health: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("Serve after cancel: %v", err)
	}
}
