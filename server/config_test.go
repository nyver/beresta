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

// TestLoadConfigRewritesLoggingDirectoryUnderTheDataOverride covers task
// 4.5: bounded file logging is on by default, and a --data override
// relocates its default directory under the new data root, matching the
// identical rewiring LoadConfig already applies to Backups.Directory and
// TLS.ACME.CacheDir - a log directory left pointing at the old default
// after --data would write logs somewhere the operator never asked for.
func TestLoadConfigRewritesLoggingDirectoryUnderTheDataOverride(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Directory != "./data/logs" {
		t.Fatalf("default logging.directory = %q, want ./data/logs", cfg.Logging.Directory)
	}

	dataOverride := filepath.Join(t.TempDir(), "override")
	overridden, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"), dataOverride)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dataOverride, "logs"); overridden.Logging.Directory != want {
		t.Fatalf("logging.directory = %q, want %q", overridden.Logging.Directory, want)
	}

	// An explicit logging.directory in the file must survive the
	// override untouched, exactly like Backups.Directory and
	// TLS.ACME.CacheDir already do.
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("logging:\n  directory: /custom/logs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	explicit, err := LoadConfig(path, filepath.Join(directory, "override"))
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Logging.Directory != "/custom/logs" {
		t.Fatalf("logging.directory = %q, want the explicit /custom/logs preserved", explicit.Logging.Directory)
	}
}

func TestConfigValidateRejectsNegativeLoggingBounds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Logging.MaxSizeMB = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative logging.max_size_mb was accepted")
	}
	cfg = DefaultConfig()
	cfg.Logging.MaxBackups = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("negative logging.max_backups was accepted")
	}
}
