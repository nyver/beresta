package main

import "github.com/wailsapp/wails/v2/pkg/runtime"

// Event names the frontend subscribes to via the Wails runtime's
// EventsOn. Payload shapes are documented next to each Emit call site.
const (
	// EventAccountUnlocked carries an AccountInfo when a local account
	// becomes the active one (after CreateAccount or UnlockAccount).
	EventAccountUnlocked = "account:unlocked"
	// EventAccountLocked carries no payload; it fires whenever the active
	// account is locked, including on application shutdown.
	EventAccountLocked = "account:locked"
	// EventSyncStatus carries the current transport.Status string whenever
	// synchronization status is queried or changes.
	EventSyncStatus = "sync:status"
	// EventQuickNoteOpen carries no payload; it fires whenever the global
	// quick-note hotkey is pressed or the tray menu's "Quick Note" item is
	// selected, after the main window has already been shown/restored.
	EventQuickNoteOpen = "quicknote:open"
	// EventWorkspaceChanged carries no payload; it fires whenever
	// SetActiveWorkspace or AcceptWorkspaceGrant changes which workspace is
	// active, so the frontend knows to reload notes/notebooks/tags instead
	// of showing state scoped to the previously active workspace.
	EventWorkspaceChanged = "workspace:changed"
)

// emit forwards a Wails runtime event, but only once startup(ctx) has
// wired the frontend dispatcher into a.ctx. Calling runtime.EventsEmit
// with a context that has no dispatcher attached terminates the process
// (see wailsapp/wails/v2/pkg/runtime.getEvents), so every emission in this
// package must go through here rather than calling runtime.EventsEmit
// directly - this also makes the App type usable in unit tests that never
// call startup.
func (a *App) emit(event string, data ...any) {
	a.mu.Lock()
	ready := a.ready
	ctx := a.ctx
	a.mu.Unlock()
	if !ready {
		return
	}
	runtime.EventsEmit(ctx, event, data...)
}
