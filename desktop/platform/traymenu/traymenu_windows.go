//go:build windows

// Package traymenu implements the Windows system-tray icon, its context
// menu, and the global quick-note hotkey for the desktop shell (see
// specs/windows-desktop-client/spec.md, "Windows quick capture
// integration", and docs/architecture.md's Windows integration
// adapters). It talks to user32.dll/shell32.dll directly through
// syscall.NewLazyDLL rather than a cgo shim or a third-party tray
// library, matching the dependency-light style of
// desktop/platform/keystore's other Windows bindings.
//
// Win32 requires the window that owns a tray icon and a registered
// hotkey to pump its own message loop on the thread that created it, so
// Start launches a dedicated goroutine locked to one OS thread
// (runtime.LockOSThread) that creates a hidden window and blocks in that
// loop until Close is called. Every call that must run on that thread
// (registering a new hotkey, tearing everything down) is marshaled onto
// it with PostMessage, which - unlike most other user32 window calls -
// is documented as safe to call from any thread.
package traymenu

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	procGetModuleHandleW    = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procUnregisterClassW    = user32.NewProc("UnregisterClassW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procInsertMenuItemW     = user32.NewProc("InsertMenuItemW")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetCursorPos        = user32.NewProc("GetCursorPos")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
)

// Win32 constants used below. Only the subset this package actually
// needs is declared; see MSDN's winuser.h/shellapi.h documentation for
// the full definitions.
const (
	cwUseDefault = 0x80000000

	wmDestroy   = 0x0002
	wmClose     = 0x0010
	wmCommand   = 0x0111
	wmHotkey    = 0x0312
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmNull      = 0x0000
	wmApp       = 0x8000

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nimAdd     = 0
	nimDelete  = 2

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000

	miimFtype    = 0x00000100
	miimString   = 0x00000040
	miimID       = 0x00000002
	mftString    = 0x00000000
	mftSeparator = 0x00000800

	tpmBottomAlign = 0x0020
	tpmLeftAlign   = 0x0000

	idiApplication = 32512
	idcArrow       = 32512

	idTrayIcon = 1
	idHotkey   = 1

	idMenuQuickNote = 1
	idMenuShow      = 2
	idMenuQuit      = 3

	wmSystrayCallback = wmApp + 1
	wmSetHotkey       = wmApp + 2

	hotkeyReplyTimeout = 2 * time.Second
)

// point mirrors the Win32 POINT struct used by GetCursorPos.
type point struct{ x, y int32 }

// msg mirrors the Win32 MSG struct read by GetMessageW/PeekMessage.
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

// wndClassEx mirrors the Win32 WNDCLASSEXW struct passed to
// RegisterClassExW.
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

// notifyIconData mirrors the Win32 NOTIFYICONDATAW struct passed to
// Shell_NotifyIconW. Only the fields this package sets (through
// nifMessage|nifIcon|nifTip) are ever read; the trailing balloon/GUID
// fields exist purely so cbSize (computed from this struct's own size)
// matches the real ABI layout byte-for-byte.
type notifyIconData struct {
	size             uint32
	wnd              uintptr
	id               uint32
	flags            uint32
	callbackMessage  uint32
	icon             uintptr
	tip              [128]uint16
	state            uint32
	stateMask        uint32
	info             [256]uint16
	timeoutOrVersion uint32
	infoTitle        [64]uint16
	infoFlags        uint32
	guidItem         [16]byte
	balloonIcon      uintptr
}

// menuItemInfo mirrors the Win32 MENUITEMINFOW struct passed to
// InsertMenuItemW.
type menuItemInfo struct {
	size      uint32
	mask      uint32
	typ       uint32
	state     uint32
	id        uint32
	subMenu   uintptr
	checked   uintptr
	unchecked uintptr
	itemData  uintptr
	typeData  *uint16
	cch       uint32
	bmpItem   uintptr
}

// Handlers are invoked from a dedicated goroutine, never from the
// caller's own goroutine, whenever the corresponding tray/hotkey action
// happens; a nil handler is simply ignored.
type Handlers struct {
	// OnQuickNote fires when the global hotkey is pressed or the tray
	// menu's "Quick Note" item is selected.
	OnQuickNote func()
	// OnShowWindow fires when the tray icon is left-clicked or the
	// "Show Beresta" menu item is selected.
	OnShowWindow func()
	// OnQuit fires when the "Quit" menu item is selected.
	OnQuit func()
}

// Controller owns one running tray icon, context menu, and (optionally)
// global hotkey. Every exported method is safe to call from any
// goroutine.
type Controller struct {
	hwnd      uintptr
	instance  uintptr
	className *uint16
	nid       notifyIconData
	menu      uintptr
	handlers  Handlers
	done      chan struct{}

	closeOnce sync.Once

	hotkeyMu    sync.Mutex
	hotkeyReply chan error
}

