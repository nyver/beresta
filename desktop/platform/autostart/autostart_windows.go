//go:build windows

// Package autostart manages the desktop app's opt-in "launch at sign-in"
// registration through the per-user HKCU Run registry key (see
// specs/windows-desktop-client/spec.md, "Windows quick capture
// integration"). It only ever touches HKCU: a per-user Run entry needs
// no elevation and only affects the signed-in user, matching the app's
// single-user local install model.
package autostart

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath is where Windows reads per-user auto-launch entries.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// valueName identifies this app's entry among any other program's.
const valueName = "Beresta"

// Status reports the live registry state for exePath's autostart entry.
type Status struct {
	// Enabled is true only when a Run value named "Beresta" exists and
	// points at exePath; a value pointing elsewhere (see ConflictPath)
	// counts as not enabled for this install.
	Enabled bool
	// ConflictPath is the command line stored in the Run value when it
	// exists but does not resolve to exePath - for example a stale entry
	// left behind by an install that was later moved or reinstalled
	// elsewhere. Empty whenever there is no such entry.
	ConflictPath string
}

// Query reports whether exePath is currently registered to launch at
// sign-in.
func Query(exePath string) (Status, error) {
	return queryKey(runKeyPath, valueName, exePath)
}

// Enable registers exePath to launch, hidden to the tray, at sign-in,
// overwriting any prior value under this app's own value name.
func Enable(exePath string) error {
	return enableKey(runKeyPath, valueName, exePath)
}

// Disable removes this app's autostart entry.
func Disable(exePath string) error {
	return disableKey(runKeyPath, valueName, exePath)
}

func queryKey(path, name, exePath string) (Status, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("open %s key: %w", path, err)
	}
	defer k.Close()

	value, _, err := k.GetStringValue(name)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return Status{}, nil
		}
		return Status{}, fmt.Errorf("read %q value: %w", name, err)
	}
	if commandPath(value) == exePath {
		return Status{Enabled: true}, nil
	}
	return Status{ConflictPath: value}, nil
}

func enableKey(path, name, exePath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open %s key: %w", path, err)
	}
	defer k.Close()
	command := fmt.Sprintf(`"%s" --autostart`, exePath)
	if err := k.SetStringValue(name, command); err != nil {
		return fmt.Errorf("write %q value: %w", name, err)
	}
	return nil
}

// disableKey is a no-op, not an error, both when no entry exists and
// when the existing entry belongs to a different install (a mismatched
// path): a caller only ever asks to stop starting *this* executable
// automatically, so Disable must never delete a Run entry it does not
// recognize as its own.
func disableKey(path, name, exePath string) error {
	status, err := queryKey(path, name, exePath)
	if err != nil {
		return err
	}
	if !status.Enabled {
		return nil
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open %s key: %w", path, err)
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("delete %q value: %w", name, err)
	}
	return nil
}

// commandPath extracts the executable path from a Run value that may be
// quoted and may carry trailing arguments, as Enable writes.
func commandPath(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
	}
	if idx := strings.IndexByte(command, ' '); idx >= 0 {
		return command[:idx]
	}
	return command
}
