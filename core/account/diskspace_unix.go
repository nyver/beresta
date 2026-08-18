//go:build !windows

package account

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// freeBytesAt reports the free space available to the current user at path
// on POSIX targets (Linux/Android), via statfs.
func freeBytesAt(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("account: query free disk space: %w", err)
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