// Start creates the hidden tray window, registers the tray icon and
// context menu, and - when vk != 0 - registers the initial global
// hotkey. On success it returns a running Controller; on a fatal setup
// error it returns (nil, err). A non-fatal initial hotkey registration
// failure (for example, another application already owns that key
// combination) is reported by returning a non-nil err alongside a
// non-nil, otherwise fully functional Controller, so the tray icon still
// works even when the hotkey does not.
func Start(h Handlers, mod, vk uint32) (*Controller, error) {
	c := &Controller{
		handlers:    h,
		done:        make(chan struct{}),
		hotkeyReply: make(chan error, 1),
	}
	ready := make(chan error, 1)
	go c.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	if vk == 0 {
		return c, nil
	}
	if err := c.SetHotkey(mod, vk); err != nil {
		return c, err
	}
	return c, nil
}

// SetHotkey replaces the currently registered global hotkey, or clears
// it entirely when vk == 0. It marshals the actual RegisterHotKey /
// UnregisterHotKey calls onto the window's owning thread and waits for
// the outcome.
func (c *Controller) SetHotkey(mod, vk uint32) error {
	c.hotkeyMu.Lock()
	defer c.hotkeyMu.Unlock()

	ret, _, callErr := procPostMessageW.Call(c.hwnd, uintptr(wmSetHotkey), uintptr(mod), uintptr(vk))
	if ret == 0 {
		return fmt.Errorf("post hotkey update: %w", callErr)
	}
	select {
	case err := <-c.hotkeyReply:
		return err
	case <-time.After(hotkeyReplyTimeout):
		return errors.New("traymenu: timed out updating the global hotkey")
	case <-c.done:
		return errors.New("traymenu: tray window is closed")
	}
}

// Close unregisters the hotkey, removes the tray icon, and destroys the
// hidden window, blocking until its message loop has fully exited. It is
// safe to call more than once.
func (c *Controller) Close() {
	c.closeOnce.Do(func() {
		procPostMessageW.Call(c.hwnd, uintptr(wmClose), 0, 0)
	})
	<-c.done
}

// run owns the hidden window and its message loop for the lifetime of
// the Controller; it must execute on a single, locked OS thread because
// window creation, RegisterHotKey/UnregisterHotKey, and the message pump
// itself are all thread-affine in Win32.
func (c *Controller) run(ready chan<- error) {
	runtime.LockOSThread()

	instance, _, _ := procGetModuleHandleW.Call(0)
	c.instance = instance

	className, err := syscall.UTF16PtrFromString(fmt.Sprintf("BerestaTrayWindow-%p", c))
	if err != nil {
		ready <- err
		return
	}
	c.className = className
	windowName, err := syscall.UTF16PtrFromString("Beresta")
	if err != nil {
		ready <- err
		return
	}

	icon, _, _ := procLoadIconW.Call(0, uintptr(idiApplication))
	cursor, _, _ := procLoadCursorW.Call(0, uintptr(idcArrow))

	wcex := wndClassEx{
		wndProc:   syscall.NewCallback(c.wndProc),
		instance:  instance,
		icon:      icon,
		cursor:    cursor,
		className: className,
		iconSm:    icon,
	}
	wcex.size = uint32(unsafe.Sizeof(wcex))
	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex))); atom == 0 {
		ready <- fmt.Errorf("register tray window class: %w", callErr)
		return
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0, // WS_OVERLAPPED, never shown
		uintptr(cwUseDefault), uintptr(cwUseDefault), uintptr(cwUseDefault), uintptr(cwUseDefault),
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		procUnregisterClassW.Call(uintptr(unsafe.Pointer(className)), instance)
		ready <- fmt.Errorf("create tray window: %w", callErr)
		return
	}
	c.hwnd = hwnd

	if err := c.addTrayIcon(icon); err != nil {
		procDestroyWindow.Call(hwnd)
		ready <- err
		return
	}
	if err := c.createMenu(); err != nil {
		c.removeTrayIcon()
		procDestroyWindow.Call(hwnd)
		ready <- err
		return
	}

	ready <- nil
	c.messageLoop()
}

