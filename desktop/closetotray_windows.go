//go:build windows

// closetotray_windows.go implements the first-use close-to-tray education
// balloon (specs/windows-desktop-client's "Understandable tray and
// single-instance behavior": "The first close-to-tray event SHALL explain
// once that Beresta remains in the notification area.").
package main

import (
	"log/slog"
	"syscall"
	"time"
)

var procIsWindowVisibleCTT = syscall.NewLazyDLL("user32.dll").NewProc("IsWindowVisible")

// closeToTrayPollInterval is how often watchForFirstCloseToTray checks the
// main window's visibility. Coarse enough to cost nothing measurable; fine
// enough that the balloon appears within about a second of the close click.
const closeToTrayPollInterval = time.Second

const closeToTrayBalloonMessage = "Beresta is still running in the notification area. Use the tray icon to reopen, lock, or quit."

// balloonShower is satisfied by *traymenu.Controller. A separate interface
// here (rather than importing traymenu directly) keeps this file's only
// real dependency the one tray method it actually calls, matching
// shell.go's shellController.
type balloonShower interface {
	ShowBalloon(title, message string) error
}

// watchForFirstCloseToTray polls the main window's visibility and shows a
// one-time balloon the first time it is hidden via the close button
// (main.go's HideWindowOnClose) rather than quit outright. Nothing else in
// this app ever hides the main window - Wails' own OnClose handler is the
// only call site for WindowHide - so any visible->hidden transition
// observed here is, by construction, exactly that close-to-tray event.
// Runs until the process exits, or returns immediately (a permanent
// no-op) once the education has already been shown on this install:
// settings.CloseToTrayExplained is persisted so it survives restarts and
// is never re-shown.
func watchForFirstCloseToTray(tray balloonShower) {
	settings, err := loadSettings()
	if err != nil || settings.CloseToTrayExplained {
		return
	}

	ticker := time.NewTicker(closeToTrayPollInterval)
	defer ticker.Stop()
	wasVisible := true
	for range ticker.C {
		hwnd := findMainWindow()
		if hwnd == 0 {
			continue
		}
		visible, _, _ := procIsWindowVisibleCTT.Call(hwnd)
		nowVisible := visible != 0
		if wasVisible && !nowVisible {
			if err := tray.ShowBalloon("Beresta", closeToTrayBalloonMessage); err != nil {
				slog.Warn("close-to-tray balloon failed", "error_class", "tray_balloon_failed")
			}
			markCloseToTrayExplained()
			return
		}
		wasVisible = nowVisible
	}
}

// markCloseToTrayExplained persists CloseToTrayExplained=true so the
// balloon never shows again on this install. Best-effort: a failure to
// save just means the balloon might show once more on a future close,
// never a reason to treat the education itself as having failed.
func markCloseToTrayExplained() {
	current, err := loadSettings()
	if err != nil {
		current = defaultSettings()
	}
	current.CloseToTrayExplained = true
	if err := saveSettings(current); err != nil {
		slog.Warn("could not persist close-to-tray education flag", "error_class", "settings_save_failed")
	}
}
