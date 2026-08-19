//go:build windows && cgo

package main

import (
	"context"

	"golang.org/x/sys/windows"

	"github.com/beresta-app/beresta/core/keystore"
	windowskeystore "github.com/beresta-app/beresta/desktop/platform/keystore"
)

// newKeyWrapper selects Windows Hello-gated DPAPI when the current
// Windows installation supports and has it configured, falling back to
// plain user-scoped DPAPI otherwise (see
// windowskeystore.Recommended and docs/threat-model.md). It reports the
// selected keystore.Protection's display name alongside the wrapper so
// the frontend can show which protection mode guards the active account.
func newKeyWrapper(ctx context.Context, prompt string) (keystore.Wrapper, string, error) {
	wrapper, err := windowskeystore.Recommended(ctx, windowskeystore.SystemVerifier{}, foregroundWindow, prompt)
	if err != nil {
		return nil, "", err
	}
	return wrapper, wrapper.Protection().String(), nil
}

// foregroundWindow returns the handle of the window currently in the
// foreground, used to anchor the native Windows Hello consent prompt.
// Wails v2 does not expose the app window's own HWND across platforms, and
// the desktop shell is expected to hold focus whenever it triggers a
// verification prompt, so this is an accepted lightweight substitute for
// tracking the handle through window creation.
func foregroundWindow() uintptr {
	return uintptr(windows.GetForegroundWindow())
}
