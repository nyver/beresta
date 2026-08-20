//go:build !windows

package server

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type serverDataLock struct {
	file *os.File
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
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("server data directory is already in use: %w", err)
	}
	return &serverDataLock{file: file}, nil
}

func (l *serverDataLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