func (c *Controller) addTrayIcon(icon uintptr) error {
	tip, err := syscall.UTF16FromString("Beresta")
	if err != nil {
		return fmt.Errorf("encode tray tooltip: %w", err)
	}
	c.nid = notifyIconData{
		wnd:             c.hwnd,
		id:              idTrayIcon,
		flags:           nifMessage | nifIcon | nifTip,
		callbackMessage: wmSystrayCallback,
		icon:            icon,
	}
	copy(c.nid.tip[:], tip)
	c.nid.size = uint32(unsafe.Sizeof(c.nid))

	ret, _, callErr := procShellNotifyIconW.Call(uintptr(nimAdd), uintptr(unsafe.Pointer(&c.nid)))
	if ret == 0 {
		return fmt.Errorf("add tray icon: %w", callErr)
	}
	return nil
}

func (c *Controller) removeTrayIcon() {
	procShellNotifyIconW.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&c.nid)))
}

func (c *Controller) createMenu() error {
	menu, _, callErr := procCreatePopupMenu.Call()
	if menu == 0 {
		return fmt.Errorf("create tray context menu: %w", callErr)
	}
	c.menu = menu
	items := []struct {
		id        uint32
		title     string
		separator bool
	}{
		{idMenuQuickNote, "Quick Note", false},
		{0, "", true},
		{idMenuShow, "Show Beresta", false},
		{0, "", true},
		{idMenuQuit, "Quit", false},
	}
	for position, item := range items {
		if err := c.insertMenuItem(uint32(position), item.id, item.title, item.separator); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) insertMenuItem(position, id uint32, title string, separator bool) error {
	mi := menuItemInfo{}
	if separator {
		mi.mask = miimFtype
		mi.typ = mftSeparator
	} else {
		titlePtr, err := syscall.UTF16PtrFromString(title)
		if err != nil {
			return fmt.Errorf("encode menu item %q: %w", title, err)
		}
		mi.mask = miimFtype | miimString | miimID
		mi.typ = mftString
		mi.id = id
		mi.typeData = titlePtr
		mi.cch = uint32(len(title))
	}
	mi.size = uint32(unsafe.Sizeof(mi))

	ret, _, callErr := procInsertMenuItemW.Call(c.menu, uintptr(position), 1, uintptr(unsafe.Pointer(&mi)))
	if ret == 0 {
		return fmt.Errorf("insert menu item %q: %w", title, callErr)
	}
	return nil
}

func (c *Controller) messageLoop() {
	for {
		var m msg
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	close(c.done)
}

func (c *Controller) showContextMenu() {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	// SetForegroundWindow before TrackPopupMenu, and an extra WM_NULL
	// after it returns, work around a documented Win32 quirk where the
	// popup otherwise fails to dismiss when the user clicks away from it
	// without choosing an item (see TrackPopupMenu's MSDN remarks).
	procSetForegroundWindow.Call(c.hwnd)
	procTrackPopupMenu.Call(c.menu, uintptr(tpmBottomAlign|tpmLeftAlign), uintptr(p.x), uintptr(p.y), 0, c.hwnd, 0)
	procPostMessageW.Call(c.hwnd, uintptr(wmNull), 0, 0)
}

// wndProc runs entirely on the loop thread (see run's doc comment); it
// must never block on anything the loop thread itself would need to
// unblock, which is why every user-facing handler callback is invoked
// from its own goroutine.
func (c *Controller) wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmSetHotkey:
		mod, vk := uint32(wParam), uint32(lParam)
		procUnregisterHotKey.Call(c.hwnd, uintptr(idHotkey))
		var err error
		if vk != 0 {
			ret, _, callErr := procRegisterHotKey.Call(c.hwnd, uintptr(idHotkey), uintptr(mod|modNoRepeat), uintptr(vk))
			if ret == 0 {
				err = fmt.Errorf("register hotkey: %w", callErr)
			}
		}
		select {
		case c.hotkeyReply <- err:
		default:
		}
		return 0
	case wmSystrayCallback:
		switch lParam {
		case wmLButtonUp:
			c.invoke(c.handlers.OnShowWindow)
		case wmRButtonUp:
			c.showContextMenu()
		}
		return 0
	case wmCommand:
		switch int32(wParam) {
		case idMenuQuickNote:
			c.invoke(c.handlers.OnQuickNote)
		case idMenuShow:
			c.invoke(c.handlers.OnShowWindow)
		case idMenuQuit:
			c.invoke(c.handlers.OnQuit)
		}
		return 0
	case wmHotkey:
		if int32(wParam) == idHotkey {
			c.invoke(c.handlers.OnQuickNote)
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procUnregisterHotKey.Call(c.hwnd, uintptr(idHotkey))
		c.removeTrayIcon()
		procDestroyMenu.Call(c.menu)
		procUnregisterClassW.Call(uintptr(unsafe.Pointer(c.className)), c.instance)
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func (c *Controller) invoke(handler func()) {
	if handler == nil {
		return
	}
	go handler()
}
