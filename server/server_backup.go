package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const serverBackupFormat = "beresta.server-backup.v1"

type ServerBackupFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode"`
}

type ServerBackupManifest struct {
	Format    string             `json:"format"`
	Kind      string             `json:"kind"`
	CreatedAt time.Time          `json:"created_at"`
	Files     []ServerBackupFile `json:"files"`
}

type ServerBackup struct {
	Path     string               `json:"path"`
	Manifest ServerBackupManifest `json:"manifest"`
}

func (s *Storage) CreateServerBackup(ctx context.Context, destination, kind string, now time.Time) (ServerBackup, error) {
	if kind != "daily" && kind != "manual" && kind != "pre-restore" {
		return ServerBackup{}, fmt.Errorf("%w: invalid server backup kind", ErrInvalid)
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return ServerBackup{}, err
	}
	liveBlobs := filepath.Join(s.dataRoot, "blobs")
	if relative, err := filepath.Rel(liveBlobs, destination); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ServerBackup{}, fmt.Errorf("%w: backup destination must not be inside the live blob directory", ErrInvalid)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return ServerBackup{}, err
	}
	if err := restrictDirectory(destination); err != nil {
		return ServerBackup{}, err
	}
	id, err := newID()
	if err != nil {
		return ServerBackup{}, err
	}
	name := "server-" + now.UTC().Format("20060102T150405Z") + "-" + id
	staging, err := os.MkdirTemp(destination, ".backup-")
	if err != nil {
		return ServerBackup{}, err
	}
	defer os.RemoveAll(staging)
	if err := restrictDirectory(staging); err != nil {
		return ServerBackup{}, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(FULL)"); err != nil {
		return ServerBackup{}, fmt.Errorf("checkpoint server database: %w", err)
	}
	databaseBackup := filepath.Join(staging, "beresta.db")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO "+quoteSQLiteString(databaseBackup)); err != nil {
		return ServerBackup{}, fmt.Errorf("snapshot server database: %w", err)
	}
	if err := restrictFile(databaseBackup); err != nil {
		return ServerBackup{}, err
	}

	manifest := ServerBackupManifest{Format: serverBackupFormat, Kind: kind, CreatedAt: now.UTC()}
	databaseEntry, err := backupFileEntry(staging, databaseBackup, "snapshot")
	if err != nil {
		return ServerBackup{}, err
	}
	manifest.Files = append(manifest.Files, databaseEntry)
	err = filepath.WalkDir(liveBlobs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(liveBlobs, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative == ".staging" || relative == ".trash" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("blob backup source contains non-regular file %q", relative)
		}
		target := filepath.Join(staging, "blobs", relative)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		mode := "hardlink"
		if err := os.Link(path, target); err != nil {
			mode = "copy"
			if err := copyFileVerified(path, target); err != nil {
				return err
			}
		}
		item, err := backupFileEntry(staging, target, mode)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, item)
		return nil
	})
	if err != nil {
		return ServerBackup{}, fmt.Errorf("snapshot server blobs: %w", err)
	}
	sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ServerBackup{}, err
	}
	if err := writeSyncedFile(filepath.Join(staging, "manifest.json"), append(manifestBytes, '\n'), 0o600); err != nil {
		return ServerBackup{}, err
	}
	final := filepath.Join(destination, name)
	if err := os.Rename(staging, final); err != nil {
		return ServerBackup{}, fmt.Errorf("publish server backup: %w", err)
	}
	if err := syncDirectory(destination); err != nil {
		return ServerBackup{}, fmt.Errorf("sync published server backup: %w", err)
	}
	backup, err := VerifyServerBackup(final)
	if err != nil {
		return ServerBackup{}, err
	}
	if kind == "daily" {
		if err := rotateServerBackups(destination, s.config.Backups.KeepDaily); err != nil {
			return ServerBackup{}, err
		}
	}
	return backup, nil
}

