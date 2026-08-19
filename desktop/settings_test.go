package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/beresta-app/beresta/locales"
)

// TestLoadSettingsBackfillsMissingBackupDirectory proves that a
// settings.json written before AppSettings.BackupDirectory existed (so it
// decodes with that field empty) has the default backup directory
// backfilled rather than being rejected wholesale by validate() and
// silently reverting every other setting - including the user's chosen
// language and last database path - back to defaultSettings().
func TestLoadSettingsBackfillsMissingBackupDirectory(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	dir, err := appDataDir()
	if err != nil {
		t.Fatalf("appDataDir() error = %v", err)
	}
	legacyJSON := `{"language":"ru","last_database_path":"C:\\notes\\beresta.db","auto_lock_minutes":30}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := loadSettings()
	if err != nil {
		t.Fatalf("loadSettings() error = %v", err)
	}
	if got.Language != locales.Russian {
		t.Fatalf("loadSettings().Language = %q, want %q (legacy value must survive)", got.Language, locales.Russian)
	}
	if got.LastDatabasePath != `C:\notes\beresta.db` {
		t.Fatalf("loadSettings().LastDatabasePath = %q, want the legacy value preserved", got.LastDatabasePath)
	}
	if got.AutoLockMinutes != 30 {
		t.Fatalf("loadSettings().AutoLockMinutes = %d, want the legacy value preserved", got.AutoLockMinutes)
	}
	if got.BackupDirectory == "" {
		t.Fatal("loadSettings().BackupDirectory is empty, want it backfilled with a default")
	}
}

// TestDefaultSettingsDoesNotCreateBackupDirectory proves defaultSettings
// (called on every App construction, including in tests) only resolves
// the default backup directory path and never creates it on disk -
// account.CreateBackup already creates destRoot itself when a backup is
// actually written, so eagerly creating an unused directory here would
// just be an unwanted side effect of constructing an App.
func TestDefaultSettingsDoesNotCreateBackupDirectory(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	settings := defaultSettings()
	if settings.BackupDirectory == "" {
		t.Fatal("defaultSettings().BackupDirectory is empty")
	}
	if _, err := os.Stat(settings.BackupDirectory); !os.IsNotExist(err) {
		t.Fatalf("default backup directory %q exists after defaultSettings(), want it left uncreated (stat err = %v)", settings.BackupDirectory, err)
	}
}
