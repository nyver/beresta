package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"

	corebackup "github.com/beresta-app/beresta/core/backup"
	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// dailyBackupRetention is the notes-management/backup-and-recovery spec's
// required number of retained daily backups ("retain exactly the latest
// seven valid daily snapshots after rotation").
const dailyBackupRetention = 7

// backupSnapshotFile and backupManifestFile are the fixed file names inside
// every backup set directory.
const (
	backupSnapshotFile = "snapshot.enc"
	backupManifestFile = "manifest.json"
)

// backupFileMagic identifies the on-disk container format written by
// writeBackupFile: a 4-byte big-endian JSON header length, the JSON-encoded
// corecrypto.BackupHeader (its own byte fields are small and self-describing
// via struct tags, so JSON is used only for this compact header), then the
// raw ciphertext. It is a private on-disk encoding, not a synchronization
// wire format.
const backupFileMagic = "BRSTBKF1"

// backupCapacityMarginNumerator/Denominator pads the raw plaintext-size
// estimate used for capacity preflight: the exported snapshot is written to
// disk once before compression, and rounding/filesystem block overhead
// across many small blob files adds a little more, so headroom beyond the
// exact estimate avoids failing partway through a backup that a tighter
// estimate would have called safe.
const (
	backupCapacityMarginNumerator   = 11
	backupCapacityMarginDenominator = 10
)

// ErrInsufficientBackupCapacity reports that a backup destination did not
// have enough free space for the estimated backup size. CreateBackup
// returns it before writing anything, so existing valid backups are always
// left untouched (specs/backup-and-recovery.md, "Insufficient mobile
// storage": "the client skips the unsafe write, preserves existing valid
// backups").
var ErrInsufficientBackupCapacity = errors.New("account: insufficient free space for backup")

// checkBackupCapacity estimates the size CreateBackup is about to write to
// destRoot (the plaintext database export plus every tracked attachment,
// deliberately not netting out zstd compression, which only shrinks the
// database portion) and fails closed if destRoot's free space is not enough
// for it.
func checkBackupCapacity(ctx context.Context, db *sql.DB, destRoot string) error {
	estimated, err := estimateBackupBytes(ctx, db)
	if err != nil {
		return err
	}
	estimated = estimated * backupCapacityMarginNumerator / backupCapacityMarginDenominator

	free, err := freeBytesAt(destRoot)
	if err != nil {
		return fmt.Errorf("account: check backup destination capacity: %w", err)
	}
	if free < estimated {
		return ErrInsufficientBackupCapacity
	}
	return nil
}

// estimateBackupBytes sums the live database's on-disk size (via SQLite's
// own page accounting, not a file stat, so it works before any export
// exists) and every tracked attachment's plaintext size.
func estimateBackupBytes(ctx context.Context, db *sql.DB) (uint64, error) {
	var pageCount, pageSize int64
	if err := db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("account: read database page count: %w", err)
	}
	if err := db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("account: read database page size: %w", err)
	}
	databaseBytes := uint64(pageCount) * uint64(pageSize)

	blobBytes, err := store.SumAttachmentSizes(ctx, db)
	if err != nil {
		return 0, err
	}
	return databaseBytes + blobBytes, nil
}

