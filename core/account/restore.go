package account

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/keystore"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// ErrBackupCorrupt reports that a backup failed verification and is
// therefore ineligible for restore (specs/backup-and-recovery.md, "Backup
// integrity classification": corrupt archives "SHALL be ineligible for
// restore").
var ErrBackupCorrupt = errors.New("account: backup is marked corrupt and cannot be restored")

// BackupPreview summarizes one backup's content for the restore UI without
// mutating current data.
type BackupPreview struct {
	Backup     store.Backup
	NoteTitles []string
}

// PreviewBackup opens (decrypts, decompresses) a backup's database snapshot
// read-only and returns its catalog entry plus every note's title, for the
// spec's required backup preview ("show each backup's date, size, and note
// count") before a user commits to any restore action.
func (a *Account) PreviewBackup(ctx context.Context, backupID model.ID) (BackupPreview, error) {
	db, rootKey, err := a.accountSession()
	if err != nil {
		return BackupPreview{}, err
	}
	backup, err := store.GetBackup(ctx, db, backupID)
	if err != nil {
		return BackupPreview{}, err
	}

	sourceDB, sourcePath, err := openBackupPlaintextDatabase(backup, rootKey)
	if err != nil {
		return BackupPreview{}, err
	}
	defer closeBackupSource(sourceDB, sourcePath)

	notes, err := store.ListNotes(ctx, sourceDB, backupSourceWorkspaceID(ctx, sourceDB))
	if err != nil {
		return BackupPreview{}, err
	}
	titles := make([]string, len(notes))
	for i, n := range notes {
		titles[i] = n.Title.Value
	}
	return BackupPreview{Backup: backup, NoteTitles: titles}, nil
}

// RestoreChangeKind classifies one note in a RestorePlan.
type RestoreChangeKind uint8

const (
	// RestoreChangeAddition means the note does not exist locally.
	RestoreChangeAddition RestoreChangeKind = iota + 1
	// RestoreChangeUpdate means the note exists locally with different
	// content.
	RestoreChangeUpdate
	// RestoreChangeUnchanged means the note exists locally with identical
	// content; restoring it would be a no-op.
	RestoreChangeUnchanged
)

// RestorePlanEntry is one note's classification in a RestorePlan.
type RestorePlanEntry struct {
	NoteID model.ID
	Title  string
	Kind   RestoreChangeKind
}

// RestorePlan is the result of a dry-run restore preview: what would
// change, and an estimate of the additional local storage it would need.
// It mutates nothing.
type RestorePlan struct {
	Entries              []RestorePlanEntry
	RequiredStorageBytes uint64
}

// PlanRestore computes, without mutating current data, what restoring
// noteIDs (or the whole backup, when noteIDs is empty) from backupID would
// do. "Update" is a coarse comparison — the note's title/notebook/flags/
// deleted state and its CRDT state vector must all match exactly, or it
// counts as changed — not the sync engine's CRDT-aware convergence; it
// reports that content differs, not why, and does not attempt real
// conflict resolution (that belongs to the synchronization layer, tasks.md
// phase 6/7, not backup/restore).
func (a *Account) PlanRestore(ctx context.Context, backupID model.ID, noteIDs []model.ID) (RestorePlan, error) {
	db, rootKey, err := a.accountSession()
	if err != nil {
		return RestorePlan{}, err
	}
	backup, err := store.GetBackup(ctx, db, backupID)
	if err != nil {
		return RestorePlan{}, err
	}
	sourceDB, sourcePath, err := openBackupPlaintextDatabase(backup, rootKey)
	if err != nil {
		return RestorePlan{}, err
	}
	defer closeBackupSource(sourceDB, sourcePath)

	workspaceID := backupSourceWorkspaceID(ctx, sourceDB)
	sourceNotes, err := selectSourceNotes(ctx, sourceDB, workspaceID, noteIDs)
	if err != nil {
		return RestorePlan{}, err
	}

	plan := RestorePlan{Entries: make([]RestorePlanEntry, 0, len(sourceNotes))}
	for _, sn := range sourceNotes {
		kind, err := classifyRestoreNote(ctx, db, sourceDB, sn)
		if err != nil {
			return RestorePlan{}, err
		}
		plan.Entries = append(plan.Entries, RestorePlanEntry{NoteID: sn.ID, Title: sn.Title.Value, Kind: kind})
		if kind == RestoreChangeUnchanged {
			continue
		}
		additional, err := additionalStorageForNote(ctx, sourceDB, a.blobs, sn.ID)
		if err != nil {
			return RestorePlan{}, err
		}
		plan.RequiredStorageBytes += additional
	}
	return plan, nil
}

