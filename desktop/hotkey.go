package main

import (
	"fmt"
	"strings"
)

// Win32 RegisterHotKey modifier bits (see
// desktop/platform/traymenu/traymenu_windows.go), duplicated here so
// accelerator parsing stays pure Go and unit-testable without a
// windows-only build of the raw syscalls that actually register a
// hotkey.
const (
	hotkeyModAlt     = 0x0001
	hotkeyModControl = 0x0002
	hotkeyModShift   = 0x0004
	hotkeyModWin     = 0x0008
)

// DefaultQuickNoteHotkey is offered on first run and whenever the user
// clears their configured accelerator.
const DefaultQuickNoteHotkey = "Ctrl+Shift+N"

// parseHotkey turns an accelerator string like "Ctrl+Shift+N" into a
// Win32 modifier mask and virtual-key code. An empty accelerator is
// valid and means "no global hotkey" (mod == 0 && vk == 0); every other
// invalid form is rejected so a typo never silently registers the wrong
// key combination.
func parseHotkey(accelerator string) (mod uint32, vk uint32, err error) {
	accelerator = strings.TrimSpace(accelerator)
	if accelerator == "" {
		return 0, 0, nil
	}

	var keyToken string
	for _, raw := range strings.Split(accelerator, "+") {
		token := strings.TrimSpace(raw)
		if token == "" {
			return 0, 0, fmt.Errorf("invalid hotkey %q: empty segment", accelerator)
		}
		switch strings.ToLower(token) {
		case "ctrl", "control":
			mod |= hotkeyModControl
		case "alt":
			mod |= hotkeyModAlt
		case "shift":
			mod |= hotkeyModShift
		case "win", "super", "windows":
			mod |= hotkeyModWin
		default:
			if keyToken != "" {
				return 0, 0, fmt.Errorf("invalid hotkey %q: more than one key", accelerator)
			}
			keyToken = token
		}
	}
	if keyToken == "" {
		return 0, 0, fmt.Errorf("invalid hotkey %q: missing key", accelerator)
	}
	if mod == 0 {
		return 0, 0, fmt.Errorf("invalid hotkey %q: at least one modifier (Ctrl/Alt/Shift/Win) is required", accelerator)
	}
	vk, err = virtualKey(keyToken)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid hotkey %q: %w", accelerator, err)
	}
	return mod, vk, nil
}

// virtualKey maps a single key token to its Win32 virtual-key code.
// Letters, digits, and F1-F12 cover every combination worth offering as
// a quick-note accelerator; VK_F1 is 0x70 and the function keys are
// contiguous from there.
func virtualKey(token string) (uint32, error) {
	upper := strings.ToUpper(token)
	switch {
	case len(upper) == 1 && upper[0] >= 'A' && upper[0] <= 'Z':
		return uint32(upper[0]), nil
	case len(upper) == 1 && upper[0] >= '0' && upper[0] <= '9':
		return uint32(upper[0]), nil
	case len(upper) >= 2 && len(upper) <= 3 && upper[0] == 'F':
		n := 0
		if _, err := fmt.Sscanf(upper[1:], "%d", &n); err != nil || n < 1 || n > 12 {
			return 0, fmt.Errorf("unsupported key %q", token)
		}
		return uint32(0x6F + n), nil
	default:
		return 0, fmt.Errorf("unsupported key %q", token)
	}
}
