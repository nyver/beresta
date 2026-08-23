//go:build windows

// singleinstance_windows.go guards against launching a second desktop
// process while one is already running: two processes would each start
// their own tray icon and hotkey registration, and could race opening
// the same account database. acquireSingleInstanceLock is called first
// thing in main(), before any of that state exists.
package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// singleInstanceMutexName is a session-independent ("Global\") named
// mutex, so a duplicate launch is caught even from a different Windows
// logon session (Remote Desktop, Fast User Switching). The GUID suffix
// keeps the name from ever colliding with an unrelated application's own
// named mutex.
const singleInstanceMutexName = `Global\Beresta-9F3E9F0C-6C34-4C7E-9E2B-9E9F5A2D8B31`

// mainWindowTitle is the title Wails gives the main window
// (options.App.Title in main.go) and, incidentally, the tray controller's
// own hidden helper window (desktop/platform/traymenu); findVisibleMainWindow
// filters to visible windows so it can only ever match the former.
const mainWindowTitle = "Beresta"

const swRestore = 9

var (
	singleInstanceUser32 = syscall.NewLazyDLL("user32.dll")

	procEnumWindows           = singleInstanceUser32.NewProc("EnumWindows")
	procIsWindowVisible       = singleInstanceUser32.NewProc("IsWindowVisible")
	procGetWindowTextW        = singleInstanceUser32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW  = singleInstanceUser32.NewProc("GetWindowTextLengthW")
	procShowWindowSI          = singleInstanceUser32.NewProc("ShowWindow")
	procSetForegroundWindowSI = singleInstanceUser32.NewProc("SetForegroundWindow")
)

// acquireSingleInstanceLock reports whether another Beresta process is
// already running, by holding a named mutex for the remaining lifetime of
// this process. The handle is intentionally never closed: the OS releases
// it automatically on exit (including a crash), which is what makes it a
// reliable single-instance signal in the first place. A failure to even
// perform the check (err != nil) is never treated as "already running": a
// legitimate launch must not be blocked by, say, a transient failure to
// reach the kernel object namespace.
func acquireSingleInstanceLock() (alreadyRunning bool, err error) {
	namePtr, err := windows.UTF16PtrFromString(singleInstanceMutexName)
	if err != nil {
		return false, fmt.Errorf("encode single-instance mutex name: %w", err)
	}
	_, err = windows.CreateMutex(nil, false, namePtr)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return true, nil
		}
		return false, fmt.Errorf("create single-instance mutex: %w", err)
	}
	return false, nil
}

// activateRunningInstance brings the already-running instance's main
// window to the foreground, best-effort. It intentionally does nothing
// when that window cannot be found - for example, the running instance is
// hidden to the tray (HideWindowOnClose in main.go): the tray icon it
// already owns is the correct way for the user to reach it, and there is
// nothing else in that state worth forcing onscreen.
func activateRunningInstance() {
	hwnd := findVisibleMainWindow()
	if hwnd == 0 {
		return
	}
	procShowWindowSI.Call(hwnd, uintptr(swRestore))
	procSetForegroundWindowSI.Call(hwnd)
}

// findVisibleMainWindow returns the handle of the visible top-level
// window titled mainWindowTitle, or 0 if none is found. It must filter on
// visibility: the tray controller's own hidden helper window shares the
// same window title but is never shown, so an unfiltered title-only
// lookup could return that window instead of the real one.
func findVisibleMainWindow() uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		const enumContinue, enumStop = 1, 0
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
			return enumContinue
		}
		length, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if length == 0 {
			return enumContinue
		}
		buf := make([]uint16, length+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if syscall.UTF16ToString(buf) != mainWindowTitle {
			return enumContinue
		}
		found = hwnd
		return enumStop
	})
	procEnumWindows.Call(cb, 0)
	return found
}
