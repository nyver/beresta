//go:build !windows

package server

import (
	"fmt"
	"os"
)

func restrictDirectory(path string) error {
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("restrict directory permissions: %w", err)
	}
	return nil
}

func restrictFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict file permissions: %w", err)
	}
	return nil
}