// CreateBackup assembles and durably publishes one backup set under
// destRoot/<backup ID>: a SQLCipher-consistent plaintext export of the
// database, zstd-compressed and encrypted under a backup key derived from
// the account's Root Key, plus every attachment blob currently tracked
// anywhere in the account, plus an authenticated manifest covering the
// whole set. It publishes to a temporary directory and renames it into
// place only once complete, so a crash never leaves a partial backup set
// visible, and only then records the backup in the catalog.
func (a *Account) CreateBackup(ctx context.Context, destRoot string, kind int, now time.Time) (store.Backup, error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return store.Backup{}, ErrAccountLocked
	}
	db := a.db
	rootKey := a.rootKey
	accountID := a.ID
	blobs := a.blobs
	a.mu.Unlock()

	accountRow, err := loadAccountRow(ctx, db)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: load account for backup: %w", err)
	}
	kdfParams := corecrypto.Argon2idParams{
		CryptoProfile:   corecrypto.CryptoProfileV1,
		Algorithm:       corecrypto.Argon2idName,
		Salt:            accountRow.kdfSalt,
		MemoryKiB:       accountRow.kdfMemoryKiB,
		TimeCost:        accountRow.kdfTimeCost,
		Parallelism:     accountRow.kdfParallelism,
		DerivedKeyBytes: corecrypto.RootKeyBytes,
	}

	backupIDModel, err := model.NewID()
	if err != nil {
		return store.Backup{}, err
	}
	backupID := backupIDModel.Bytes()

	if err := os.MkdirAll(destRoot, 0o700); err != nil {
		return store.Backup{}, fmt.Errorf("account: create backup destination: %w", err)
	}
	if err := checkBackupCapacity(ctx, db, destRoot); err != nil {
		return store.Backup{}, err
	}
	stagingDir, err := os.MkdirTemp(destRoot, ".staging-*")
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: create backup staging directory: %w", err)
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			os.RemoveAll(stagingDir)
		}
	}()

	noteCount, err := store.CountNotes(ctx, db)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: count notes for backup: %w", err)
	}

	plaintextPath := filepath.Join(stagingDir, "plaintext.db")
	if err := store.ExportPlaintextSnapshot(ctx, db, plaintextPath); err != nil {
		return store.Backup{}, fmt.Errorf("account: create plaintext backup snapshot: %w", err)
	}
	plaintext, err := os.ReadFile(plaintextPath)
	os.Remove(plaintextPath)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: read plaintext backup export: %w", err)
	}

	compressed, err := zstdCompress(plaintext)
	clear(plaintext)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: compress backup snapshot: %w", err)
	}

	nowUnixMS := uint64(now.UnixMilli())
	metadata, err := corecrypto.NewBackupMetadata(accountID.Bytes(), backupID, nowUnixMS, kdfParams)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: create backup metadata: %w", err)
	}
	envelope, err := corecrypto.EncryptBackup(rootKey, metadata, compressed)
	clear(compressed)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: encrypt backup snapshot: %w", err)
	}

	if err := writeBackupFile(filepath.Join(stagingDir, backupSnapshotFile), envelope); err != nil {
		return store.Backup{}, fmt.Errorf("account: write encrypted backup snapshot: %w", err)
	}

	blobIDs, err := store.ListAllAttachmentBlobIDs(ctx, db)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: list backup blobs: %w", err)
	}
	relativePaths := []string{backupSnapshotFile}
	if len(blobIDs) > 0 {
		blobPaths, err := stageBackupBlobs(blobs, stagingDir, blobIDs)
		if err != nil {
			return store.Backup{}, fmt.Errorf("account: stage backup blobs: %w", err)
		}
		relativePaths = append(relativePaths, blobPaths...)
	}

	manifest, err := corebackup.GenerateManifest(ctx, stagingDir, relativePaths)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: generate backup manifest: %w", err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: encode backup manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, backupManifestFile), manifestBytes, 0o600); err != nil {
		return store.Backup{}, fmt.Errorf("account: write backup manifest: %w", err)
	}
	manifestHash := sha256.Sum256(manifestBytes)

	var sizeBytes int64
	for _, entry := range manifest.Entries {
		sizeBytes += int64(entry.Size)
	}

	finalDir := filepath.Join(destRoot, backupIDModel.String())
	if err := os.Rename(stagingDir, finalDir); err != nil {
		return store.Backup{}, fmt.Errorf("account: publish backup set: %w", err)
	}
	cleanupStaging = false

	record := store.Backup{
		ID:            backupIDModel,
		Kind:          kind,
		Location:      finalDir,
		ManifestHash:  manifestHash[:],
		NoteCount:     &noteCount,
		SizeBytes:     &sizeBytes,
		CreatedUnixMS: int64(nowUnixMS),
	}
	// A failure here (the backup set is already published, but its catalog
	// row is not) leaves an orphaned backup set directory: unlike blobs,
	// there is no garbage-collection sweep for those. This is accepted as a
	// rare residual risk, not fixed by reordering: publishing the catalog
	// row before the set exists would let a restore or rotation reference a
	// set that is not actually there yet.
	if err := store.InsertBackup(ctx, db, record); err != nil {
		return store.Backup{}, fmt.Errorf("account: record backup catalog entry: %w", err)
	}
	return record, nil
}

