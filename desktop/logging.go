package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/beresta-app/beresta/internal/logging"
)

// configureLogging installs the default slog logger, writing structured
// JSON to stderr and to a bounded, rotated file under the per-user app
// data directory (specs/release-quality's "Layered automated verification"
// requirement: logs stay bounded and reviewable locally, never unbounded
// or uploaded anywhere - see also client-data-protection's privacy
// defaults). It never blocks startup: if the log file cannot be opened
// (for example, a read-only profile), slog keeps its stderr-only handler
// and the returned close function is a no-op.
func configureLogging() func() {
	writer := io.Writer(os.Stderr)
	closeLogging := func() {}
	if dir, err := appDataDir(); err == nil {
		if rotating, err := logging.New(filepath.Join(dir, "logs"), "beresta-desktop.log", 0, 0); err == nil {
			writer = io.MultiWriter(os.Stderr, rotating)
			closeLogging = func() { rotating.Close() }
		}
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(writer, nil)))
	return closeLogging
}