func classifyRestoreNote(ctx context.Context, liveDB, sourceDB store.Executor, sourceNote model.Note) (RestoreChangeKind, error) {
	liveNote, err := store.GetNote(ctx, liveDB, sourceNote.ID)
	if errors.Is(err, store.ErrNotFound) {
		return RestoreChangeAddition, nil
	}
	if err != nil {
		return 0, err
	}
	if liveNote.Title.Value != sourceNote.Title.Value ||
		liveNote.NotebookID.Value != sourceNote.NotebookID.Value ||
		liveNote.Flags.Value != sourceNote.Flags.Value ||
		liveNote.Deleted.Value != sourceNote.Deleted.Value {
		return RestoreChangeUpdate, nil
	}
	liveVector, _, err := store.LoadCRDTState(ctx, liveDB, sourceNote.ID)
	if err != nil {
		return 0, err
	}
	sourceVector, _, err := store.LoadCRDTState(ctx, sourceDB, sourceNote.ID)
	if err != nil {
		return 0, err
	}
	if !bytes.Equal(liveVector.StateVector, sourceVector.StateVector) {
		return RestoreChangeUpdate, nil
	}
	return RestoreChangeUnchanged, nil
}

// additionalStorageForNote sums the plaintext size of every attachment
// sourceNoteID references in sourceDB that is not already published in the
// live blob store.
func additionalStorageForNote(ctx context.Context, sourceDB store.Executor, liveBlobs *store.BlobStore, sourceNoteID model.ID) (uint64, error) {
	blobIDs, err := store.NoteAttachmentBlobIDs(ctx, sourceDB, sourceNoteID)
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, blobID := range blobIDs {
		exists, err := liveBlobs.Exists(blobID)
		if err != nil {
			return 0, err
		}
		if exists {
			continue
		}
		attachment, err := store.GetAttachment(ctx, sourceDB, blobID)
		if err != nil {
			return 0, err
		}
		total += attachment.SizeBytes
	}
	return total, nil
}

// RestoreResult reports what a restore actually did.
type RestoreResult struct {
	SafetyBackup store.Backup
	// NewNoteIDs holds the freshly assigned IDs of every note
	// RestoreSelective imported (empty for RestoreWhole, which replaces the
	// whole dataset in place rather than assigning new IDs).
	NewNoteIDs []model.ID
}

