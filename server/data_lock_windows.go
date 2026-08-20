//go:build windows

package server

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type serverDataLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireDataLock(dataRoot string) (*serverDataLock, error) {
	path := filepath.Join(dataRoot, ".server.lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open server data lock: %w", err)
	}
	if err := restrictFile(path); err != nil {
		file.Close()
		return nil, err
	}
	lock := &serverDataLock{file: file}
	err = windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("server data directory is already in use: %w", err)
	}
	return lock, nil
}

func (l *serverDataLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &l.overlapped)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
