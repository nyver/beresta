package account

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

func TestAddReadRemoveAttachment(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	content := bytes.Repeat([]byte("beresta attachment content "), 4096) // multi-KB, single chunk
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "notes.txt", "text/plain", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if attachment.WorkspaceID != workspaceID {
		t.Fatalf("attachment workspace = %v", attachment.WorkspaceID)
	}

	ids, err := store.NoteAttachmentBlobIDs(ctx, created.db, note.ID)
	if err != nil || len(ids) != 1 || ids[0] != attachment.BlobID {
		t.Fatalf("NoteAttachmentBlobIDs = %v, err = %v", ids, err)
	}

	var out bytes.Buffer
	name, mediaType, err := created.ReadAttachment(ctx, workspaceID, attachment.BlobID, &out)
	if err != nil {
		t.Fatalf("ReadAttachment: %v", err)
	}
	if name != "notes.txt" || mediaType != "text/plain" {
		t.Fatalf("name=%q mediaType=%q", name, mediaType)
	}
	if !bytes.Equal(out.Bytes(), content) {
		t.Fatal("read attachment content does not match what was added")
	}

	if err := created.RemoveAttachment(ctx, workspaceID, note.ID, attachment.BlobID); err != nil {
		t.Fatalf("RemoveAttachment: %v", err)
	}
	ids, err = store.NoteAttachmentBlobIDs(ctx, created.db, note.ID)
	if err != nil || len(ids) != 0 {
		t.Fatalf("NoteAttachmentBlobIDs after remove = %v, err = %v", ids, err)
	}

	// The blob itself is left in place for garbage collection, not deleted
	// immediately by RemoveAttachment.
	exists, err := created.blobs.Exists(attachment.BlobID)
	if err != nil || !exists {
		t.Fatalf("blob should still exist after remove: exists=%v err=%v", exists, err)
	}
}

func TestListNoteAttachments(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	empty, err := created.ListNoteAttachments(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatalf("ListNoteAttachments (empty): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListNoteAttachments (empty) = %v, want none", empty)
	}

	content := []byte("photo bytes")
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "photo.png", "image/png", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	infos, err := created.ListNoteAttachments(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatalf("ListNoteAttachments: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("ListNoteAttachments = %v, want 1 entry", infos)
	}
	got := infos[0]
	if got.BlobID != attachment.BlobID || got.DisplayName != "photo.png" || got.MediaType != "image/png" || got.SizeBytes != uint64(len(content)) {
		t.Fatalf("ListNoteAttachments[0] = %+v", got)
	}

	if err := created.RemoveAttachment(ctx, workspaceID, note.ID, attachment.BlobID); err != nil {
		t.Fatalf("RemoveAttachment: %v", err)
	}
	afterRemove, err := created.ListNoteAttachments(ctx, workspaceID, note.ID)
	if err != nil {
		t.Fatalf("ListNoteAttachments (after remove): %v", err)
	}
	if len(afterRemove) != 0 {
		t.Fatalf("ListNoteAttachments (after remove) = %v, want none", afterRemove)
	}
}

func TestListNoteAttachmentsRejectsUnknownNote(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	unknownNote, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.ListNoteAttachments(ctx, workspaceID, unknownNote); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListNoteAttachments(unknown note) err = %v, want ErrNotFound", err)
	}
}

