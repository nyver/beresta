package store

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
)

// TestSQLCipherFileHidesFTSAndNoteContent proves the "FTS isolation inside
// SQLCipher" property: notes_fts is a standalone table inside the same
// SQLCipher-encrypted file as everything else (see the migration comment
// on notes_fts), not a separate unencrypted index file, so a secret
// title/body indexed for full-text search never appears as recoverable
// plaintext in the raw database file on disk.
func TestSQLCipherFileHidesFTSAndNoteContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "beresta.db")
	key := testDatabaseKey(t, 0x70)
	defer key.Close()
	ctx := context.Background()

	db, _, err := Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)
	const secret = "zzz-super-secret-recipe-for-invisible-ink-zzz"
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, secret, repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, db, note.ID, secret, secret); err != nil {
		t.Fatal(err)
	}

	// Force everything out of the WAL into the main file so a raw byte scan
	// of just path (not a -wal sidecar) is a meaningful check either way.
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("secret note title/body is recoverable as plaintext in the raw encrypted database file")
	}
}

// TestStolenProfileDirectoryHidesPlaintext is the whole-directory version
// of the same property (docs/threat-model.md's "Stolen client storage"
// scenario): every file a client profile writes — the encrypted database
// and a published attachment blob, sealed exactly as the real attachment
// path would seal it via core/crypto — must be scanned without finding a
// plaintext secret an attacker who steals the directory should never
// recover.
func TestStolenProfileDirectoryHidesPlaintext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "beresta.db")
	blobRoot := filepath.Join(dir, "blobs")
	blobTmp := filepath.Join(dir, "runtime")
	key := testDatabaseKey(t, 0x71)
	defer key.Close()
	ctx := context.Background()

	db, _, err := Open(ctx, dbPath, key)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := seedWorkspace(t, db)

	const secretTitle = "zzz-stolen-directory-note-title-zzz"
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, secretTitle, repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := ReplaceNoteFTS(ctx, db, note.ID, secretTitle, secretTitle); err != nil {
		t.Fatal(err)
	}

	workspaceKey, err := corecrypto.TakeSecret(bytesOf(0x05, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer workspaceKey.Close()
	const secretAttachment = "zzz-stolen-directory-attachment-body-zzz"
	blobIDBytes, _, err := corecrypto.ComputeBlobID(ctx, corecrypto.CryptoProfileV1, workspaceKey, workspaceID.Bytes(), bytes.NewReader([]byte(secretAttachment)))
	if err != nil {
		t.Fatal(err)
	}
	blobID, err := ParseBlobID(blobIDBytes)
	if err != nil {
		t.Fatal(err)
	}
	metadata := corecrypto.AttachmentMetadata{
		SchemaVersion: corecrypto.AttachmentSchemaVersion,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		BlobID:        blobIDBytes,
		KeyID:         bytesOf(0x01, corecrypto.KeyIDBytes),
	}
	sealer := corecrypto.NewAttachmentChunkSealer()
	chunk, err := sealer.SealChunk(workspaceKey, metadata, 0, []byte(secretAttachment))
	if err != nil {
		t.Fatal(err)
	}

	blobStore := NewBlobStore(blobRoot, blobTmp)
	if _, err := blobStore.Publish(ctx, blobID, func(w io.Writer) error {
		_, werr := w.Write(chunk.Ciphertext)
		return werr
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	markers := [][]byte{[]byte(secretTitle), []byte(secretAttachment)}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, marker := range markers {
			if bytes.Contains(raw, marker) {
				t.Fatalf("file %s contains a recoverable plaintext marker %q", path, marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
