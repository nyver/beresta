package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// Backup kinds for the backups.kind column.
const (
	BackupKindDaily        = 1
	BackupKindPreMigration = 2
	BackupKindPreRestore   = 3
	BackupKindManual       = 4
)

// Backup is one entry in the local backup catalog: bookkeeping for a backup
// set durably published under Location, not the backup content itself.
type Backup struct {
	ID             model.ID
	Kind           int
	Location       string
	ManifestHash   []byte
	VerifiedUnixMS *int64
	NoteCount      *int64
	SizeBytes      *int64
	CreatedUnixMS  int64
	// Corrupt is set once manifest verification has found this backup
	// archive damaged. A corrupt backup is kept (not deleted) but excluded
	// from restore eligibility and from the seven-valid-day rotation count.
	Corrupt bool
}

// InsertBackup records one completed backup in the catalog.
func InsertBackup(ctx context.Context, exec Executor, b Backup) error {
	if _, err := exec.ExecContext(ctx,
		`INSERT INTO backups (id, kind, location, manifest_hash, verified_unix_ms, note_count, size_bytes, created_unix_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID.Bytes(), b.Kind, b.Location, b.ManifestHash, b.VerifiedUnixMS, b.NoteCount, b.SizeBytes, b.CreatedUnixMS,
	); err != nil {
		return fmt.Errorf("store: insert backup: %w", err)
	}
	return nil
}

// ListBackups returns every catalog entry of one kind, newest first,
// including corrupt ones (so a catalog UI can still show that they exist).
// Callers deciding rotation counts or restore eligibility must use
// ListValidBackups instead.
func ListBackups(ctx context.Context, exec Executor, kind int) ([]Backup, error) {
	return queryBackups(ctx, exec, `SELECT id, kind, location, manifest_hash, verified_unix_ms, note_count, size_bytes, created_unix_ms, corrupt
		 FROM backups WHERE kind = ? ORDER BY created_unix_ms DESC, id DESC`, kind)
}

// ListValidBackups returns every non-corrupt catalog entry of one kind,
// newest first. The seven-valid-day rotation count and restore eligibility
// (specs/backup-and-recovery.md, "Backup integrity classification") both
// use this, not ListBackups.
func ListValidBackups(ctx context.Context, exec Executor, kind int) ([]Backup, error) {
	return queryBackups(ctx, exec, `SELECT id, kind, location, manifest_hash, verified_unix_ms, note_count, size_bytes, created_unix_ms, corrupt
		 FROM backups WHERE kind = ? AND corrupt = 0 ORDER BY created_unix_ms DESC, id DESC`, kind)
}

func queryBackups(ctx context.Context, exec Executor, query string, kind int) ([]Backup, error) {
	rows, err := exec.QueryContext(ctx, query, kind)
	if err != nil {
		return nil, fmt.Errorf("store: list backups: %w", err)
	}
	defer rows.Close()

	var backups []Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		backups = append(backups, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list backups: %w", err)
	}
	return backups, nil
}

// DeleteBackup removes one backup's catalog entry. It does not touch its
// on-disk backup set; callers remove that first and only delete the catalog
// entry once removal succeeds, so a crash between the two never hides a
// backup set that still exists on disk.
func DeleteBackup(ctx context.Context, exec Executor, id model.ID) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, id.Bytes()); err != nil {
		return fmt.Errorf("store: delete backup: %w", err)
	}
	return nil
}

// MarkBackupVerified records that a backup passed manifest verification at
// verifiedUnixMS and clears any previous corrupt classification (a backup
// found corrupt once is eligible to be reclassified valid if a later
// verification, for example after copying it back from a healthy replica,
// succeeds).
func MarkBackupVerified(ctx context.Context, exec Executor, id model.ID, verifiedUnixMS int64) error {
	if _, err := exec.ExecContext(ctx, `UPDATE backups SET verified_unix_ms = ?, corrupt = 0 WHERE id = ?`, verifiedUnixMS, id.Bytes()); err != nil {
		return fmt.Errorf("store: mark backup verified: %w", err)
	}
	return nil
}

// MarkBackupCorrupt records that a backup failed manifest verification. The
// row and its on-disk backup set are both left in place; see Backup.Corrupt.
func MarkBackupCorrupt(ctx context.Context, exec Executor, id model.ID) error {
	if _, err := exec.ExecContext(ctx, `UPDATE backups SET corrupt = 1 WHERE id = ?`, id.Bytes()); err != nil {
		return fmt.Errorf("store: mark backup corrupt: %w", err)
	}
	return nil
}

// GetBackup returns one backup's catalog entry.
func GetBackup(ctx context.Context, exec Executor, id model.ID) (Backup, error) {
	row := exec.QueryRowContext(ctx,
		`SELECT id, kind, location, manifest_hash, verified_unix_ms, note_count, size_bytes, created_unix_ms, corrupt
		 FROM backups WHERE id = ?`,
		id.Bytes(),
	)
	return scanBackup(row)
}

func scanBackup(scanner rowScanner) (Backup, error) {
	var b Backup
	var idBytes []byte
	var verified, noteCount, sizeBytes sql.NullInt64
	if err := scanner.Scan(&idBytes, &b.Kind, &b.Location, &b.ManifestHash, &verified, &noteCount, &sizeBytes, &b.CreatedUnixMS, &b.Corrupt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Backup{}, ErrNotFound
		}
		return Backup{}, fmt.Errorf("store: scan backup: %w", err)
	}
	id, err := model.ParseID(idBytes)
	if err != nil {
		return Backup{}, fmt.Errorf("store: stored backup ID: %w", err)
	}
	b.ID = id
	if verified.Valid {
		v := verified.Int64
		b.VerifiedUnixMS = &v
	}
	if noteCount.Valid {
		v := noteCount.Int64
		b.NoteCount = &v
	}
	if sizeBytes.Valid {
		v := sizeBytes.Int64
		b.SizeBytes = &v
	}
	return b, nil
}

// ExportPlaintextSnapshot writes a consistent, unencrypted copy of db to
// destPath using SQLCipher's documented sqlcipher_export() cross-key export
// recipe (attach an unkeyed destination database, export into it, detach).
// Plain "VACUUM INTO" alone is not sufficient here: on an SQLCipher
// connection it preserves the source connection's page encryption rather
// than decrypting, so it cannot produce the plaintext snapshot a
// passphrase-portable backup needs to compress and re-encrypt under a
// separate backup key. destPath must not already exist. All three
// statements run on one held connection, since ATTACH/DETACH state is
// connection-scoped and a pooled *sql.DB does not guarantee successive
// calls reuse the same connection.
func ExportPlaintextSnapshot(ctx context.Context, db *sql.DB, destPath string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire connection for backup export: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("store: checkpoint before backup export: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS backup_plaintext KEY ''`, destPath); err != nil {
		return fmt.Errorf("store: attach backup export target: %w", err)
	}
	_, exportErr := conn.ExecContext(ctx, `SELECT sqlcipher_export('backup_plaintext')`)
	_, detachErr := conn.ExecContext(ctx, `DETACH DATABASE backup_plaintext`)
	if exportErr != nil {
		return fmt.Errorf("store: export plaintext backup snapshot: %w", exportErr)
	}
	if detachErr != nil {
		return fmt.Errorf("store: detach backup export target: %w", detachErr)
	}
	return nil
}