func TestAddAttachmentDedupsIdenticalContent(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	noteA, err := created.CreateNote(ctx, workspaceID, model.Nil, "A")
	if err != nil {
		t.Fatal(err)
	}
	noteB, err := created.CreateNote(ctx, workspaceID, model.Nil, "B")
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("identical content")
	first, err := created.AddAttachment(ctx, workspaceID, noteA.ID, "a.txt", "text/plain", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("first AddAttachment: %v", err)
	}
	second, err := created.AddAttachment(ctx, workspaceID, noteB.ID, "b.txt", "text/plain", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("second AddAttachment: %v", err)
	}
	if first.BlobID != second.BlobID {
		t.Fatal("identical content should dedup to the same BlobID")
	}

	var attachmentRows int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments`).Scan(&attachmentRows); err != nil {
		t.Fatal(err)
	}
	if attachmentRows != 1 {
		t.Fatalf("attachment rows = %d, want 1", attachmentRows)
	}

	// The manifest that comes back for the second attach is the first
	// attach's display name, not the second's: dedup reuses the existing
	// manifest rather than re-encrypting.
	var out bytes.Buffer
	name, _, err := created.ReadAttachment(ctx, workspaceID, second.BlobID, &out)
	if err != nil {
		t.Fatalf("ReadAttachment: %v", err)
	}
	if name != "a.txt" {
		t.Fatalf("name = %q, want %q", name, "a.txt")
	}
}

func TestAddAttachmentEmptyContent(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}

	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "empty.txt", "text/plain", bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("AddAttachment (empty): %v", err)
	}
	if attachment.SizeBytes != 0 || attachment.ChunkCount != 1 {
		t.Fatalf("attachment = %+v, want SizeBytes=0 ChunkCount=1", attachment)
	}

	var out bytes.Buffer
	if _, _, err := created.ReadAttachment(ctx, workspaceID, attachment.BlobID, &out); err != nil {
		t.Fatalf("ReadAttachment (empty): %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("read %d bytes, want 0", out.Len())
	}
}

func TestAddAttachmentRejectsInvalidMetadata(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "", "text/plain", strings.NewReader("x")); !errors.Is(err, ErrInvalidAttachmentMetadata) {
		t.Fatalf("empty display name error = %v, want ErrInvalidAttachmentMetadata", err)
	}
	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "a/b.txt", "text/plain", strings.NewReader("x")); !errors.Is(err, ErrInvalidAttachmentMetadata) {
		t.Fatalf("path-separator display name error = %v, want ErrInvalidAttachmentMetadata", err)
	}
	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "a.txt", "", strings.NewReader("x")); !errors.Is(err, ErrInvalidAttachmentMetadata) {
		t.Fatalf("empty media type error = %v, want ErrInvalidAttachmentMetadata", err)
	}
}

// TestCheckAttachmentSizeRejectsOversizedPlaintext covers task 5.1's
// preflight size check: a caller that knows a source's size up front (a
// file picker's os.Stat, a paste/drop's byte length) can reject it before
// staging or encrypting any of it, rather than discovering the same limit
// only after copying up to corecrypto.MaxAttachmentPlaintextBytes to disk.
func TestCheckAttachmentSizeRejectsOversizedPlaintext(t *testing.T) {
	if err := CheckAttachmentSize(1024); err != nil {
		t.Fatalf("CheckAttachmentSize(1024) = %v, want nil", err)
	}
	oversized := uint64(1) << 62
	if err := CheckAttachmentSize(oversized); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Fatalf("CheckAttachmentSize(oversized) = %v, want ErrAttachmentTooLarge", err)
	}
}

// TestCheckAttachmentCapacityRejectsWhenTheVolumeCannotFitIt covers task
// 5.1's preflight disk-space check, using the same deterministic
// technique TestCreateBackupRejectsInsufficientCapacity uses: an
// absurdly large requested size guarantees the real free-space query
// reports insufficient capacity without needing to actually fill a disk.
func TestCheckAttachmentCapacityRejectsWhenTheVolumeCannotFitIt(t *testing.T) {
	created := createTestAccount(t)

	if err := created.CheckAttachmentCapacity(1024); err != nil {
		t.Fatalf("CheckAttachmentCapacity(1024) = %v, want nil", err)
	}
	impossible := uint64(1) << 62
	if err := created.CheckAttachmentCapacity(impossible); !errors.Is(err, ErrInsufficientAttachmentCapacity) {
		t.Fatalf("CheckAttachmentCapacity(impossible) = %v, want ErrInsufficientAttachmentCapacity", err)
	}
}

func TestAttachmentManifestEncodeDecodeRoundTrips(t *testing.T) {
	payload := attachmentManifestPayload{
		plaintextSize: 5_000_000,
		mediaType:     "image/png",
		displayName:   "photo.png",
		chunks: []attachmentChunkRecord{
			{ciphertextSize: 4*1024*1024 + 16, plaintextSize: 4 * 1024 * 1024},
			{ciphertextSize: 1000, plaintextSize: 984},
		},
	}
	for i := range payload.chunks {
		payload.chunks[i].nonce[0] = byte(i + 1)
		payload.chunks[i].ciphertextSHA256[0] = byte(i + 2)
	}

	encoded, err := encodeAttachmentManifest(payload)
	if err != nil {
		t.Fatalf("encodeAttachmentManifest: %v", err)
	}
	decoded, err := decodeAttachmentManifest(encoded)
	if err != nil {
		t.Fatalf("decodeAttachmentManifest: %v", err)
	}
	if decoded.plaintextSize != payload.plaintextSize || decoded.mediaType != payload.mediaType || decoded.displayName != payload.displayName {
		t.Fatalf("decoded = %+v", decoded)
	}
	if len(decoded.chunks) != len(payload.chunks) {
		t.Fatalf("decoded chunk count = %d, want %d", len(decoded.chunks), len(payload.chunks))
	}
	for i := range payload.chunks {
		if decoded.chunks[i] != payload.chunks[i] {
			t.Fatalf("chunk %d = %+v, want %+v", i, decoded.chunks[i], payload.chunks[i])
		}
	}
}

// failingReaderAfter returns the bytes in remaining, then err on every
// subsequent read, simulating a source that fails partway through (a
// disk-full or permission-revoked condition on the volume the source file
// itself lives on, or a removable/network device failing mid-read).
type failingReaderAfter struct {
	remaining []byte
	err       error
}

func (r *failingReaderAfter) Read(p []byte) (int, error) {
	if len(r.remaining) == 0 {
		return 0, r.err
	}
	n := copy(p, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}

// TestAddAttachmentSourceReadFailureDiskFullLeavesNoRowOrBlob and
// TestAddAttachmentSourceReadFailurePermissionDeniedLeavesNoRowOrBlob cover
// task 5.2's "before encryption" boundary: stagePlaintext buffers the
// entire source into a staging temp file before AddAttachment ever computes
// a content address or seals a chunk, so a read failure partway through
// that copy must fail closed before any row, blob, or leftover staging file
// exists — and the caller must see the underlying OS condition, not a
// generic error.
func TestAddAttachmentSourceReadFailureDiskFullLeavesNoRowOrBlob(t *testing.T) {
	testAddAttachmentSourceReadFailureLeavesNoRowOrBlob(t, syscall.ENOSPC)
}

func TestAddAttachmentSourceReadFailurePermissionDeniedLeavesNoRowOrBlob(t *testing.T) {
	testAddAttachmentSourceReadFailureLeavesNoRowOrBlob(t, syscall.EACCES)
}

func testAddAttachmentSourceReadFailureLeavesNoRowOrBlob(t *testing.T, cause error) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}

	source := &failingReaderAfter{
		remaining: []byte("partial content read before the fault"),
		err:       &os.PathError{Op: "read", Path: "source", Err: cause},
	}
	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "notes.txt", "text/plain", source); !errors.Is(err, cause) {
		t.Fatalf("AddAttachment() error = %v, want it to wrap %v", err, cause)
	}

	var attachmentRows int
	if err := created.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments`).Scan(&attachmentRows); err != nil {
		t.Fatal(err)
	}
	if attachmentRows != 0 {
		t.Fatalf("attachment rows = %d, want 0 after a staging read failure", attachmentRows)
	}
	ids, err := store.NoteAttachmentBlobIDs(ctx, created.db, note.ID)
	if err != nil || len(ids) != 0 {
		t.Fatalf("NoteAttachmentBlobIDs = %v, err = %v, want none", ids, err)
	}
	published, err := created.blobs.ListPublished()
	if err != nil || len(published) != 0 {
		t.Fatalf("ListPublished() = %v, err = %v, want none", published, err)
	}
	entries, err := os.ReadDir(created.blobs.TempDir())
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read staging directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging directory has %d leftover entries after a failed stage, want 0", len(entries))
	}
}

