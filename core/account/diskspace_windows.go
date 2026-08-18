//go:build windows

package account

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// freeBytesAt reports the free space available to the current user at path
// on Windows, via GetDiskFreeSpaceEx.
func freeBytesAt(path string) (uint64, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("account: encode backup destination path: %w", err)
	}
	var freeToCaller, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeToCaller, &total, &totalFree); err != nil {
		return 0, fmt.Errorf("account: query free disk space: %w", err)
	}
	return freeToCaller, nil
}
