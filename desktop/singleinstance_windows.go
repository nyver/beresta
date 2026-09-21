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
// (options.App.Title in main.go).
const mainWindowTitle = "Beresta"

// mainWindowClassName is the Win32 window class Wails registers the main
// window under when options.App.Windows.WindowClassName is left unset (see
// wails/v2's internal/frontend/desktop/windows/window.go), which main.go
// does not override. findMainWindow matches on this rather than on
// visibility: the tray controller's own hidden helper window
// (desktop/platform/traymenu) shares the same title "Beresta" but is
// created under its own unique class name, so class name alone already
// disambiguates the two without having to exclude hidden windows - which
// would also incorrectly exclude the real main window whenever it is
// legitimately hidden to the tray (HideWindowOnClose in main.go), the
// exact case specs/windows-desktop-client's "Launch while hidden in tray"
// scenario requires this lookup to find.
const mainWindowClassName = "wailsWindow"

const (
	swRestore = 9
	// maxClassNameLength is generous headroom over Win32's own 256-char
	// class-name limit (WNDCLASSEX docs), not a tuned/measured value.
	maxClassNameLength = 256
)

var (
	singleInstanceUser32 = syscall.NewLazyDLL("user32.dll")

	procEnumWindows           = singleInstanceUser32.NewProc("EnumWindows")
	procGetClassNameW         = singleInstanceUser32.NewProc("GetClassNameW")
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
// window to the foreground, best-effort, restoring it first if it is
// currently hidden to the tray or minimized (specs/windows-desktop-
// client's "Launch while hidden in tray" scenario: the existing window
// must become visible and active, not stay reachable only via the tray
// icon). Does nothing if the window cannot be found at all.
func activateRunningInstance() {
	hwnd := findMainWindow()
	if hwnd == 0 {
		return
	}
	procShowWindowSI.Call(hwnd, uintptr(swRestore))
	procSetForegroundWindowSI.Call(hwnd)
}

// findMainWindow returns the handle of the top-level window titled
// mainWindowTitle under class mainWindowClassName, or 0 if none is found.
// Deliberately not filtered on visibility (see mainWindowClassName's doc
// comment): the real main window must still be found while legitimately
// hidden to the tray, which is exactly the state activateRunningInstance
// needs to restore it from.
func findMainWindow() uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		const enumContinue, enumStop = 1, 0
		classBuf := make([]uint16, maxClassNameLength)
		classLen, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&classBuf[0])), uintptr(len(classBuf)))
		if syscall.UTF16ToString(classBuf[:classLen]) != mainWindowClassName {
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
