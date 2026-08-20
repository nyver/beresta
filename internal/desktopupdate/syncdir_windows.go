//go:build windows

package desktopupdate

// replaceFile uses MOVEFILE_WRITE_THROUGH on Windows. Directory handles do
// not support os.File.Sync there, so no second directory flush is available.
func syncDirectory(string) error {
	return nil
}
