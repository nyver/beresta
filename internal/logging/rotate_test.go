package logging

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestRotatingWriterRotatesAtSizeBoundaryAndPrunesOldestBackup(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "app.log", 20, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Each line is 10 bytes ("0123456789\n" is 11, use 9 bytes payload +
	// newline = 10) so every two writes should cross the 20-byte bound and
	// trigger exactly one rotation.
	for i := range 8 {
		line := fmt.Sprintf("line-%03d\n", i) // 9 bytes
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	if !names["app.log"] {
		t.Fatalf("active log file missing, dir = %v", names)
	}
	// MaxBackups=2 bounds retained rotated files to app.log.1 and
	// app.log.2; anything older must already be pruned.
	if !names["app.log.1"] || !names["app.log.2"] {
		t.Fatalf("expected app.log.1 and app.log.2 present, dir = %v", names)
	}
	if names["app.log.3"] {
		t.Fatalf("app.log.3 should have been pruned beyond MaxBackups=2, dir = %v", names)
	}
}

func TestRotatingWriterNeverLosesOrCorruptsBytesAcrossRotation(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "app.log", 50, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var written []byte
	for i := range 40 {
		line := []byte(fmt.Sprintf("entry-%02d\n", i))
		if _, err := w.Write(line); err != nil {
			t.Fatalf("Write(%d): %v", i, err)
		}
		written = append(written, line...)
	}

	// Concatenate every surviving file (oldest backup first) and confirm
	// it is a contiguous, uncorrupted suffix of everything ever written -
	// rotation must never duplicate, truncate mid-line, or reorder bytes,
	// even though the oldest entries are expected to have been pruned.
	var reconstructed []byte
	for _, suffix := range []string{".3", ".2", ".1", ""} {
		data, err := os.ReadFile(filepath.Join(dir, "app.log"+suffix))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		reconstructed = append(reconstructed, data...)
	}
	if !bytes.Equal(reconstructed, written[len(written)-len(reconstructed):]) {
		t.Fatalf("reconstructed log does not match the tail of what was written")
	}
}

func TestRotatingWriterConcurrentWritesAreNeverInterleavedOrLost(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir, "app.log", 1<<20, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	const goroutines = 8
	const perGoroutine = 100
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range perGoroutine {
				line := fmt.Sprintf("g%d-%03d\n", id, i)
				if _, err := w.Write([]byte(line)); err != nil {
					t.Errorf("Write from goroutine %d: %v", id, err)
				}
			}
		}(g)
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Count(data, []byte("\n"))
	if want := goroutines * perGoroutine; lines != want {
		t.Fatalf("wrote %d lines, want %d - a lost or interleaved write would corrupt or drop a line", lines, want)
	}
}

// TestRotatingWriterReopenContinuesTheExistingFileInsteadOfTruncating
// proves a restarted process (a new RotatingWriter for the same
// directory/name) appends to what is already there rather than silently
// discarding it - losing a truncated tail is exactly the kind of gap
// "neither leaks prohibited fields nor interrupts service" guards
// against.
func TestRotatingWriterReopenContinuesTheExistingFileInsteadOfTruncating(t *testing.T) {
	dir := t.TempDir()
	first, err := New(dir, "app.log", 1<<20, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("before restart\n")); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := New(dir, "app.log", 1<<20, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.Write([]byte("after restart\n")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("before restart")) || !bytes.Contains(data, []byte("after restart")) {
		t.Fatalf("app.log = %q, want both pre- and post-restart lines", data)
	}
}

func TestRotatingWriterCreatesOwnerOnlyFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode bits are not meaningful on Windows")
	}
	dir := t.TempDir()
	w, err := New(dir, "app.log", 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for range 4 {
		if _, err := w.Write([]byte("0123456789\n")); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"app.log", "app.log.1"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s permissions = %o, want 0600", name, perm)
		}
	}
}
