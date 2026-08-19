//go:build windows

package traymenu

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestStartAndClose proves the hidden window, tray icon, and context
// menu can actually be created and torn down on this machine's real
// Win32 message loop: window-class registration, CreateWindowExW,
// Shell_NotifyIconW, and InsertMenuItemW all have exacting struct-layout
// requirements that only a real syscall round trip can catch, unlike a
// pure Go unit test. It never registers a hotkey, so it cannot collide
// with (or shadow) whatever accelerator the developer or CI machine
// already has bound elsewhere.
func TestStartAndClose(t *testing.T) {
	c, err := Start(Handlers{}, 0, 0)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if c == nil {
		t.Fatal("Start() returned a nil Controller with a nil error")
	}
	c.Close()
	// A second Close must not hang or panic.
	c.Close()
}

// TestSetHotkeyRoundTrip proves a hotkey can be registered and then
// cleared through the cross-thread PostMessage handshake in SetHotkey,
// which is the part of this package most likely to deadlock if the
// window/message-loop thread ever stops draining wmSetHotkey.
func TestSetHotkeyRoundTrip(t *testing.T) {
	var triggered int32
	c, err := Start(Handlers{
		OnQuickNote: func() { atomic.AddInt32(&triggered, 1) },
	}, 0, 0)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(c.Close)

	// Ctrl+Alt+Shift+F24 is about as unlikely as a real key combination
	// gets to already be bound by something else on the test machine.
	if err := c.SetHotkey(hotkeyModsForTest(), vkF24ForTest()); err != nil {
		t.Fatalf("SetHotkey(register) error = %v", err)
	}
	if err := c.SetHotkey(0, 0); err != nil {
		t.Fatalf("SetHotkey(clear) error = %v", err)
	}
	// Give any (unexpected) delivered WM_HOTKEY message time to reach the
	// handler; the assertion below only guards against a false trigger
	// during registration/deregistration itself; it is not how real key
	// presses are simulated (they never happen in this test).
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&triggered) != 0 {
		t.Fatal("OnQuickNote fired without a real key press")
	}
}

func hotkeyModsForTest() uint32 { return modControl | modAlt | modShift }
func vkF24ForTest() uint32      { return 0x87 } // VK_F24