func VerifyServerBackup(path string) (ServerBackup, error) {
	root, err := filepath.Abs(path)
	if err != nil {
		return ServerBackup{}, err
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return ServerBackup{}, errors.New("server backup root must be a real directory")
	}
	manifestInfo, err := os.Lstat(filepath.Join(root, "manifest.json"))
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return ServerBackup{}, errors.New("server backup manifest must be a regular file")
	}
	manifest, err := loadServerBackupManifest(root)
	if err != nil {
		return ServerBackup{}, err
	}
	seen := make(map[string]bool, len(manifest.Files))
	for _, item := range manifest.Files {
		clean := filepath.Clean(filepath.FromSlash(item.Path))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || seen[clean] {
			return ServerBackup{}, errors.New("server backup manifest contains an unsafe or duplicate path")
		}
		if clean != "beresta.db" && !strings.HasPrefix(clean, "blobs"+string(filepath.Separator)) {
			return ServerBackup{}, errors.New("server backup manifest contains an unsupported path")
		}
		seen[clean] = true
		if (item.Mode != "snapshot" && item.Mode != "hardlink" && item.Mode != "copy") || len(item.SHA256) != sha256.Size*2 || item.SHA256 != strings.ToLower(item.SHA256) {
			return ServerBackup{}, errors.New("server backup manifest contains invalid file metadata")
		}
		full := filepath.Join(root, clean)
		if err := verifyBackupPathComponents(root, clean); err != nil {
			return ServerBackup{}, err
		}
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Size() != item.Bytes {
			return ServerBackup{}, fmt.Errorf("server backup file %q is missing or has the wrong size", item.Path)
		}
		digest, err := hashFile(full)
		if err != nil || hex.EncodeToString(digest) != item.SHA256 {
			return ServerBackup{}, fmt.Errorf("server backup file %q failed SHA-256 verification", item.Path)
		}
	}
	if !seen["beresta.db"] {
		return ServerBackup{}, errors.New("server backup does not contain beresta.db")
	}
	backupDatabase, err := sql.Open("sqlite", filepath.Join(root, "beresta.db"))
	if err != nil {
		return ServerBackup{}, fmt.Errorf("open server backup database: %w", err)
	}
	defer backupDatabase.Close()
	backupDatabase.SetMaxOpenConns(1)
	if _, err := backupDatabase.Exec("PRAGMA query_only = ON"); err != nil {
		return ServerBackup{}, fmt.Errorf("protect server backup database: %w", err)
	}
	var integrity string
	if err := backupDatabase.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil {
		return ServerBackup{}, fmt.Errorf("check server backup database integrity: %w", err)
	}
	if integrity != "ok" {
		return ServerBackup{}, fmt.Errorf("server backup database integrity check failed: %s", integrity)
	}
	foreignRows, err := backupDatabase.Query("PRAGMA foreign_key_check")
	if err != nil {
		return ServerBackup{}, fmt.Errorf("check server backup foreign keys: %w", err)
	}
	hasForeignKeyViolation := foreignRows.Next()
	foreignErr := foreignRows.Err()
	foreignRows.Close()
	if foreignErr != nil {
		return ServerBackup{}, fmt.Errorf("check server backup foreign keys: %w", foreignErr)
	}
	if hasForeignKeyViolation {
		return ServerBackup{}, errors.New("server backup database contains a foreign-key violation")
	}
	backupStorage := NewStorage(backupDatabase, root, DefaultConfig(), "")
	if err := backupStorage.VerifyServerState(context.Background()); err != nil {
		return ServerBackup{}, fmt.Errorf("verify server backup state: %w", err)
	}
	return ServerBackup{Path: root, Manifest: manifest}, nil
}

func loadServerBackupManifest(root string) (ServerBackupManifest, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return ServerBackupManifest{}, fmt.Errorf("read server backup manifest: %w", err)
	}
	var manifest ServerBackupManifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.Format != serverBackupFormat || len(manifest.Files) == 0 {
		return ServerBackupManifest{}, errors.New("invalid server backup manifest")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ServerBackupManifest{}, errors.New("server backup manifest contains trailing data")
	}
	if (manifest.Kind != "daily" && manifest.Kind != "manual" && manifest.Kind != "pre-restore") || manifest.CreatedAt.IsZero() {
		return ServerBackupManifest{}, errors.New("server backup manifest contains invalid metadata")
	}
	return manifest, nil
}

func verifyBackupPathComponents(root, relative string) error {
	components := strings.Split(relative, string(filepath.Separator))
	current := root
	for _, component := range components[:len(components)-1] {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("server backup path %q contains an invalid directory", relative)
		}
	}
	return nil
}

func RestoreServerBackup(dataRoot, backupPath string) error {
	resolvedRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return err
	}
	lock, err := acquireDataLock(resolvedRoot)
	if err != nil {
		return err
	}
	restoreErr := restoreServerBackupLocked(resolvedRoot, backupPath)
	lockErr := lock.Close()
	if restoreErr != nil {
		return restoreErr
	}
	return lockErr
}

