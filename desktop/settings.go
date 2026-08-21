package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/beresta-app/beresta/locales"
)

// appDataDirName is the fixed per-user directory name under the OS config
// root (%AppData% on Windows) that holds both this app's own settings and,
// by default, a new local account's encrypted database.
const appDataDirName = "Beresta"

// AppSettings is desktop-local application configuration: it exists before
// any account is created or unlocked (English/Russian onboarding needs a
// language before there is an account to store one in) and is never
// synchronized or encrypted, since it carries no note content or secret
// material.
type AppSettings struct {
	// Language is an ISO code from locales.Supported.
	Language string `json:"language"`
	// LastDatabasePath is the most recently used local account database
	// path, offered as the default on the next unlock.
	LastDatabasePath string `json:"last_database_path"`
	// AutoLockMinutes is how long the unlocked account may sit idle before
	// the desktop shell locks it automatically. Zero disables automatic
	// locking.
	AutoLockMinutes int `json:"auto_lock_minutes"`
	// BackupDirectory is destRoot for every daily/manual/restore-safety
	// backup this device writes (account.CreateBackup et al.). It defaults
	// under the app data directory but the user may point it at any
	// external location (task 5.7's "external backup-directory UI");
	// unlike LastDatabasePath, it is never bound to a particular account,
	// since one Beresta installation backs up whichever account is
	// currently open.
	BackupDirectory string `json:"backup_directory"`
	// QuickNoteHotkey is the global accelerator (for example
	// "Ctrl+Shift+N") that opens the quick-note capture surface even
	// while the main window is hidden (task 5.9). Empty disables the
	// global hotkey; the tray icon and menu remain available either way.
	QuickNoteHotkey string `json:"quick_note_hotkey"`
	// AutostartEnabled opts this install into launching, hidden to the
	// tray, at Windows sign-in (task 5.9). It is off by default: launch
	// at sign-in is a deliberate per-install choice, never assumed.
	AutostartEnabled bool `json:"autostart_enabled"`
	// SyncEnabled attaches the optional HTTP transport. Disabling it never
	// removes local notes, cursors, or queued operations.
	SyncEnabled      bool   `json:"sync_enabled"`
	SyncServerURL    string `json:"sync_server_url"`
	SyncSecurityMode string `json:"sync_security_mode"`
	SyncFingerprint  string `json:"sync_fingerprint"`
}

func defaultSettings() AppSettings {
	dir, err := defaultBackupDirectory()
	if err != nil {
		// appDataDirPath() only fails if the OS config directory cannot be
		// resolved at all, a condition the app cannot run under regardless;
		// leaving BackupDirectory empty here still lets GetSettings/
		// onboarding succeed, and UpdateSettings' validation will reject an
		// empty value once the user is prompted to fix it.
		dir = ""
	}
	return AppSettings{
		Language:        locales.English,
		AutoLockMinutes: 15,
		BackupDirectory: dir,
		QuickNoteHotkey: DefaultQuickNoteHotkey,
	}
}

// defaultBackupDirectory returns the default backup destination offered
// before the user picks their own: a "backups" subdirectory next to this
// app's own settings file, kept separate from any account database
// directory since BackupDirectory is not account-specific. It only
// resolves the path, deliberately not creating it (unlike appDataDir()) -
// account.CreateBackup already creates destRoot itself, and defaultSettings
// runs on every App construction (including in tests), where reaching out
// to create real directories on disk as a side effect would be surprising.
func defaultBackupDirectory() (string, error) {
	dir, err := appDataDirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "backups"), nil
}

func (s AppSettings) validate() error {
	supported := false
	for _, l := range locales.Supported() {
		if s.Language == l {
			supported = true
			break
		}
	}
	if !supported {
		return &AppError{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("unsupported language %q", s.Language)}
	}
	if s.AutoLockMinutes < 0 {
		return &AppError{Code: ErrCodeInvalidInput, Message: "auto-lock minutes must not be negative"}
	}
	if strings.TrimSpace(s.BackupDirectory) == "" {
		return &AppError{Code: ErrCodeInvalidInput, Message: "backup directory must not be empty"}
	}
	if _, _, err := parseHotkey(s.QuickNoteHotkey); err != nil {
		return &AppError{Code: ErrCodeInvalidInput, Message: err.Error()}
	}
	if s.SyncEnabled {
		parsed, err := url.Parse(s.SyncServerURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return &AppError{Code: ErrCodeInvalidInput, Message: "sync server must be an absolute HTTPS URL"}
		}
		if s.SyncSecurityMode != "pinned" && s.SyncSecurityMode != "trusted" {
			return &AppError{Code: ErrCodeInvalidInput, Message: "sync security mode must be pinned or trusted"}
		}
		if s.SyncSecurityMode == "pinned" {
			fingerprint := strings.ReplaceAll(strings.TrimSpace(s.SyncFingerprint), ":", "")
			decoded, err := hex.DecodeString(fingerprint)
			if err != nil || len(decoded) != 32 {
				return &AppError{Code: ErrCodeInvalidInput, Message: "sync fingerprint must be a SHA-256 digest"}
			}
		}
	}
	return nil
}

