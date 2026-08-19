package account

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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
