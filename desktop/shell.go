package main

import (
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// shellController is the tray/hotkey platform surface App drives. The
// production value is *traymenu.Controller
// (desktop/platform/traymenu), started in main() before wails.Run so a
// failed tray/hotkey initialization can still decide HideWindowOnClose
// correctly; it stays nil whenever tray integration is unavailable, in
// which case UpdateSettings simply skips re-registering the hotkey.
type shellController interface {
	SetHotkey(mod, vk uint32) error
	Close()
}

// showWindow brings the main window to the foreground, restoring it from
// a minimized or tray-hidden state. It is a no-op before startup(ctx) has
// wired the Wails runtime dispatcher (see emit's doc comment for why that
// matters); in practice that only matters for the vanishingly unlikely
// case of the hotkey firing in the brief window between process start
// and OnStartup completing.
func (a *App) showWindow() {
	a.mu.Lock()
	ready := a.ready
	ctx := a.ctx
	a.mu.Unlock()
	if !ready {
		return
	}
	runtime.WindowShow(ctx)
	runtime.WindowUnminimise(ctx)
}

// handleQuickNoteTrigger is traymenu.Handlers.OnQuickNote: it satisfies
// the "quick-note while the main window is hidden" scenario in
// specs/windows-desktop-client/spec.md by showing/restoring the window
// before telling the frontend to open the capture surface, so the
// surface is never rendered behind a still-hidden window.
func (a *App) handleQuickNoteTrigger() {
	a.showWindow()
	a.emit(EventQuickNoteOpen)
}

// handleShowWindowTrigger is traymenu.Handlers.OnShowWindow.
func (a *App) handleShowWindowTrigger() {
	a.showWindow()
}

// handleQuitTrigger is traymenu.Handlers.OnQuit. Unlike the window's
// close button (intercepted by HideWindowOnClose when the tray started
// successfully), this always requests a real shutdown: HideWindowOnClose
// only changes what the OS close button does, it does not affect
// runtime.Quit.
func (a *App) handleQuitTrigger() {
	a.mu.Lock()
	ready := a.ready
	ctx := a.ctx
	a.mu.Unlock()
	if !ready {
		return
	}
	runtime.Quit(ctx)
}

// AutostartStatusDTO reports the live Windows Run-key state for this
// executable, which can drift from AppSettings.AutostartEnabled if the
// user removed the entry directly (for example, via Task Manager's
// Startup tab) or if it was left behind by a different install path.
type AutostartStatusDTO struct {
	Enabled      bool   `json:"enabled"`
	ConflictPath string `json:"conflict_path"`
}

// AutostartStatus returns the live Windows Run-key state for this
// executable, independent of the persisted AppSettings.AutostartEnabled
// intent.
func (a *App) AutostartStatus() (AutostartStatusDTO, error) {
	status, err := autostartStatus()
	if err != nil {
		return AutostartStatusDTO{}, mapError(err)
	}
	return AutostartStatusDTO{Enabled: status.Enabled, ConflictPath: status.ConflictPath}, nil
}
