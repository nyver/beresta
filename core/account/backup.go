package account

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
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
		return store.Backup{}, err
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
		return store.Backup{}, err
	}

	plaintextPath := filepath.Join(stagingDir, "plaintext.db")
	if err := store.ExportPlaintextSnapshot(ctx, db, plaintextPath); err != nil {
		return store.Backup{}, err
	}
	plaintext, err := os.ReadFile(plaintextPath)
	os.Remove(plaintextPath)
	if err != nil {
		return store.Backup{}, fmt.Errorf("account: read plaintext backup export: %w", err)
	}

	compressed, err := zstdCompress(plaintext)
	clear(plaintext)
	if err != nil {
		return store.Backup{}, err
	}

	nowUnixMS := uint64(now.UnixMilli())
	metadata, err := corecrypto.NewBackupMetadata(accountID.Bytes(), backupID, nowUnixMS, kdfParams)
	if err != nil {
		return store.Backup{}, err
	}
	envelope, err := corecrypto.EncryptBackup(rootKey, metadata, compressed)
	clear(compressed)
	if err != nil {
		return store.Backup{}, err
	}

	if err := writeBackupFile(filepath.Join(stagingDir, backupSnapshotFile), envelope); err != nil {
		return store.Backup{}, err
	}

	blobIDs, err := store.ListAllAttachmentBlobIDs(ctx, db)
	if err != nil {
		return store.Backup{}, err
	}
	relativePaths := []string{backupSnapshotFile}
	if len(blobIDs) > 0 {
		blobPaths, err := stageBackupBlobs(blobs, stagingDir, blobIDs)
		if err != nil {
			return store.Backup{}, err
		}
		relativePaths = append(relativePaths, blobPaths...)
	}

	manifest, err := corebackup.GenerateManifest(ctx, stagingDir, relativePaths)
	if err != nil {
		return store.Backup{}, err
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
		return store.Backup{}, err
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

	existing, err := store.ListBackups(ctx, db, store.BackupKindDaily)
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

	backups, err := store.ListBackups(ctx, db, store.BackupKindDaily)
	if err != nil {
		return err
	}
	if len(backups) <= dailyBackupRetention {
		return nil
	}
	// ListBackups orders newest first; everything past the retention count
	// is the oldest excess.
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