// RestoreServerBackup closes the runtime database while retaining the
// exclusive data-root lock across the complete restore swap.
func (r *Runtime) RestoreServerBackup(backupPath string) error {
	if r == nil || r.dataLock == nil {
		return errors.New("server runtime does not hold the data-directory lock")
	}
	if r.Database != nil {
		if err := r.Database.Close(); err != nil {
			return err
		}
		r.Database = nil
	}
	return restoreServerBackupLocked(r.DataDirectory, backupPath)
}

func restoreServerBackupLocked(dataRoot, backupPath string) error {
	backup, err := VerifyServerBackup(backupPath)
	if err != nil {
		return err
	}
	dataRoot, err = filepath.Abs(dataRoot)
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp(dataRoot, ".restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := restrictDirectory(staging); err != nil {
		return err
	}
	for _, item := range backup.Manifest.Files {
		clean := filepath.Clean(filepath.FromSlash(item.Path))
		target := filepath.Join(staging, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := copyFileVerified(filepath.Join(backup.Path, clean), target); err != nil {
			return err
		}
	}
	liveDatabase := filepath.Join(dataRoot, "beresta.db")
	liveBlobs := filepath.Join(dataRoot, "blobs")
	rollbackID, err := newID()
	if err != nil {
		return err
	}
	rollbackDatabase := filepath.Join(dataRoot, ".beresta.db.rollback-"+rollbackID)
	rollbackBlobs := filepath.Join(dataRoot, ".blobs.rollback-"+rollbackID)
	hadDatabase, err := regularFileExists(liveDatabase)
	if err != nil {
		return err
	}
	hadBlobs, err := directoryExists(liveBlobs)
	if err != nil {
		return err
	}
	if hadDatabase {
		if err := os.Rename(liveDatabase, rollbackDatabase); err != nil {
			return fmt.Errorf("stage current server database for rollback: %w", err)
		}
	}
	restoreComplete := false
	blobsMoved := false
	defer func() {
		if !restoreComplete {
			os.Remove(liveDatabase)
			if hadDatabase {
				os.Rename(rollbackDatabase, liveDatabase)
			}
			if blobsMoved {
				os.RemoveAll(liveBlobs)
				if hadBlobs {
					os.Rename(rollbackBlobs, liveBlobs)
				}
			}
		}
	}()
	if hadBlobs {
		if err := os.Rename(liveBlobs, rollbackBlobs); err != nil {
			return fmt.Errorf("stage current server blobs for rollback: %w", err)
		}
	}
	blobsMoved = true
	if err := os.Rename(filepath.Join(staging, "beresta.db"), liveDatabase); err != nil {
		return fmt.Errorf("publish restored server database: %w", err)
	}
	stagedBlobs := filepath.Join(staging, "blobs")
	if _, err := os.Stat(stagedBlobs); errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(stagedBlobs, 0o700); err != nil {
			return err
		}
	}
	if err := os.Rename(stagedBlobs, liveBlobs); err != nil {
		return fmt.Errorf("publish restored server blobs: %w", err)
	}
	restoreComplete = true
	if hadDatabase {
		if err := os.Remove(rollbackDatabase); err != nil {
			return fmt.Errorf("remove restored database rollback copy: %w", err)
		}
	}
	if hadBlobs {
		if err := os.RemoveAll(rollbackBlobs); err != nil {
			return fmt.Errorf("remove restored blob rollback copy: %w", err)
		}
	}
	if err := syncDirectory(dataRoot); err != nil {
		return fmt.Errorf("sync restored server state: %w", err)
	}
	return nil
}

func directoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("restore target %q is not a directory", path)
	}
	return true, nil
}

