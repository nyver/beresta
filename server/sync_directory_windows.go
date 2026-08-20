//go:build windows

package server

// Windows does not expose portable directory fsync semantics through os.File.
// File contents are flushed before every atomic rename, and recovery validates
// the published pair or blob before making it addressable.
func syncDirectory(string) error { return nil }