// RestoreSelective imports noteIDs from backupID as new local notes and
// operations (specs/backup-and-recovery.md: "selective restore imports
// chosen objects as new local operations so future synchronization remains
// coherent"), preserving their title, notebook path, tags, flags, deleted
// state, body content, and attachments, but assigning each a fresh note ID
// rather than reusing the backup's. It always takes a mandatory pre-restore
// safety backup first and verifies the target backup before importing
// anything.
func (a *Account) RestoreSelective(ctx context.Context, backupID model.ID, noteIDs []model.ID, destRoot string, now time.Time) (RestoreResult, error) {
	if len(noteIDs) == 0 {
		return RestoreResult{}, fmt.Errorf("account: selective restore requires at least one note ID")
	}
	db, rootKey, err := a.accountSession()
	if err != nil {
		return RestoreResult{}, err
	}

	safetyBackup, err := a.CreateBackup(ctx, destRoot, store.BackupKindPreRestore, now)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("account: create mandatory pre-restore safety backup: %w", err)
	}
	if err := a.verifyBackupForRestore(ctx, db, backupID, now); err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}

	backup, err := store.GetBackup(ctx, db, backupID)
	if err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}
	sourceDB, sourcePath, err := openBackupPlaintextDatabase(backup, rootKey)
	if err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}
	defer closeBackupSource(sourceDB, sourcePath)
	sourceBlobs := store.NewBlobStore(filepath.Join(backup.Location, "blobs"), a.blobs.TempDir())

	workspaceID := backupSourceWorkspaceID(ctx, sourceDB)
	entry, ok := a.workspaceKeysSnapshot()[workspaceID]
	if !ok {
		return RestoreResult{SafetyBackup: safetyBackup}, ErrUnknownWorkspace
	}

	notebookMapping := make(map[model.ID]model.ID)
	tagMapping := make(map[model.ID]model.ID)
	result := RestoreResult{SafetyBackup: safetyBackup, NewNoteIDs: make([]model.ID, 0, len(noteIDs))}

	for _, sourceNoteID := range noteIDs {
		sourceNote, err := store.GetNote(ctx, sourceDB, sourceNoteID)
		if err != nil {
			return result, fmt.Errorf("account: read note %s from backup: %w", sourceNoteID, err)
		}

		newNotebookID := model.Nil
		if !sourceNote.NotebookID.Value.IsZero() {
			newNotebookID, err = a.resolveOrCreateNotebookPath(ctx, workspaceID, sourceDB, sourceNote.NotebookID.Value, notebookMapping)
			if err != nil {
				return result, err
			}
		}

		newNote, err := a.CreateNote(ctx, workspaceID, newNotebookID, sourceNote.Title.Value)
		if err != nil {
			return result, err
		}
		result.NewNoteIDs = append(result.NewNoteIDs, newNote.ID)

		doc, err := loadNoteDocument(ctx, sourceDB, entry, workspaceID, sourceNoteID)
		if err != nil {
			return result, err
		}
		update, err := doc.EncodeStateAsUpdate(noteSnapshotFormat)
		doc.Close()
		if err != nil {
			return result, err
		}
		if len(update) > 0 {
			if err := a.CommitNoteBody(ctx, NoteBodyCommand{
				WorkspaceID: workspaceID, NoteID: newNote.ID, Update: update, UpdateFormat: noteSnapshotFormat,
			}); err != nil {
				return result, err
			}
		}

		if sourceNote.Flags.Value != 0 {
			if err := a.SetNoteFlags(ctx, workspaceID, newNote.ID, sourceNote.Flags.Value); err != nil {
				return result, err
			}
		}
		if sourceNote.Deleted.Value {
			if err := a.DeleteNote(ctx, workspaceID, newNote.ID); err != nil {
				return result, err
			}
		}

		sourceTagIDs, err := store.NoteTagIDs(ctx, sourceDB, sourceNoteID)
		if err != nil {
			return result, err
		}
		for _, sourceTagID := range sourceTagIDs {
			newTagID, err := a.resolveOrCreateTag(ctx, workspaceID, sourceDB, sourceTagID, tagMapping)
			if err != nil {
				return result, err
			}
			if err := a.SetNoteTag(ctx, workspaceID, newNote.ID, newTagID, true); err != nil {
				return result, err
			}
		}

		blobIDs, err := store.NoteAttachmentBlobIDs(ctx, sourceDB, sourceNoteID)
		if err != nil {
			return result, err
		}
		for _, blobID := range blobIDs {
			var buf bytes.Buffer
			displayName, mediaType, err := readAttachmentFrom(ctx, sourceDB, sourceBlobs, entry.Key, workspaceID, blobID, &buf)
			if err != nil {
				return result, fmt.Errorf("account: read attachment from backup: %w", err)
			}
			if _, err := a.AddAttachment(ctx, workspaceID, newNote.ID, displayName, mediaType, &buf); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

// resolveOrCreateNotebookPath finds or creates a local notebook matching
// sourceNotebookID's full ancestor chain (by name, recreated from the
// workspace root down), memoizing already-resolved notebooks in mapping so
// a restore importing many notes under the same notebook only walks and
// creates it once.
func (a *Account) resolveOrCreateNotebookPath(ctx context.Context, workspaceID model.ID, sourceDB store.Executor, sourceNotebookID model.ID, mapping map[model.ID]model.ID) (model.ID, error) {
	if resolved, ok := mapping[sourceNotebookID]; ok {
		return resolved, nil
	}
	sourceNotebooks, err := store.ListNotebooks(ctx, sourceDB, backupSourceWorkspaceID(ctx, sourceDB))
	if err != nil {
		return model.Nil, err
	}
	byID := make(map[model.ID]store.Notebook, len(sourceNotebooks))
	for _, nb := range sourceNotebooks {
		byID[nb.ID] = nb
	}

	var chain []store.Notebook
	current := sourceNotebookID
	for !current.IsZero() {
		nb, ok := byID[current]
		if !ok {
			break
		}
		chain = append([]store.Notebook{nb}, chain...)
		current = nb.ParentID
	}

	parentID := model.Nil
	for _, nb := range chain {
		if resolved, ok := mapping[nb.ID]; ok {
			parentID = resolved
			continue
		}
		local, err := findOrCreateLocalNotebook(ctx, a, workspaceID, parentID, nb.Name)
		if err != nil {
			return model.Nil, err
		}
		mapping[nb.ID] = local
		parentID = local
	}
	return parentID, nil
}

func findOrCreateLocalNotebook(ctx context.Context, a *Account, workspaceID, parentID model.ID, name string) (model.ID, error) {
	existing, err := a.ListNotebooks(ctx, workspaceID)
	if err != nil {
		return model.Nil, err
	}
	for _, nb := range existing {
		if !nb.Deleted && nb.ParentID == parentID && nb.Name == name {
			return nb.ID, nil
		}
	}
	created, err := a.CreateNotebook(ctx, workspaceID, parentID, name)
	if err != nil {
		return model.Nil, err
	}
	return created.ID, nil
}

// resolveOrCreateTag finds or creates a local tag matching sourceTagID's
// name, memoizing already-resolved tags in mapping.
func (a *Account) resolveOrCreateTag(ctx context.Context, workspaceID model.ID, sourceDB store.Executor, sourceTagID model.ID, mapping map[model.ID]model.ID) (model.ID, error) {
	if resolved, ok := mapping[sourceTagID]; ok {
		return resolved, nil
	}
	sourceTags, err := store.ListTags(ctx, sourceDB, backupSourceWorkspaceID(ctx, sourceDB))
	if err != nil {
		return model.Nil, err
	}
	var name string
	for _, tag := range sourceTags {
		if tag.ID == sourceTagID {
			name = tag.Name
			break
		}
	}
	if name == "" {
		return model.Nil, fmt.Errorf("account: backup tag %s not found", sourceTagID)
	}
	if existing, err := store.GetTagByName(ctx, a.db, workspaceID, name); err == nil {
		mapping[sourceTagID] = existing.ID
		return existing.ID, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return model.Nil, err
	}
	created, err := a.CreateTag(ctx, workspaceID, name)
	if err != nil {
		return model.Nil, err
	}
	mapping[sourceTagID] = created.ID
	return created.ID, nil
}

// RestoreWhole atomically replaces the entire local database with
// backupID's content: it takes a mandatory pre-restore safety backup,
// verifies the target backup, decrypts and decompresses its snapshot,
// re-encrypts it under a brand-new device database key (the original
// device key is wrapped by this device's OS keystore and is never
// portable, so a restored database cannot reuse it), and swaps it into
// place. If anything after the safety backup fails, the original database
// file and key envelope are restored and reopened, so the account is left
// exactly as it was and never partially replaced.
func (a *Account) RestoreWhole(ctx context.Context, backupID model.ID, destRoot string, now time.Time) (RestoreResult, error) {
	db, rootKey, err := a.accountSession()
	if err != nil {
		return RestoreResult{}, err
	}

	safetyBackup, err := a.CreateBackup(ctx, destRoot, store.BackupKindPreRestore, now)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("account: create mandatory pre-restore safety backup: %w", err)
	}
	if err := a.verifyBackupForRestore(ctx, db, backupID, now); err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}
	backup, err := store.GetBackup(ctx, db, backupID)
	if err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}

	plaintext, err := openBackupPlaintext(backup, rootKey)
	if err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}
	defer clear(plaintext)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.locked {
		return RestoreResult{SafetyBackup: safetyBackup}, ErrAccountLocked
	}

	stagingDir, err := os.MkdirTemp(filepath.Dir(a.databasePath), ".whole-restore-*")
	if err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, fmt.Errorf("account: create restore staging directory: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	sourcePath := filepath.Join(stagingDir, "source.db")
	if err := os.WriteFile(sourcePath, plaintext, 0o600); err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, fmt.Errorf("account: stage restore source database: %w", err)
	}
	sourceDB, err := sql.Open("sqlite3", sourcePath)
	if err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, fmt.Errorf("account: open restore source database: %w", err)
	}
	sourceDB.SetMaxOpenConns(1)

	if err := checkPlaintextIntegrity(ctx, sourceDB); err != nil {
		sourceDB.Close()
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}
	if _, err := store.Migrate(ctx, sourceDB); err != nil {
		sourceDB.Close()
		return RestoreResult{SafetyBackup: safetyBackup}, fmt.Errorf("account: migrate restore source database: %w", err)
	}

	dbKey, envelope, err := store.LoadOrCreateDatabaseKey(ctx, a.wrapper, localDeviceKeyID, nil)
	if err != nil {
		sourceDB.Close()
		return RestoreResult{SafetyBackup: safetyBackup}, err
	}
	var keyBytes []byte
	if useErr := dbKey.Use(func(b []byte) error {
		keyBytes = append([]byte(nil), b...)
		return nil
	}); useErr != nil {
		dbKey.Close()
		sourceDB.Close()
		return RestoreResult{SafetyBackup: safetyBackup}, useErr
	}

	encryptedPath := filepath.Join(stagingDir, "restored.db")
	exportErr := store.ExportEncryptedSnapshot(ctx, sourceDB, encryptedPath, keyBytes)
	clear(keyBytes)
	sourceDB.Close()
	if exportErr != nil {
		dbKey.Close()
		return RestoreResult{SafetyBackup: safetyBackup}, exportErr
	}

	if err := a.db.Close(); err != nil {
		dbKey.Close()
		return RestoreResult{SafetyBackup: safetyBackup}, fmt.Errorf("account: close live database before restore: %w", err)
	}
	newDB, swapErr := restoreDatabaseFile(ctx, a.databasePath, encryptedPath, a.wrapper, dbKey, envelope)
	dbKey.Close()
	a.db = newDB
	if swapErr != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, swapErr
	}

	// The whole database, including its backups catalog table, was just
	// replaced by backupID's content — which predates the safety backup
	// taken moments ago, so that row no longer exists in the (now live)
	// database even though its set is durably on disk. Re-register it so
	// "recover the state that existed immediately before this restore"
	// stays discoverable through the normal catalog, not just on disk.
	if err := store.InsertBackup(ctx, a.db, safetyBackup); err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, fmt.Errorf("account: re-register pre-restore safety backup: %w", err)
	}

	// The restored database's attachment rows may reference blobs no longer
	// present in the live blob store (for example, a backup older than a
	// blob's local garbage collection). Republish any that the backup set
	// still has; one it does not have either is a pre-existing gap this
	// restore cannot recover and is left as-is.
	if err := restoreBackupBlobsIntoLive(ctx, a.db, a.blobs, backup.Location); err != nil {
		return RestoreResult{SafetyBackup: safetyBackup}, fmt.Errorf("account: restore attachment blobs: %w", err)
	}
	return RestoreResult{SafetyBackup: safetyBackup}, nil
}

