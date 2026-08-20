package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigUsesDefaultsWhenFileIsAbsent(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:8443" || cfg.Server.DataDirectory != "./data" {
		t.Fatalf("unexpected defaults: %+v", cfg.Server)
	}
	if cfg.Auth.SessionTTL.Value() != 24*time.Hour {
		t.Fatalf("session TTL = %s", cfg.Auth.SessionTTL.Value())
	}
}

func TestLoadConfigAppliesDataOverrideAndRejectsUnknownFields(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  listen: 0.0.0.0:9443\n  data_dir: ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path, filepath.Join(directory, "override"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "0.0.0.0:9443" || cfg.Server.DataDirectory != filepath.Join(directory, "override") {
		t.Fatalf("unexpected overlay: %+v", cfg.Server)
	}

	if err := os.WriteFile(path, []byte("server:\n  unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("unknown configuration field was accepted")
	}
}

func TestLoadConfigRejectsSessionLongerThanTwentyFourHours(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("auth:\n  session_ttl: 25h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path, ""); err == nil {
		t.Fatal("unsafe session lifetime was accepted")
	}
}