// EnsureDailyBackup creates today's (in now's local timezone) daily backup
// if one does not already exist, then rotates daily backups down to
// dailyBackupRetention. It satisfies both the routine daily-backup
// requirement and the "device was powered off" missed-day requirement: a
// client that calls this once at every startup, before normal mutation
// work proceeds, always has a backup for the current day and never more
// than dailyBackupRetention of them.
func (a *Account) EnsureDailyBackup(ctx context.Context, destRoot string, now time.Time) (created bool, err error) {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return false, ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()

	existing, err := store.ListValidBackups(ctx, db, store.BackupKindDaily)
	if err != nil {
		return false, err
	}
	for _, b := range existing {
		if isSameLocalDay(time.UnixMilli(b.CreatedUnixMS), now) {
			return false, nil
		}
	}

	if _, err := a.CreateBackup(ctx, destRoot, store.BackupKindDaily, now); err != nil {
		return false, err
	}
	if err := a.rotateDailyBackups(ctx, destRoot); err != nil {
		return true, err
	}
	return true, nil
}

// rotateDailyBackups deletes daily backups beyond dailyBackupRetention,
// oldest first, removing each one's on-disk backup set before its catalog
// entry (see store.DeleteBackup) so a crash mid-rotation can never hide a
// backup set that still exists on disk.
func (a *Account) rotateDailyBackups(ctx context.Context, destRoot string) error {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()

	backups, err := store.ListValidBackups(ctx, db, store.BackupKindDaily)
	if err != nil {
		return err
	}
	if len(backups) <= dailyBackupRetention {
		return nil
	}
	// ListValidBackups orders newest first; everything past the retention
	// count is the oldest excess. Corrupt backups are excluded from this
	// list entirely (specs/backup-and-recovery.md: corrupt archives "SHALL
	// not count as valid snapshots during rotation"), so they are never
	// counted toward or removed by this rotation.
	for _, b := range backups[dailyBackupRetention:] {
		if err := os.RemoveAll(b.Location); err != nil {
			return fmt.Errorf("account: remove rotated backup set: %w", err)
		}
		if err := store.DeleteBackup(ctx, db, b.ID); err != nil {
			return err
		}
	}
	return nil
}

// VerifyBackup re-hashes every file in one backup's set against the
// manifest it was published with and confirms that manifest itself matches
// the catalog's recorded hash, then records the result: verified at now on
// success, or corrupt on failure (see specs/backup-and-recovery.md,
// "Backup integrity classification"). It does not decrypt the encrypted
// snapshot itself; that authentication only happens on restore, with the
// account's Root Key. Call it at startup for every backup and again
// immediately before a restore, per the spec's two required verification
// points.
func (a *Account) VerifyBackup(ctx context.Context, id model.ID, now time.Time) error {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()

	backup, err := store.GetBackup(ctx, db, id)
	if err != nil {
		return err
	}

	if verifyErr := verifyBackupSet(ctx, backup); verifyErr != nil {
		return store.MarkBackupCorrupt(ctx, db, id)
	}
	return store.MarkBackupVerified(ctx, db, id, now.UnixMilli())
}

// VerifyAllBackups runs VerifyBackup over every catalog entry, for the
// spec's required startup verification pass. It keeps going after an
// individual backup fails to verify (that failure is recorded on its own
// row, not fatal to the sweep) and returns every error encountered, if any.
func (a *Account) VerifyAllBackups(ctx context.Context, now time.Time) error {
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return ErrAccountLocked
	}
	db := a.db
	a.mu.Unlock()

	var allBackups []store.Backup
	for _, kind := range []int{store.BackupKindDaily, store.BackupKindPreMigration, store.BackupKindPreRestore, store.BackupKindManual} {
		kindBackups, err := store.ListBackups(ctx, db, kind)
		if err != nil {
			return err
		}
		allBackups = append(allBackups, kindBackups...)
	}

	var errs []error
	for _, b := range allBackups {
		if err := a.VerifyBackup(ctx, b.ID, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// verifyBackupSet re-hashes a backup set's manifest.json against the
// catalog's recorded hash, then re-hashes every file the manifest declares
// against it. Either mismatch, or a missing file, reports a verification
// failure.
func verifyBackupSet(ctx context.Context, backup store.Backup) error {
	manifestPath := filepath.Join(backup.Location, backupManifestFile)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("account: read backup manifest: %w", err)
	}
	actualHash := sha256.Sum256(manifestBytes)
	if !bytes.Equal(actualHash[:], backup.ManifestHash) {
		return fmt.Errorf("account: backup manifest hash mismatch")
	}

	var manifest corebackup.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("account: decode backup manifest: %w", err)
	}
	return corebackup.VerifyManifest(ctx, backup.Location, manifest)
}