// TestAddAttachmentCrashBetweenPublishAndNoteReferenceCommitRecoversOnRetry
// covers task 5.2's database-reference-commit boundary. AddAttachment
// durably publishes the blob and commits its attachments row
// (publishAndRecordAttachment) before committing the note's separate
// reference to it (commitNoteMetadata), as two different local
// transactions. A crash between the two leaves a fully valid,
// non-placeholder attachment row that no note references yet. This test
// reproduces exactly that state by calling the same unexported publish step
// AddAttachment itself uses and stopping there (standing in for the
// process dying before commitNoteMetadata ever runs), then proves the
// documented recovery path: a retried AddAttachment call with the same
// content and note finds the already-published, already-recorded blob by
// its content address and only needs to complete the missing note
// reference, without re-publishing or re-encrypting.
func TestAddAttachmentCrashBetweenPublishAndNoteReferenceCommitRecoversOnRetry(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("crash between publish and note reference commit")

	db, entry, _, _, err := created.workspaceSession(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := created.stagePlaintext(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		staged.Close()
		os.Remove(staged.Name())
	}()
	blobIDBytes, totalSize, err := corecrypto.ComputeBlobID(ctx, corecrypto.CryptoProfileV1, entry.Key, workspaceID.Bytes(), staged)
	if err != nil {
		t.Fatal(err)
	}
	blobID, err := store.ParseBlobID(blobIDBytes)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := created.publishAndRecordAttachment(ctx, db, entry, workspaceID, blobID, staged, totalSize, "notes.txt", "text/plain"); err != nil {
		t.Fatalf("publishAndRecordAttachment: %v", err)
	}

	// The row and blob exist, but the note does not reference it yet - the
	// simulated crash window.
	row, err := store.GetAttachment(ctx, created.db, blobID)
	if err != nil {
		t.Fatalf("GetAttachment after simulated crash: %v", err)
	}
	if row.IsPlaceholder() {
		t.Fatal("attachment row should have a full manifest, not a placeholder")
	}
	ids, err := store.NoteAttachmentBlobIDs(ctx, created.db, note.ID)
	if err != nil || len(ids) != 0 {
		t.Fatalf("NoteAttachmentBlobIDs before retry = %v, err = %v, want none", ids, err)
	}

	// The retried call (same note, content, and metadata a UI retry would
	// resend) must recover cleanly: dedup finds the existing published,
	// recorded blob and only completes the missing note reference.
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "notes.txt", "text/plain", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AddAttachment retry after simulated crash: %v", err)
	}
	if attachment.BlobID != blobID {
		t.Fatalf("retried AddAttachment BlobID = %x, want %x", attachment.BlobID, blobID)
	}
	ids, err = store.NoteAttachmentBlobIDs(ctx, created.db, note.ID)
	if err != nil || len(ids) != 1 || ids[0] != blobID {
		t.Fatalf("NoteAttachmentBlobIDs after retry = %v, err = %v, want [%x]", ids, err, blobID)
	}

	var out bytes.Buffer
	if _, _, err := created.ReadAttachment(ctx, workspaceID, blobID, &out); err != nil {
		t.Fatalf("ReadAttachment after recovered commit: %v", err)
	}
	if !bytes.Equal(out.Bytes(), content) {
		t.Fatal("recovered attachment content does not match what was staged before the simulated crash")
	}
}
