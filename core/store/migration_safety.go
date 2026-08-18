package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BackupDatabaseFile writes a complete, consistent copy of the database
// file at sourcePath to destPath: it forces a full WAL checkpoint so every
// committed write is present in the main file, then durably copies that
// file (write-temp/fsync/rename, mirroring BlobStore.Publish) so a crash
// mid-copy can never leave a partial backup at destPath. Because SQLCipher
// encrypts at the page level, the resulting file is a plain byte-for-byte
// copy that remains a valid encrypted database, openable with the same key
// — no separate export step is required.
//
// This is the safety backup Open takes before applying a schema migration
// to a pre-existing database, and the forward-fix recovery path for a
// migration later found to be wrong: restore it with RestoreDatabaseFile,
// then re-open with a corrected migration. db must be sourcePath's open
// connection, with no pending write transaction.
func BackupDatabaseFile(ctx context.Context, db *sql.DB, sourcePath, destPath string) error {
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("store: checkpoint before backup: %w", err)
	}
	return durablyCopyFile(ctx, sourcePath, destPath)
}

// RestoreDatabaseFile durably copies a previously taken backup (see
// BackupDatabaseFile) back over a database file, and removes any stale
// -wal/-shm sidecar files at destPath so a subsequent open replays nothing
// against the restored main file. Call it only while nothing holds
// destPath open.
func RestoreDatabaseFile(ctx context.Context, backupPath, destPath string) error {
	if err := durablyCopyFile(ctx, backupPath, destPath); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(destPath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("store: remove stale %s sidecar: %w", suffix, err)
		}
	}
	return nil
}

// durablyCopyFile copies sourcePath to destPath via write-temp/fsync/rename
// so a crash mid-copy leaves destPath either absent or byte-complete, never
// truncated.
func durablyCopyFile(ctx context.Context, sourcePath, destPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("store: open source file: %w", err)
	}
	defer source.Close()

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return fmt.Errorf("store: create destination directory: %w", err)
	}
	tmp, err := os.CreateTemp(destDir, "db-copy-*.tmp")
	if err != nil {
		return fmt.Errorf("store: create copy temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmp, source); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("store: copy file content: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("store: fsync copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close copy: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("store: publish copy: %w", err)
	}
	cleanup = false
	return nil
}

// RebuildFTSIndex reconstructs notes_fts's internal index structures from
// its stored title/body columns using FTS5's built-in 'rebuild' command.
// notes_fts is a standalone (non external-content) table, so this does not
// need — and store cannot itself perform, since the canonical Markdown
// projection lives above this package — access to the notes/CRDT tables it
// was originally populated from; it only repairs the FTS5 index's own
// internal structures, which is what a future migration should call after
// any schema change to notes_fts itself (e.g. a tokenizer change) or after
// suspected FTS-specific corruption.
func RebuildFTSIndex(ctx context.Context, exec Executor) error {
	if _, err := exec.ExecContext(ctx, `INSERT INTO notes_fts (notes_fts) VALUES ('rebuild')`); err != nil {
		return fmt.Errorf("store: rebuild FTS index: %w", err)
	}
	return nil
}