// ExportEncryptedSnapshot re-keys a plaintext SQLite database (for example,
// one produced by decrypting a portable backup) into a new SQLCipher
// database at destPath, encrypted under key, using the same
// sqlcipher_export() cross-key recipe as ExportPlaintextSnapshot but in
// reverse. destPath must not already exist. It is used by whole-database
// restore, which cannot reuse this device's existing database key (that key
// is wrapped by this device's own OS keystore and is never portable), so it
// generates and wraps a fresh one instead, exactly as account creation
// does.
func ExportEncryptedSnapshot(ctx context.Context, plainDB *sql.DB, destPath string, key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("store: encrypted export key must be 32 bytes, got %d", len(key))
	}
	conn, err := plainDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: acquire connection for encrypted export: %w", err)
	}
	defer conn.Close()

	rawKey := "x'" + hex.EncodeToString(key) + "'"
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS backup_encrypted KEY ?`, destPath, rawKey); err != nil {
		return fmt.Errorf("store: attach encrypted export target: %w", err)
	}
	_, exportErr := conn.ExecContext(ctx, `SELECT sqlcipher_export('backup_encrypted')`)
	_, detachErr := conn.ExecContext(ctx, `DETACH DATABASE backup_encrypted`)
	if exportErr != nil {
		return fmt.Errorf("store: export encrypted snapshot: %w", exportErr)
	}
	if detachErr != nil {
		return fmt.Errorf("store: detach encrypted export target: %w", detachErr)
	}
	return nil
}

// ListAllAttachmentBlobIDs returns the ID of every attachment tracked
// anywhere in the account, across all workspaces, regardless of orphan
// status. A backup's "self-contained blob set" includes orphaned-but-not-
// yet-collected attachments too: they are still within their retention
// grace period and a backup must not silently drop data garbage collection
// has not yet been authorized to remove.
func ListAllAttachmentBlobIDs(ctx context.Context, exec Executor) ([]BlobID, error) {
	rows, err := exec.QueryContext(ctx, `SELECT blob_id FROM attachments`)
	if err != nil {
		return nil, fmt.Errorf("store: list all attachment blob IDs: %w", err)
	}
	defer rows.Close()

	var ids []BlobID
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("store: scan attachment blob ID: %w", err)
		}
		id, err := ParseBlobID(raw)
		if err != nil {
			return nil, fmt.Errorf("store: stored attachment blob ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list all attachment blob IDs: %w", err)
	}
	return ids, nil
}

// CountNotes returns the total number of note rows in the account, across
// all workspaces, including deleted (tombstoned) ones, for backup catalog
// bookkeeping.
func CountNotes(ctx context.Context, exec Executor) (int64, error) {
	var count int64
	if err := exec.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes`).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count notes: %w", err)
	}
	return count, nil
}

// SumAttachmentSizes returns the total plaintext size of every attachment
// tracked anywhere in the account, for a backup capacity preflight
// estimate.
func SumAttachmentSizes(ctx context.Context, exec Executor) (uint64, error) {
	var total sql.NullInt64
	if err := exec.QueryRowContext(ctx, `SELECT SUM(size_bytes) FROM attachments`).Scan(&total); err != nil {
		return 0, fmt.Errorf("store: sum attachment sizes: %w", err)
	}
	if !total.Valid {
		return 0, nil
	}
	return uint64(total.Int64), nil
}
