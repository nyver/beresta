// Package logging provides a small, dependency-free size-bounded rotating
// file writer for desktop and server structured logs (specs/release-quality's
// "Layered automated verification" and specs/product-experience's "Layered
// privacy-preserving diagnostics" requirements: logs stay bounded, retained,
// and reviewable, never an unbounded or externally-uploaded stream).
package logging

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// DefaultMaxSizeBytes bounds one log file before rotation when a
	// caller does not specify one.
	DefaultMaxSizeBytes int64 = 10 << 20 // 10 MiB
	// DefaultMaxBackups bounds how many rotated files are retained
	// (beyond the currently active one) when a caller does not specify
	// one.
	DefaultMaxBackups = 5
)

// RotatingWriter is an io.WriteCloser that appends to a single active file
// under Directory named Name, rotating it to Name.1 (shifting Name.1 to
// Name.2, and so on) once it would exceed MaxSizeBytes, and deleting the
// oldest backup once more than MaxBackups accumulate. It never truncates or
// drops a write to stay under the bound - the bound is enforced only at
// rotation boundaries - and every file it creates is owner-only (0600),
// matching every other sensitive file this project writes.
type RotatingWriter struct {
	mu         sync.Mutex
	directory  string
	name       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
	closed     bool
}

// New opens (creating if necessary) directory/name for append, using its
// current size as the starting point so a restarted process continues the
// same file instead of silently starting a new one every launch.
// maxSizeBytes and maxBackups fall back to DefaultMaxSizeBytes and
// DefaultMaxBackups when zero.
func New(directory, name string, maxSizeBytes int64, maxBackups int) (*RotatingWriter, error) {
	if directory == "" || name == "" {
		return nil, errors.New("logging: directory and name are required")
	}
	if maxSizeBytes <= 0 {
		maxSizeBytes = DefaultMaxSizeBytes
	}
	if maxBackups <= 0 {
		maxBackups = DefaultMaxBackups
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("logging: create log directory: %w", err)
	}
	w := &RotatingWriter{directory: directory, name: name, maxBytes: maxSizeBytes, maxBackups: maxBackups}
	if err := w.openCurrent(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) path(suffix string) string {
	if suffix == "" {
		return filepath.Join(w.directory, w.name)
	}
	return filepath.Join(w.directory, w.name+suffix)
}

func (w *RotatingWriter) openCurrent() error {
	file, err := os.OpenFile(w.path(""), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("logging: open log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("logging: stat log file: %w", err)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

// Write appends p to the active file, rotating first if p would push the
// active file past MaxSizeBytes. A single write larger than MaxSizeBytes
// on its own is still written whole to a freshly rotated file rather than
// being split or rejected.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errors.New("logging: write to closed RotatingWriter")
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate must be called with mu held. It closes the active file, shifts
// every numbered backup up by one (dropping the oldest beyond
// MaxBackups), and opens a fresh, empty active file - so a reader tailing
// the active path never observes a gap: the file always exists, and any
// single Write is atomic with respect to rotation because both happen
// under mu.
func (w *RotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("logging: close log file before rotation: %w", err)
	}
	if err := os.Remove(w.path(fmt.Sprintf(".%d", w.maxBackups))); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("logging: prune oldest rotated log: %w", err)
	}
	for i := w.maxBackups - 1; i >= 1; i-- {
		oldPath, newPath := w.path(fmt.Sprintf(".%d", i)), w.path(fmt.Sprintf(".%d", i+1))
		if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("logging: shift rotated log: %w", err)
		}
	}
	if err := os.Rename(w.path(""), w.path(".1")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("logging: rotate active log: %w", err)
	}
	return w.openCurrent()
}

// Close closes the active file. Rotated backups are left in place.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.file.Close()
}