// appDataDirPath resolves this app's per-user data directory path without
// creating it. Callers that need the directory to exist on disk (writing a
// file into it) should use appDataDir() instead.
func appDataDirPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(root, appDataDirName), nil
}

// appDataDir returns (creating if necessary) this app's per-user data
// directory.
func appDataDir() (string, error) {
	dir, err := appDataDirPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create app data directory: %w", err)
	}
	return dir, nil
}

func settingsFilePath() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// loadSettings reads persisted settings, falling back to defaultSettings
// for a first run or an unreadable/corrupt file: settings are a
// convenience, never a prerequisite the app can fail to start over.
func loadSettings() (AppSettings, error) {
	path, err := settingsFilePath()
	if err != nil {
		return defaultSettings(), err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultSettings(), nil
		}
		return defaultSettings(), fmt.Errorf("read settings file: %w", err)
	}
	var s AppSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return defaultSettings(), fmt.Errorf("decode settings file: %w", err)
	}
	if strings.TrimSpace(s.BackupDirectory) == "" {
		// A settings.json written before BackupDirectory existed decodes
		// with it empty; backfilling the default here (instead of letting
		// validate() below reject the whole file) preserves the user's
		// actual language/database-path/auto-lock settings across the
		// upgrade instead of silently reverting all of them to defaults.
		if dir, err := defaultBackupDirectory(); err == nil {
			s.BackupDirectory = dir
		}
	}
	if s.validate() != nil {
		return defaultSettings(), nil
	}
	return s, nil
}

func saveSettings(s AppSettings) error {
	path, err := settingsFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write settings file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("publish settings file: %w", err)
	}
	return nil
}

// DefaultDatabasePath returns the default local account database location
// offered during onboarding: the last one used, or a fresh path under the
// app data directory for a first run.
func (a *App) DefaultDatabasePath() (string, error) {
	a.mu.Lock()
	last := a.settings.LastDatabasePath
	a.mu.Unlock()
	if last != "" {
		return last, nil
	}
	dir, err := appDataDir()
	if err != nil {
		return "", mapError(err)
	}
	return filepath.Join(dir, "beresta.db"), nil
}

// GetSettings returns the current application settings.
func (a *App) GetSettings() AppSettings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// UpdateSettings validates and persists next, replacing the current
// settings entirely. A changed QuickNoteHotkey or AutostartEnabled is
// applied to the live OS state (re-registering the global hotkey,
// enabling/disabling the Run-key entry) before the file is saved. If a
// later step in this sequence fails - the other OS-level change, or the
// save itself - every OS-level change already applied is rolled back
// before returning the error, so the live OS state can never end up
// matching next while settings.json and GetSettings still report
// previous (or vice versa): the two must always agree.
func (a *App) UpdateSettings(next AppSettings) (AppSettings, error) {
	if err := next.validate(); err != nil {
		return AppSettings{}, err
	}
	a.mu.Lock()
	previous := a.settings
	shell := a.shell
	a.mu.Unlock()

	var rollback []func()
	fail := func(err error) (AppSettings, error) {
		for i := len(rollback) - 1; i >= 0; i-- {
			rollback[i]()
		}
		return AppSettings{}, mapError(err)
	}

	if next.QuickNoteHotkey != previous.QuickNoteHotkey && shell != nil {
		mod, vk, _ := parseHotkey(next.QuickNoteHotkey) // already validated above
		if err := shell.SetHotkey(mod, vk); err != nil {
			return fail(fmt.Errorf("register quick-note hotkey: %w", err))
		}
		rollback = append(rollback, func() {
			if prevMod, prevVK, err := parseHotkey(previous.QuickNoteHotkey); err == nil {
				_ = shell.SetHotkey(prevMod, prevVK)
			}
		})
	}
	if next.AutostartEnabled != previous.AutostartEnabled {
		if err := a.applyAutostart(next.AutostartEnabled); err != nil {
			return fail(fmt.Errorf("update autostart registration: %w", err))
		}
		rollback = append(rollback, func() {
			_ = a.applyAutostart(previous.AutostartEnabled)
		})
	}

	if err := saveSettings(next); err != nil {
		return fail(err)
	}
	a.mu.Lock()
	a.settings = next
	a.mu.Unlock()
	return next, nil
}

// Locale returns the full string catalog for locale ("en" or "ru") for the
// frontend to render from, plus the list of supported locale codes.
type LocaleCatalog struct {
	Locale    string            `json:"locale"`
	Strings   map[string]string `json:"strings"`
	Supported []string          `json:"supported"`
}

// Catalog returns the string catalog for the requested locale. An empty
// locale uses the current setting.
func (a *App) Catalog(locale string) (LocaleCatalog, error) {
	if locale == "" {
		a.mu.Lock()
		locale = a.settings.Language
		a.mu.Unlock()
	}
	catalog, err := locales.Catalog(locale)
	if err != nil {
		return LocaleCatalog{}, &AppError{Code: ErrCodeInvalidInput, Message: err.Error()}
	}
	return LocaleCatalog{Locale: locale, Strings: catalog, Supported: locales.Supported()}, nil
}