// restoreBackupBlobsIntoLive republishes every attachment blob the (already
// restored) live database references but the live blob store does not yet
// have, copying from the backup set's own blob directory.
func restoreBackupBlobsIntoLive(ctx context.Context, liveDB *sql.DB, liveBlobs *store.BlobStore, backupLocation string) error {
	blobIDs, err := store.ListAllAttachmentBlobIDs(ctx, liveDB)
	if err != nil {
		return err
	}
	backupBlobs := store.NewBlobStore(filepath.Join(backupLocation, "blobs"), liveBlobs.TempDir())
	for _, id := range blobIDs {
		liveExists, err := liveBlobs.Exists(id)
		if err != nil {
			return err
		}
		if liveExists {
			continue
		}
		backupExists, err := backupBlobs.Exists(id)
		if err != nil {
			return err
		}
		if !backupExists {
			continue
		}
		if _, err := liveBlobs.Publish(ctx, id, func(w io.Writer) error {
			f, err := backupBlobs.Open(id)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(w, f)
			return err
		}); err != nil {
			return fmt.Errorf("account: republish restored blob: %w", err)
		}
	}
	return nil
}

// verifyBackupForRestore is VerifyBackup plus the restore-specific
// eligibility check: a backup already known corrupt, or one that just
// failed this verification pass, is ineligible.
func (a *Account) verifyBackupForRestore(ctx context.Context, db store.Executor, backupID model.ID, now time.Time) error {
	if err := a.VerifyBackup(ctx, backupID, now); err != nil {
		return err
	}
	verified, err := store.GetBackup(ctx, db, backupID)
	if err != nil {
		return err
	}
	if verified.Corrupt {
		return ErrBackupCorrupt
	}
	return nil
}

