package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"time"

	corebackup "github.com/beresta-app/beresta/core/backup"
	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

const maxImportedManifestBytes = 8 << 20

// ImportBackupSet authenticates a complete backup copied from a user-selected
// device or cloud-backed destination and registers it in the local catalog.
// The encrypted snapshot must belong to the unlocked account and its folder
// name must be its canonical backup ID.
func (a *Account) ImportBackupSet(ctx context.Context, location string, kind int, now time.Time) (store.Backup, error) {
	if kind != store.BackupKindDaily && kind != store.BackupKindManual {
		return store.Backup{}, errors.New("account: imported backup kind is invalid")
	}
	abs, err := filepath.Abs(location)
	if err != nil || filepath.Clean(abs) != abs {
		return store.Backup{}, errors.New("account: imported backup path is invalid")
	}
	manifestPath := filepath.Join(abs, backupManifestFile)
	info, err := os.Stat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxImportedManifestBytes {
		return store.Backup{}, errors.New("account: imported backup manifest is invalid")
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return store.Backup{}, err
	}
	var manifest corebackup.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return store.Backup{}, errors.New("account: imported backup manifest is malformed")
	}
	if err := corebackup.VerifyManifest(ctx, abs, manifest); err != nil {
		return store.Backup{}, errors.New("account: imported backup manifest verification failed")
	}
	containsSnapshot := false
	var sizeBytes int64
	for _, entry := range manifest.Entries {
		containsSnapshot = containsSnapshot || entry.Path == backupSnapshotFile
		if entry.Size > math.MaxInt64 || sizeBytes > math.MaxInt64-int64(entry.Size) {
			return store.Backup{}, errors.New("account: imported backup size is invalid")
		}
		sizeBytes += int64(entry.Size)
	}
	if !containsSnapshot {
		return store.Backup{}, errors.New("account: imported backup has no encrypted snapshot")
	}

	envelope, err := readBackupFile(filepath.Join(abs, backupSnapshotFile))
	if err != nil {
		return store.Backup{}, errors.New("account: imported backup identity mismatch")
	}
	id, err := model.ParseID(envelope.Header.BackupID)
	if err != nil || filepath.Base(abs) != id.String() {
		return store.Backup{}, errors.New("account: imported backup identity mismatch")
	}
	a.mu.Lock()
	if a.locked {
		a.mu.Unlock()
		return store.Backup{}, ErrAccountLocked
	}
	db, accountID, rootKey := a.db, a.ID, a.rootKey
	a.mu.Unlock()
	if !bytes.Equal(envelope.Header.AccountID, accountID.Bytes()) || envelope.Header.CreatedUnixMS > math.MaxInt64 {
		return store.Backup{}, errors.New("account: imported backup belongs to another account")
	}
	plaintext, err := corecrypto.OpenBackup(rootKey, envelope)
	if err != nil {
		return store.Backup{}, errors.New("account: imported backup authentication failed")
	}
	clear(plaintext)

	manifestHash := sha256.Sum256(manifestBytes)
	verified := now.UnixMilli()
	record := store.Backup{ID: id, Kind: kind, Location: abs, ManifestHash: manifestHash[:], VerifiedUnixMS: &verified,
		SizeBytes: &sizeBytes, CreatedUnixMS: int64(envelope.Header.CreatedUnixMS)}
	if err := store.InsertBackup(ctx, db, record); err != nil {
		return store.Backup{}, err
	}
	return record, nil
}
