package main

import "testing"

func TestParseHotkeyValid(t *testing.T) {
	cases := []struct {
		accelerator string
		wantMod     uint32
		wantVK      uint32
	}{
		{"", 0, 0},
		{"  ", 0, 0},
		{"Ctrl+Shift+N", hotkeyModControl | hotkeyModShift, 'N'},
		{"ctrl+shift+n", hotkeyModControl | hotkeyModShift, 'N'},
		{"Alt+F5", hotkeyModAlt, 0x70 + 4},
		{"Win+Control+9", hotkeyModWin | hotkeyModControl, '9'},
		{" Ctrl + N ", hotkeyModControl, 'N'},
	}
	for _, tc := range cases {
		mod, vk, err := parseHotkey(tc.accelerator)
		if err != nil {
			t.Fatalf("parseHotkey(%q) error = %v", tc.accelerator, err)
		}
		if mod != tc.wantMod || vk != tc.wantVK {
			t.Fatalf("parseHotkey(%q) = (%#x, %#x), want (%#x, %#x)", tc.accelerator, mod, vk, tc.wantMod, tc.wantVK)
		}
	}
}

func TestParseHotkeyInvalid(t *testing.T) {
	cases := []string{
		"N",           // no modifier
		"Ctrl+",       // empty key segment
		"Ctrl+Shift",  // no key, only modifiers
		"Ctrl+N+M",    // two keys
		"Ctrl+Escape", // unsupported key name
		"Ctrl+F13",    // out of supported F-key range
		"Ctrl+F0",     // out of supported F-key range
		"Ctrl+AB",     // multi-character non-function token
	}
	for _, accelerator := range cases {
		if _, _, err := parseHotkey(accelerator); err == nil {
			t.Fatalf("parseHotkey(%q) error = nil, want an error", accelerator)
		}
	}
}

func TestDefaultQuickNoteHotkeyParses(t *testing.T) {
	mod, vk, err := parseHotkey(DefaultQuickNoteHotkey)
	if err != nil {
		t.Fatalf("parseHotkey(DefaultQuickNoteHotkey) error = %v", err)
	}
	if mod == 0 || vk == 0 {
		t.Fatalf("parseHotkey(DefaultQuickNoteHotkey) = (%#x, %#x), want a real hotkey", mod, vk)
	}
}
