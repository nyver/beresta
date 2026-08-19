//go:build windows

package main

import (
	"fmt"
	"os"

	"github.com/beresta-app/beresta/desktop/platform/autostart"
)

// applyAutostartReal is App's default autostartApply: it resolves the
// running executable's own path so Enable/Disable always registers
// *this* install, never a hardcoded or relative path that would break
// once the user moves or reinstalls the app.
func applyAutostartReal(enabled bool) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if enabled {
		return autostart.Enable(exePath)
	}
	return autostart.Disable(exePath)
}

// autostartStatus reports the live registry state for the running
// executable (see App.AutostartStatus).
func autostartStatus() (autostart.Status, error) {
	exePath, err := os.Executable()
	if err != nil {
		return autostart.Status{}, fmt.Errorf("resolve executable path: %w", err)
	}
	return autostart.Query(exePath)
}