// isSameLocalDay reports whether a and b fall on the same calendar day in
// b's location (the caller's "now").
func isSameLocalDay(a, b time.Time) bool {
	a = a.In(b.Location())
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// stageBackupBlobs hardlinks (or, if that fails, copies) every blob in
// blobIDs into the same two-level content-addressed layout as the live blob
// store, rooted at stagingDir, and returns their paths relative to
// stagingDir for the manifest. A missing blob file is skipped rather than
// failing the whole backup: its attachment row still exists and is
// captured, and a blob that vanished from disk between listing and staging
// is a pre-existing local corruption unrelated to backup creation.
func stageBackupBlobs(blobs *store.BlobStore, stagingDir string, blobIDs []store.BlobID) ([]string, error) {
	root := filepath.Join(stagingDir, "blobs")
	relativePaths := make([]string, 0, len(blobIDs))
	for _, id := range blobIDs {
		src := blobs.Path(id)
		info, err := os.Stat(src)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		hexID := fmt.Sprintf("%x", id.Bytes())
		dst := filepath.Join(root, hexID[0:2], hexID[2:4], hexID)
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, fmt.Errorf("account: create backup blob directory: %w", err)
		}
		if err := os.Link(src, dst); err != nil {
			if err := copyFile(src, dst); err != nil {
				return nil, fmt.Errorf("account: stage backup blob: %w", err)
			}
		}
		relativePaths = append(relativePaths, filepath.ToSlash(filepath.Join("blobs", hexID[0:2], hexID[2:4], hexID)))
	}
	return relativePaths, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func zstdCompress(plaintext []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, fmt.Errorf("account: create zstd encoder: %w", err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(plaintext, make([]byte, 0, len(plaintext)/2)), nil
}

func zstdDecompress(compressed []byte, maxDecodedBytes uint64) ([]byte, error) {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxDecodedBytes))
	if err != nil {
		return nil, fmt.Errorf("account: create zstd decoder: %w", err)
	}
	defer decoder.Close()
	decoded, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf("account: decompress backup archive: %w", err)
	}
	return decoded, nil
}

// writeBackupFile durably writes envelope in backupFileMagic's container
// format via write-temp/fsync/rename, so a crash never leaves a partially
// written snapshot file at its final path.
func writeBackupFile(path string, envelope corecrypto.EncryptedBackup) error {
	headerJSON, err := json.Marshal(envelope.Header)
	if err != nil {
		return fmt.Errorf("account: encode backup header: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("account: create backup snapshot temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write([]byte(backupFileMagic)); err != nil {
		tmp.Close()
		return fmt.Errorf("account: write backup snapshot: %w", err)
	}
	var lengthPrefix [4]byte
	binary.BigEndian.PutUint32(lengthPrefix[:], uint32(len(headerJSON)))
	if _, err := tmp.Write(lengthPrefix[:]); err != nil {
		tmp.Close()
		return fmt.Errorf("account: write backup snapshot: %w", err)
	}
	if _, err := tmp.Write(headerJSON); err != nil {
		tmp.Close()
		return fmt.Errorf("account: write backup snapshot: %w", err)
	}
	if _, err := tmp.Write(envelope.Ciphertext); err != nil {
		tmp.Close()
		return fmt.Errorf("account: write backup snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("account: sync backup snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("account: close backup snapshot: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("account: publish backup snapshot: %w", err)
	}
	cleanup = false
	return nil
}

// readBackupFile parses a file written by writeBackupFile. It does not
// authenticate the result; callers must still call corecrypto.OpenBackup or
// corecrypto.UnlockBackup, which does.
func readBackupFile(path string) (corecrypto.EncryptedBackup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return corecrypto.EncryptedBackup{}, fmt.Errorf("account: read backup snapshot: %w", err)
	}
	prefixLen := len(backupFileMagic) + 4
	if len(data) < prefixLen || string(data[:len(backupFileMagic)]) != backupFileMagic {
		return corecrypto.EncryptedBackup{}, fmt.Errorf("account: backup snapshot has an invalid container header")
	}
	headerLen := binary.BigEndian.Uint32(data[len(backupFileMagic):prefixLen])
	if uint64(len(data)-prefixLen) < uint64(headerLen) {
		return corecrypto.EncryptedBackup{}, fmt.Errorf("account: backup snapshot header is truncated")
	}
	var header corecrypto.BackupHeader
	if err := json.Unmarshal(data[prefixLen:prefixLen+int(headerLen)], &header); err != nil {
		return corecrypto.EncryptedBackup{}, fmt.Errorf("account: decode backup snapshot header: %w", err)
	}
	ciphertext := data[prefixLen+int(headerLen):]
	return corecrypto.EncryptedBackup{Header: header, Ciphertext: ciphertext}, nil
}
