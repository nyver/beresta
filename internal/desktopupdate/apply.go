package desktopupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const rollbackSuffix = ".previous"

// InstallerRunner runs an already verified installer and waits for its exit.
type InstallerRunner interface {
	Run(context.Context, string, ...string) error
}

// InstallerRunnerFunc adapts a function to InstallerRunner.
type InstallerRunnerFunc func(context.Context, string, ...string) error

func (f InstallerRunnerFunc) Run(ctx context.Context, path string, args ...string) error {
	return f(ctx, path, args...)
}

// Apply verifies the staged installer, preserves the current executable, and
// restores it if the installer fails or does not leave a runnable file.
func Apply(ctx context.Context, manifest Manifest, artifactPath, installedPath, currentVersion string, publicKey ed25519.PublicKey, publisher PublisherVerifier, runner InstallerRunner) error {
	if err := Verify(ctx, manifest, artifactPath, currentVersion, publicKey, publisher); err != nil {
		return err
	}
	if runner == nil {
		return errors.New("desktop update: installer runner is not configured")
	}
	if err := validateInstalledPath(installedPath); err != nil {
		return err
	}
	rollbackPath := installedPath + rollbackSuffix
	if err := atomicCopy(installedPath, rollbackPath); err != nil {
		return fmt.Errorf("desktop update: preserve prior executable: %w", err)
	}
	if err := runner.Run(ctx, artifactPath, "/S", "/UPDATE", "/D="+filepath.Dir(installedPath)); err != nil {
		if restoreErr := atomicCopy(rollbackPath, installedPath); restoreErr != nil {
			return fmt.Errorf("desktop update: installer failed: %v; rollback failed: %w", err, restoreErr)
		}
		return fmt.Errorf("desktop update: installer failed and prior version was restored: %w", err)
	}
	if err := validateInstalledPath(installedPath); err != nil {
		if restoreErr := atomicCopy(rollbackPath, installedPath); restoreErr != nil {
			return fmt.Errorf("desktop update: installed executable validation failed: %v; rollback failed: %w", err, restoreErr)
		}
		return fmt.Errorf("desktop update: installed executable validation failed and prior version was restored: %w", err)
	}
	unchanged, err := sameFileContents(installedPath, rollbackPath)
	if err != nil {
		if restoreErr := atomicCopy(rollbackPath, installedPath); restoreErr != nil {
			return fmt.Errorf("desktop update: compare installed executable: %v; rollback failed: %w", err, restoreErr)
		}
		return fmt.Errorf("desktop update: compare installed executable and prior version was restored: %w", err)
	}
	if unchanged {
		return errors.New("desktop update: installer completed without replacing the installed executable")
	}
	if err := publisher.VerifyPublisher(ctx, installedPath); err != nil {
		if restoreErr := atomicCopy(rollbackPath, installedPath); restoreErr != nil {
			return fmt.Errorf("desktop update: installed publisher verification failed: %v; rollback failed: %w", err, restoreErr)
		}
		return fmt.Errorf("desktop update: installed publisher verification failed and prior version was restored: %w", err)
	}
	return nil
}

func sameFileContents(leftPath, rightPath string) (bool, error) {
	left, err := os.Open(leftPath)
	if err != nil {
		return false, err
	}
	defer left.Close()
	right, err := os.Open(rightPath)
	if err != nil {
		return false, err
	}
	defer right.Close()

	leftInfo, err := left.Stat()
	if err != nil {
		return false, err
	}
	rightInfo, err := right.Stat()
	if err != nil {
		return false, err
	}
	if leftInfo.Size() != rightInfo.Size() {
		return false, nil
	}
	leftHash := sha256.New()
	if _, err := io.Copy(leftHash, left); err != nil {
		return false, err
	}
	rightHash := sha256.New()
	if _, err := io.Copy(rightHash, right); err != nil {
		return false, err
	}
	return bytes.Equal(leftHash.Sum(nil), rightHash.Sum(nil)), nil
}

// Rollback atomically restores the last executable preserved by Apply.
func Rollback(installedPath string) error {
	if err := validateInstalledPath(installedPath + rollbackSuffix); err != nil {
		return fmt.Errorf("desktop update: rollback version unavailable: %w", err)
	}
	if err := atomicCopy(installedPath+rollbackSuffix, installedPath); err != nil {
		return fmt.Errorf("desktop update: restore prior executable: %w", err)
	}
	return nil
}

func validateInstalledPath(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("desktop update: inspect installed executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("desktop update: installed executable is not a regular file")
	}
	return nil
}

func atomicCopy(source, destination string) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	info, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("source is not a regular file")
	}
	dir := filepath.Dir(destination)
	tmp, err := os.CreateTemp(dir, ".beresta-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
		}
	}()
	if _, err := io.Copy(tmp, sourceFile); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	ok = true
	if err := replaceFile(tmpPath, destination); err != nil {
		return err
	}
	return syncDirectory(dir)
}