func (s *Storage) VerifyServerState(ctx context.Context) error {
	var integrity string
	if err := s.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return fmt.Errorf("check server database integrity: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("server database integrity check failed: %s", integrity)
	}
	foreignRows, err := s.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("check server foreign keys: %w", err)
	}
	hasForeignKeyViolation := foreignRows.Next()
	foreignErr := foreignRows.Err()
	foreignRows.Close()
	if foreignErr != nil {
		return fmt.Errorf("check server foreign keys: %w", foreignErr)
	}
	if hasForeignKeyViolation {
		return errors.New("server database contains a foreign-key violation")
	}
	var inconsistentReferences int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM blobs b
		WHERE b.reference_count != (
			SELECT count(*) FROM blob_references r
			WHERE r.workspace_id = b.workspace_id AND r.blob_id = b.blob_id
		)`).Scan(&inconsistentReferences); err != nil {
		return err
	}
	if inconsistentReferences != 0 {
		return fmt.Errorf("server blob reference accounting is inconsistent for %d blobs", inconsistentReferences)
	}
	var inconsistentUsers int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM users u
		WHERE u.used_bytes != COALESCE((SELECT sum(total_bytes) FROM blobs b WHERE b.owner_user_id = u.user_id AND b.state = 'complete'), 0)
		   OR u.reserved_bytes != COALESCE((SELECT sum(reserved_bytes) FROM blobs b WHERE b.owner_user_id = u.user_id AND b.state = 'staging'), 0)`).
		Scan(&inconsistentUsers); err != nil {
		return err
	}
	if inconsistentUsers != 0 {
		return fmt.Errorf("server user quota accounting is inconsistent for %d users", inconsistentUsers)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.workspace_id, c.blob_id, c.chunk_index, c.expected_bytes, c.expected_hash
		FROM blob_chunks c JOIN blobs b USING(workspace_id, blob_id) WHERE b.state = 'complete'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var workspaceID, blobID string
		var index int
		var size int64
		var digest []byte
		if err := rows.Scan(&workspaceID, &blobID, &index, &size, &digest); err != nil {
			return err
		}
		if err := s.verifyChunkFile(s.finalChunkPath(workspaceID, blobID, index), size, digest); err != nil {
			return fmt.Errorf("verify blob %s chunk %d: %w", blobID, index, err)
		}
	}
	return rows.Err()
}

func rotateServerBackups(destination string, keep int) error {
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	var valid []ServerBackup
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		backup, err := VerifyServerBackup(filepath.Join(destination, entry.Name()))
		if err == nil && backup.Manifest.Kind == "daily" {
			valid = append(valid, backup)
		}
	}
	sort.Slice(valid, func(left, right int) bool {
		return valid[left].Manifest.CreatedAt.After(valid[right].Manifest.CreatedAt)
	})
	if len(valid) <= keep {
		return nil
	}
	for _, backup := range valid[keep:] {
		if err := os.RemoveAll(backup.Path); err != nil {
			return err
		}
	}
	return nil
}

func backupFileEntry(root, path, mode string) (ServerBackupFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return ServerBackupFile{}, err
	}
	digest, err := hashFile(path)
	if err != nil {
		return ServerBackupFile{}, err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return ServerBackupFile{}, err
	}
	return ServerBackupFile{Path: filepath.ToSlash(relative), Bytes: info.Size(), SHA256: hex.EncodeToString(digest), Mode: mode}, nil
}

func hashFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

func copyFileVerified(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		output.Close()
		if remove {
			os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	sourceHash, err := hashFile(source)
	if err != nil {
		return err
	}
	targetHash, err := hashFile(target)
	if err != nil {
		return err
	}
	if !equalBytes(sourceHash, targetHash) {
		return errors.New("copied server backup file failed verification")
	}
	remove = false
	return nil
}

func quoteSQLiteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func backupDestination(cfg Config, dataRoot string) string {
	if filepath.IsAbs(cfg.Backups.Directory) {
		return cfg.Backups.Directory
	}
	if filepath.Clean(cfg.Backups.Directory) == filepath.Clean(defaultDataDirectory+string(filepath.Separator)+"backups") {
		return filepath.Join(dataRoot, "backups")
	}
	return filepath.Clean(cfg.Backups.Directory)
}

// ServerBackupDestination resolves the configured destination relative to the
// active data root when the default path is in use.
func (s *Storage) ServerBackupDestination() string {
	return backupDestination(s.config, s.dataRoot)
}

func databaseHasTodayBackup(destination string, now time.Time) bool {
	entries, err := os.ReadDir(destination)
	if err != nil {
		return false
	}
	year, month, day := now.Local().Date()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(destination, entry.Name())
		manifest, err := loadServerBackupManifest(path)
		if err != nil || manifest.Kind != "daily" {
			continue
		}
		backupYear, backupMonth, backupDay := manifest.CreatedAt.Local().Date()
		if year == backupYear && month == backupMonth && day == backupDay {
			if _, err := VerifyServerBackup(path); err == nil {
				return true
			}
		}
	}
	return false
}

func (s *Storage) EnsureDailyServerBackup(ctx context.Context, now time.Time) (bool, error) {
	destination := backupDestination(s.config, s.dataRoot)
	if databaseHasTodayBackup(destination, now) {
		return false, nil
	}
	_, err := s.CreateServerBackup(ctx, destination, "daily", now)
	return err == nil, err
}
