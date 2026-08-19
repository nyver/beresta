//go:build windows

package autostart

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// testKeyPath exercises the same logic as the real Run key against a
// disposable HKCU subkey, so these tests never read or write the actual
// per-user autostart entry on the machine running them.
const testKeyPath = `Software\Beresta\AutostartTest`

const testValueName = "Beresta"

func cleanupTestKey(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.CURRENT_USER, testKeyPath)
	})
}

func TestQueryKeyMissingKeyIsDisabled(t *testing.T) {
	cleanupTestKey(t)
	status, err := queryKey(testKeyPath, testValueName, `C:\Beresta\beresta.exe`)
	if err != nil {
		t.Fatalf("queryKey() error = %v", err)
	}
	if status.Enabled || status.ConflictPath != "" {
		t.Fatalf("queryKey() = %+v, want disabled with no conflict", status)
	}
}

func TestEnableQueryDisableRoundTrip(t *testing.T) {
	cleanupTestKey(t)
	exePath := `C:\Beresta\beresta.exe`

	if err := enableKey(testKeyPath, testValueName, exePath); err != nil {
		t.Fatalf("enableKey() error = %v", err)
	}
	status, err := queryKey(testKeyPath, testValueName, exePath)
	if err != nil {
		t.Fatalf("queryKey() error = %v", err)
	}
	if !status.Enabled {
		t.Fatalf("queryKey() = %+v, want Enabled after enableKey", status)
	}

	if err := disableKey(testKeyPath, testValueName, exePath); err != nil {
		t.Fatalf("disableKey() error = %v", err)
	}
	status, err = queryKey(testKeyPath, testValueName, exePath)
	if err != nil {
		t.Fatalf("queryKey() error = %v", err)
	}
	if status.Enabled {
		t.Fatalf("queryKey() = %+v, want disabled after disableKey", status)
	}
}

func TestQueryKeyReportsConflictForForeignPath(t *testing.T) {
	cleanupTestKey(t)
	if err := enableKey(testKeyPath, testValueName, `C:\Old\beresta.exe`); err != nil {
		t.Fatalf("enableKey() error = %v", err)
	}
	status, err := queryKey(testKeyPath, testValueName, `C:\New\beresta.exe`)
	if err != nil {
		t.Fatalf("queryKey() error = %v", err)
	}
	if status.Enabled {
		t.Fatal("queryKey() reports Enabled for a mismatched executable path")
	}
	if status.ConflictPath == "" {
		t.Fatal("queryKey() ConflictPath is empty, want the stale command line")
	}
}

// TestDisableKeyNeverDeletesAForeignEntry proves Disable is a no-op when
// the Run value exists but does not resolve to the caller's own exePath:
// deleting it would silently break someone else's autostart entry (or a
// stale entry from a different Beresta install) that Disable was never
// asked to touch.
func TestDisableKeyNeverDeletesAForeignEntry(t *testing.T) {
	cleanupTestKey(t)
	if err := enableKey(testKeyPath, testValueName, `C:\Old\beresta.exe`); err != nil {
		t.Fatalf("enableKey() error = %v", err)
	}
	if err := disableKey(testKeyPath, testValueName, `C:\New\beresta.exe`); err != nil {
		t.Fatalf("disableKey() error = %v", err)
	}
	status, err := queryKey(testKeyPath, testValueName, `C:\Old\beresta.exe`)
	if err != nil {
		t.Fatalf("queryKey() error = %v", err)
	}
	if !status.Enabled {
		t.Fatal("disableKey() removed a Run entry belonging to a different executable path")
	}
}

func TestDisableKeyOnMissingKeyIsNotAnError(t *testing.T) {
	cleanupTestKey(t)
	if err := disableKey(testKeyPath, testValueName, `C:\Beresta\beresta.exe`); err != nil {
		t.Fatalf("disableKey() on a missing key error = %v, want nil", err)
	}
}

func TestCommandPath(t *testing.T) {
	cases := map[string]string{
		`"C:\Beresta\beresta.exe" --autostart`: `C:\Beresta\beresta.exe`,
		`C:\Beresta\beresta.exe --autostart`:   `C:\Beresta\beresta.exe`,
		`C:\Beresta\beresta.exe`:               `C:\Beresta\beresta.exe`,
		`"C:\Beresta\beresta.exe"`:             `C:\Beresta\beresta.exe`,
	}
	for input, want := range cases {
		if got := commandPath(input); got != want {
			t.Fatalf("commandPath(%q) = %q, want %q", input, got, want)
		}
	}
}
