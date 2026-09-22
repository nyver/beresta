package store

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
)

// TestBlobStoreDetectsACorruptedPublishedBlobOnRead is task 11.5's literal
// byte-level "corrupt cache" fault-injection coverage. It is distinct from
// the missing/orphaned-row drift core/account/datacheck_test.go and
// core/store/datacheck_test.go already cover (a logical inconsistency
// between the database and a regenerable index): this proves that
// ciphertext bytes changing after a successful, durable Publish - the real
// shape of disk bit-rot, an external tamper, or any other on-disk
// corruption of a locally cached encrypted attachment - is detected rather
// than silently decrypted into corrupted plaintext. It combines two
// CGO-free packages directly (this package's filesystem-only BlobStore and
// core/crypto's chunk sealing/verification), so unlike most of this
// package's SQLCipher-backed sibling tests, it runs and is verified in
// every environment, including one with no C compiler available.
func TestBlobStoreDetectsACorruptedPublishedBlobOnRead(t *testing.T) {
	blobStore, _ := newTestBlobStore(t)
	sealer := corecrypto.NewAttachmentChunkSealer()
	workspaceKey, err := corecrypto.TakeSecret(bytes.Repeat([]byte{0x42}, corecrypto.HKDFKeyBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer workspaceKey.Close()

	id := testBlobID(t, 99)
	metadata := corecrypto.AttachmentMetadata{
		SchemaVersion: corecrypto.AttachmentSchemaVersion,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   bytes.Repeat([]byte{0x01}, corecrypto.WorkspaceIDBytes),
		BlobID:        id.Bytes(),
		KeyID:         bytes.Repeat([]byte{0x02}, corecrypto.KeyIDBytes),
	}
	plaintext := bytes.Repeat([]byte("beresta-attachment-chunk-"), 1000)
	chunk, err := sealer.SealChunk(workspaceKey, metadata, 0, plaintext)
	if err != nil {
		t.Fatalf("SealChunk() error = %v", err)
	}

	// Publish the sealed ciphertext exactly as a real attachment write
	// would: the published file on disk is the ciphertext, with the nonce
	// and hash kept separately (here, still on chunk.Nonce/
	// CiphertextSHA256, as a real caller would keep them in the database).
	if _, err := blobStore.Publish(context.Background(), id, func(w io.Writer) error {
		_, err := w.Write(chunk.Ciphertext)
		return err
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	// Sanity check before corrupting anything: the freshly published blob
	// still decrypts correctly.
	if _, err := corecrypto.OpenChunk(workspaceKey, chunk); err != nil {
		t.Fatalf("OpenChunk() on the freshly sealed chunk error = %v, want success", err)
	}

	// Simulate cache/disk corruption: flip one byte directly in the
	// published file, bypassing BlobStore entirely - the same effect real
	// bit-rot or an external tamper would have, and something no durable-
	// write fault injection (which only covers the write path itself) can
	// catch.
	published := blobStore.Path(id)
	onDisk, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	onDisk[len(onDisk)/2] ^= 0xFF
	if err := os.WriteFile(published, onDisk, 0o600); err != nil {
		t.Fatal(err)
	}

	reread, err := blobStore.Open(id)
	if err != nil {
		t.Fatalf("Open() on the corrupted blob error = %v, want success (corruption is caught at decrypt time, not read time)", err)
	}
	corruptedCiphertext, err := io.ReadAll(reread)
	reread.Close()
	if err != nil {
		t.Fatal(err)
	}

	corruptedChunk := chunk
	corruptedChunk.Ciphertext = corruptedCiphertext
	if _, err := corecrypto.OpenChunk(workspaceKey, corruptedChunk); err == nil {
		t.Fatal("OpenChunk() on a corrupted cached blob succeeded, want a verification error")
	}
}
