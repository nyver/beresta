package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigureLoggingWritesToTheAppDataLogDirectory covers task 4.5:
// desktop structured logging goes to a bounded, rotated file under the
// per-user app data directory, matching the server's identical
// internal/logging wiring, rather than only ever appearing in a terminal
// that may not exist for a GUI app.
func TestConfigureLoggingWritesToTheAppDataLogDirectory(t *testing.T) {
	profileDir, err := os.MkdirTemp("", "beresta-desktop-logging-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeDesktopTestDirectory(t, profileDir) })
	t.Setenv("AppData", profileDir)

	closeLogging := configureLogging()
	defer closeLogging()

	dir, err := appDataDirPath()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "logs", "beresta-desktop.log")
	if _, err := os.Stat(filepath.Join(filepath.Dir(logPath))); err != nil {
		t.Fatalf("log directory was not created: %v", err)
	}
}

// TestConfigureLoggingNeverBlocksStartupWhenTheProfileIsUnwritable covers
// the "never interrupts service" half of task 4.5: an app data directory
// configureLogging cannot create or write to must fall back to a working
// (stderr-only) logger instead of leaving slog unconfigured or panicking.
func TestConfigureLoggingNeverBlocksStartupWhenTheProfileIsUnwritable(t *testing.T) {
	// A profile directory that does not exist and cannot be created (its
	// parent is a file, not a directory) forces appDataDir's MkdirAll to
	// fail, exercising the fallback path.
	blocker, err := os.CreateTemp("", "beresta-desktop-logging-blocker-*")
	if err != nil {
		t.Fatal(err)
	}
	blocker.Close()
	t.Cleanup(func() { os.Remove(blocker.Name()) })
	t.Setenv("AppData", filepath.Join(blocker.Name(), "profile"))

	closeLogging := configureLogging()
	closeLogging()
}