// restoreDatabaseFile atomically replaces the live database file at
// databasePath with preparedPath (already a complete valid SQLCipher
// database encrypted under freshKey) and updates the on-disk key envelope
// to match. On any failure it restores the original file and envelope and
// reopens the original database with the original key, so the account
// remains fully usable with its pre-restore data even though the restore
// did not complete; the returned error in that case still reports the
// failure. The caller must have already closed its live *sql.DB
// connection.
func restoreDatabaseFile(ctx context.Context, databasePath, preparedPath string, wrapper keystore.Wrapper, freshKey *corecrypto.Secret, envelope []byte) (db *sql.DB, err error) {
	envelopeFile := envelopePath(databasePath)
	oldEnvelope, err := os.ReadFile(envelopeFile)
	if err != nil {
		return nil, fmt.Errorf("account: read existing key envelope: %w", err)
	}

	suffix := fmt.Sprintf(".pre-restore-rollback-%d", time.Now().UnixMilli())
	var movedAside []string
	for _, ext := range []string{"", "-wal", "-shm"} {
		src := databasePath + ext
		if _, statErr := os.Stat(src); statErr != nil {
			continue
		}
		dst := src + suffix
		if renameErr := os.Rename(src, dst); renameErr != nil {
			for _, moved := range movedAside {
				os.Rename(moved, strings.TrimSuffix(moved, suffix))
			}
			return nil, fmt.Errorf("account: move current database aside: %w", renameErr)
		}
		movedAside = append(movedAside, dst)
	}

	rollback := func(cause error) (*sql.DB, error) {
		os.Remove(databasePath)
		os.Remove(databasePath + "-wal")
		os.Remove(databasePath + "-shm")
		for _, moved := range movedAside {
			os.Rename(moved, strings.TrimSuffix(moved, suffix))
		}
		if writeErr := os.WriteFile(envelopeFile, oldEnvelope, 0o600); writeErr != nil {
			return nil, fmt.Errorf("%w (additionally failed to restore the key envelope: %v)", cause, writeErr)
		}
		oldKey, _, unwrapErr := store.LoadOrCreateDatabaseKey(ctx, wrapper, localDeviceKeyID, oldEnvelope)
		if unwrapErr != nil {
			return nil, fmt.Errorf("%w (additionally failed to reopen the original database: %v)", cause, unwrapErr)
		}
		reopened, _, openErr := store.Open(ctx, databasePath, oldKey)
		oldKey.Close()
		if openErr != nil {
			return nil, fmt.Errorf("%w (additionally failed to reopen the original database: %v)", cause, openErr)
		}
		return reopened, cause
	}

	if err := os.Rename(preparedPath, databasePath); err != nil {
		return rollback(fmt.Errorf("account: move restored database into place: %w", err))
	}
	if err := os.WriteFile(envelopeFile, envelope, 0o600); err != nil {
		return rollback(fmt.Errorf("account: write restored key envelope: %w", err))
	}
	newDB, _, err := store.Open(ctx, databasePath, freshKey)
	if err != nil {
		return rollback(fmt.Errorf("account: reopen restored database: %w", err))
	}
	for _, moved := range movedAside {
		os.Remove(moved)
	}
	return newDB, nil
}

