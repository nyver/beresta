package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/server"
)

func TestRunInitOnlyCreatesRequestedDataRoot(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "server-data")
	if err := run([]string{"--data", directory, "--init-only"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"beresta.db", "tls/server.crt", "tls/server.key", "blobs"} {
		if _, err := os.Stat(filepath.Join(directory, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("expected %s: %v", relative, err)
		}
	}
}

// TestRunWritesStructuredLogFileAcrossRestarts covers task 4.5's wiring:
// the default config enables bounded file logging under <data>/logs, a
// restarted process (a second run against the same --data directory)
// appends to it rather than erroring or clobbering it, and a config
// override for max_size_mb/max_backups is accepted and reaches
// configureLogger without failing startup. internal/logging's own tests
// cover the rotation arithmetic itself at byte precision; this only
// proves the CLI wires a real config through to a real file across
// process lifetimes.
func TestRunWritesStructuredLogFileAcrossRestarts(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "server-data")
	configPath := filepath.Join(directory, "config.yaml")

	if err := run([]string{"--data", directory, "--init-only"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(directory, "logs", "beresta-server.log")
	first, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file after first run: %v", err)
	}
	if !bytes.Contains(first, []byte("server initialized")) {
		t.Fatalf("log file missing the startup record: %s", first)
	}

	// A tiny max_size_mb/max_backups override must be accepted (Validate
	// only rejects negative values) and never fail startup, even though a
	// single run's log volume will not actually cross a whole-megabyte
	// bound.
	if err := os.WriteFile(configPath, []byte("logging:\n  max_size_mb: 1\n  max_backups: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := run([]string{"--data", directory, "--init-only"}, &bytes.Buffer{}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	second, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file after restarts: %v", err)
	}
	if len(second) <= len(first) {
		t.Fatalf("log file did not grow across restarts (len=%d, want > %d) - a restart must append, not truncate", len(second), len(first))
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	if err := run([]string{"unexpected"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unexpected positional argument was accepted")
	}
}

func TestAdministrativeCommandsAndDestructiveDryRuns(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "server-data")
	var inviteOutput bytes.Buffer
	if err := run([]string{"--data", directory, "invite", "--name", "Alice"}, &inviteOutput); err != nil {
		t.Fatal(err)
	}
	inviteCode := strings.TrimSpace(inviteOutput.String())
	if len(inviteCode) < 40 || strings.Contains(inviteCode, "{") {
		t.Fatalf("invite output is not a single opaque code: %q", inviteCode)
	}

	cfg, err := server.LoadConfig(filepath.Join(directory, "config.yaml"), directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := server.Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	userID := testID(t)
	deviceID := testID(t)
	workspaceID := testID(t)
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Storage.Register(context.Background(), server.Registration{
		InviteCode: inviteCode, UserID: userID, IdentityPublic: bytes.Repeat([]byte{1}, 32), AuthorityPublic: bytes.Repeat([]byte{2}, 32),
		DeviceID: deviceID, DeviceName: "Alice device", SigningPublic: publicKey,
		WorkspaceID: workspaceID, WorkspaceKeyID: strings.Repeat("1", 32),
		WorkspaceEnvelope: []byte("opaque envelope"), KeybagCiphertext: []byte("opaque keybag"),
	}, time.Now())
	if err != nil {
		runtime.Close()
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	var usersOutput bytes.Buffer
	if err := run([]string{"--data", directory, "users", "list"}, &usersOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(usersOutput.String(), userID) {
		t.Fatalf("users list omitted registered user: %s", usersOutput.String())
	}
	if err := run([]string{"--data", directory, "device", "revoke", "--id", deviceID}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--data", directory, "verify"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}

	backupDestination := filepath.Join(t.TempDir(), "backups")
	var backupOutput bytes.Buffer
	if err := run([]string{"--data", directory, "backup", "--destination", backupDestination}, &backupOutput); err != nil {
		t.Fatal(err)
	}
	var backup server.ServerBackup
	if err := json.Unmarshal(backupOutput.Bytes(), &backup); err != nil || backup.Path == "" {
		t.Fatalf("decode backup output: path=%q error=%v", backup.Path, err)
	}
	if err := run([]string{"--data", directory, "verify", "--backup", backup.Path}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var restoreOutput bytes.Buffer
	if err := run([]string{"--data", directory, "restore", "--backup", backup.Path}, &restoreOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(restoreOutput.String(), `"dry_run": true`) {
		t.Fatalf("restore without confirmation was not a dry run: %s", restoreOutput.String())
	}
	var gcOutput bytes.Buffer
	if err := run([]string{"--data", directory, "gc"}, &gcOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gcOutput.String(), `"dry_run": true`) {
		t.Fatalf("gc without confirmation was not a dry run: %s", gcOutput.String())
	}
}

func testID(t *testing.T) string {
	t.Helper()
	id, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id.String()
}
