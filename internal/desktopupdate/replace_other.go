//go:build !windows

package desktopupdate

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