// checkPlaintextIntegrity runs SQLite's structural integrity check (not
// SQLCipher's cipher_integrity_check, which only applies to an encrypted
// connection) against a decrypted restore source database.
func checkPlaintextIntegrity(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("%w: %v", store.ErrIntegrityCheckFailed, err)
	}
	if result != "ok" {
		return fmt.Errorf("%w: %s", store.ErrIntegrityCheckFailed, result)
	}
	return nil
}

// openBackupPlaintext decrypts and decompresses one backup's snapshot file
// into its plaintext SQLite database bytes.
func openBackupPlaintext(backup store.Backup, rootKey *corecrypto.Secret) ([]byte, error) {
	envelope, err := readBackupFile(filepath.Join(backup.Location, backupSnapshotFile))
	if err != nil {
		return nil, err
	}
	compressed, err := corecrypto.OpenBackup(rootKey, envelope)
	if err != nil {
		return nil, err
	}
	defer clear(compressed)
	return zstdDecompress(compressed, corecrypto.MaxBackupArchiveBytes)
}

// openBackupPlaintextDatabase decrypts and decompresses backup's snapshot,
// stages it to a temporary file, and opens it read-write (restore code
// paths that only read from it simply never write). The caller must call
// closeBackupSource on the result.
func openBackupPlaintextDatabase(backup store.Backup, rootKey *corecrypto.Secret) (db *sql.DB, path string, err error) {
	plaintext, err := openBackupPlaintext(backup, rootKey)
	if err != nil {
		return nil, "", err
	}
	defer clear(plaintext)

	f, err := os.CreateTemp("", "beresta-backup-source-*.db")
	if err != nil {
		return nil, "", fmt.Errorf("account: stage backup source database: %w", err)
	}
	path = f.Name()
	if _, err := f.Write(plaintext); err != nil {
		f.Close()
		os.Remove(path)
		return nil, "", fmt.Errorf("account: stage backup source database: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, "", fmt.Errorf("account: stage backup source database: %w", err)
	}
	db, err = sql.Open("sqlite3", path)
	if err != nil {
		os.Remove(path)
		return nil, "", fmt.Errorf("account: open backup source database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, path, nil
}

func closeBackupSource(db *sql.DB, path string) {
	db.Close()
	os.Remove(path)
}

// backupSourceWorkspaceID returns the single workspace ID present in a
// backup's source database. Every account currently has exactly one
// workspace (sharing/multi-workspace is tasks.md phase 9), so this always
// resolves the workspace a restore is importing into without the caller
// needing to already know its ID.
func backupSourceWorkspaceID(ctx context.Context, sourceDB store.Executor) model.ID {
	var idBytes []byte
	if err := sourceDB.QueryRowContext(ctx, `SELECT id FROM workspaces LIMIT 1`).Scan(&idBytes); err != nil {
		return model.Nil
	}
	id, err := model.ParseID(idBytes)
	if err != nil {
		return model.Nil
	}
	return id
}

// selectSourceNotes returns the notes in sourceDB matching noteIDs, or
// every note in workspaceID when noteIDs is empty.
func selectSourceNotes(ctx context.Context, sourceDB store.Executor, workspaceID model.ID, noteIDs []model.ID) ([]model.Note, error) {
	if len(noteIDs) == 0 {
		return store.ListNotes(ctx, sourceDB, workspaceID)
	}
	notes := make([]model.Note, 0, len(noteIDs))
	for _, id := range noteIDs {
		note, err := store.GetNote(ctx, sourceDB, id)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, nil
}

// workspaceKeysSnapshot returns a shallow copy of the account's current
// workspace key map under lock, so callers already holding no lock (e.g.
// RestoreSelective after its own accountSession snapshot) can look up an
// entry without racing Lock.
func (a *Account) workspaceKeysSnapshot() map[model.ID]workspaceKeyEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot := make(map[model.ID]workspaceKeyEntry, len(a.workspaceKeys))
	for k, v := range a.workspaceKeys {
		snapshot[k] = v
	}
	return snapshot
}
