package account

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

func TestRunGarbageCollectionDryRunReportsWithoutMutating(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Old deleted note")
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "a.txt", "text/plain", bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if err := created.RemoveAttachment(ctx, workspaceID, note.ID, attachment.BlobID); err != nil {
		t.Fatal(err)
	}
	if err := created.DeleteNote(ctx, workspaceID, note.ID); err != nil {
		t.Fatal(err)
	}

	oldUnixMS := time.Now().Add(-40 * 24 * time.Hour).UnixMilli()
	backdateAttachmentOrphan(t, created, attachment.BlobID, oldUnixMS)
	backdateNoteDeletion(t, created, note.ID, oldUnixMS)

	report, err := created.RunGarbageCollection(ctx, time.Now(), true)
	if err != nil {
		t.Fatalf("RunGarbageCollection (dry run): %v", err)
	}
	if !report.DryRun {
		t.Fatal("DryRun should be true")
	}
	if len(report.Blobs) != 1 || report.Blobs[0].BlobID != attachment.BlobID {
		t.Fatalf("Blobs = %v", report.Blobs)
	}
	if len(report.Notes) != 1 || report.Notes[0].NoteID != note.ID {
		t.Fatalf("Notes = %v", report.Notes)
	}

	// Nothing should actually have been deleted by a dry run.
	if _, err := store.GetAttachment(ctx, created.db, attachment.BlobID); err != nil {
		t.Fatalf("attachment should still exist after dry run: %v", err)
	}
	if _, err := created.GetNote(ctx, note.ID); err != nil {
		t.Fatalf("note should still exist after dry run: %v", err)
	}
}

func TestRunGarbageCollectionCollectsPastRetentionOnly(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	oldNote, err := created.CreateNote(ctx, workspaceID, model.Nil, "Old")
	if err != nil {
		t.Fatal(err)
	}
	oldAttachment, err := created.AddAttachment(ctx, workspaceID, oldNote.ID, "old.txt", "text/plain", bytes.NewReader([]byte("old")))
	if err != nil {
		t.Fatal(err)
	}
	if err := created.RemoveAttachment(ctx, workspaceID, oldNote.ID, oldAttachment.BlobID); err != nil {
		t.Fatal(err)
	}
	if err := created.DeleteNote(ctx, workspaceID, oldNote.ID); err != nil {
		t.Fatal(err)
	}
	oldUnixMS := time.Now().Add(-31 * 24 * time.Hour).UnixMilli()
	backdateAttachmentOrphan(t, created, oldAttachment.BlobID, oldUnixMS)
	backdateNoteDeletion(t, created, oldNote.ID, oldUnixMS)

	recentNote, err := created.CreateNote(ctx, workspaceID, model.Nil, "Recent")
	if err != nil {
		t.Fatal(err)
	}
	recentAttachment, err := created.AddAttachment(ctx, workspaceID, recentNote.ID, "recent.txt", "text/plain", bytes.NewReader([]byte("recent")))
	if err != nil {
		t.Fatal(err)
	}
	if err := created.RemoveAttachment(ctx, workspaceID, recentNote.ID, recentAttachment.BlobID); err != nil {
		t.Fatal(err)
	}
	if err := created.DeleteNote(ctx, workspaceID, recentNote.ID); err != nil {
		t.Fatal(err)
	}
	// Recent deletion/orphan (a few days ago), well inside the 30-day floor.
	recentUnixMS := time.Now().Add(-2 * 24 * time.Hour).UnixMilli()
	backdateAttachmentOrphan(t, created, recentAttachment.BlobID, recentUnixMS)
	backdateNoteDeletion(t, created, recentNote.ID, recentUnixMS)

	report, err := created.RunGarbageCollection(ctx, time.Now(), false)
	if err != nil {
		t.Fatalf("RunGarbageCollection: %v", err)
	}
	if len(report.Blobs) != 1 || report.Blobs[0].BlobID != oldAttachment.BlobID {
		t.Fatalf("collected blobs = %v, want only the old one", report.Blobs)
	}
	if len(report.Notes) != 1 || report.Notes[0].NoteID != oldNote.ID {
		t.Fatalf("collected notes = %v, want only the old one", report.Notes)
	}

	if _, err := store.GetAttachment(ctx, created.db, oldAttachment.BlobID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old attachment error = %v, want ErrNotFound", err)
	}
	if _, err := created.GetNote(ctx, oldNote.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("old note error = %v, want ErrNotFound", err)
	}
	exists, err := created.blobs.Exists(oldAttachment.BlobID)
	if err != nil || exists {
		t.Fatalf("old blob file exists = %v, err = %v, want gone", exists, err)
	}

	// The recent one must survive: still within the 30-day floor.
	if _, err := store.GetAttachment(ctx, created.db, recentAttachment.BlobID); err != nil {
		t.Fatalf("recent attachment should survive: %v", err)
	}
	if _, err := created.GetNote(ctx, recentNote.ID); err != nil {
		t.Fatalf("recent note should survive: %v", err)
	}
}

func TestRunGarbageCollectionReportsBackupAwareness(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)

	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "a.txt", "text/plain", bytes.NewReader([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}

	// Take a backup while the attachment is still referenced, so the
	// backup set has its own copy of the blob.
	if _, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, time.Now()); err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	if err := created.RemoveAttachment(ctx, workspaceID, note.ID, attachment.BlobID); err != nil {
		t.Fatal(err)
	}
	oldUnixMS := time.Now().Add(-40 * 24 * time.Hour).UnixMilli()
	backdateAttachmentOrphan(t, created, attachment.BlobID, oldUnixMS)

	report, err := created.RunGarbageCollection(ctx, time.Now(), true)
	if err != nil {
		t.Fatalf("RunGarbageCollection: %v", err)
	}
	if len(report.Blobs) != 1 || !report.Blobs[0].InAnyBackup {
		t.Fatalf("Blobs = %+v, want InAnyBackup=true", report.Blobs)
	}
}

// TestRunGarbageCollectionReclaimsAPublishedBlobFileWithNoAttachmentRow
// covers task 5.1's recovery-cleanup gap: a blob published (via
// BlobStore.Publish) but never followed by a committed attachments row -
// exactly what a crash between AddAttachment's Publish call and its
// CreateAttachment call leaves behind - has no row for
// ListOrphanedAttachments to ever find, so without this it would sit on
// disk forever, unreachable and un-reclaimable (core/account's own
// ErrAttachmentBlobOrphaned doc comment promises garbage collection will
// eventually reclaim it, so this failing would make that promise false).
func TestRunGarbageCollectionReclaimsAPublishedBlobFileWithNoAttachmentRow(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)

	blobID := publishOrphanBlobFile(t, created, 0xAB, []byte("orphaned content"))
	backdatePublishedBlob(t, created, blobID, time.Now().Add(-40*24*time.Hour))

	report, err := created.RunGarbageCollection(ctx, time.Now(), false)
	if err != nil {
		t.Fatalf("RunGarbageCollection: %v", err)
	}
	found := false
	for _, candidate := range report.Blobs {
		if candidate.BlobID == blobID {
			found = true
		}
	}
	if !found {
		t.Fatalf("report.Blobs = %+v, want the rowless blob included", report.Blobs)
	}
	if published, err := created.blobs.Exists(blobID); err != nil || published {
		t.Fatalf("blob file still exists after collection: published=%v err=%v", published, err)
	}
}

// TestRunGarbageCollectionKeepsARecentlyPublishedBlobFileWithNoRowYet is
// the safety-margin half of the fix above: a blob published moments ago
// with no row yet is indistinguishable, by file inspection alone, from a
// legitimate AddAttachment call still inside its normal (sub-second)
// publish-then-commit window. Collecting it immediately would delete
// content out from under that in-flight call. Only the same retention
// window ListOrphanedAttachments already uses makes it eligible.
func TestRunGarbageCollectionKeepsARecentlyPublishedBlobFileWithNoRowYet(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)

	blobID := publishOrphanBlobFile(t, created, 0xCD, []byte("still in flight"))

	report, err := created.RunGarbageCollection(ctx, time.Now(), false)
	if err != nil {
		t.Fatalf("RunGarbageCollection: %v", err)
	}
	for _, candidate := range report.Blobs {
		if candidate.BlobID == blobID {
			t.Fatalf("report.Blobs = %+v, want the recently published blob excluded", report.Blobs)
		}
	}
	if published, err := created.blobs.Exists(blobID); err != nil || !published {
		t.Fatalf("blob file should still exist: published=%v err=%v", published, err)
	}
}

// publishOrphanBlobFile publishes content directly through the account's
// BlobStore, bypassing AddAttachment entirely, to simulate the file-only
// state a crash between Publish and CreateAttachment leaves behind: a
// published blob with no attachments row at all.
func publishOrphanBlobFile(t *testing.T, a *Account, seed byte, content []byte) store.BlobID {
	t.Helper()
	raw := bytes.Repeat([]byte{seed}, store.BlobIDBytes)
	blobID, err := store.ParseBlobID(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.blobs.Publish(context.Background(), blobID, func(w io.Writer) error {
		_, err := w.Write(content)
		return err
	}); err != nil {
		t.Fatalf("publish orphan blob file: %v", err)
	}
	return blobID
}

// backdatePublishedBlob sets a published blob file's modification time,
// standing in for RunGarbageCollection's real safety margin: only a file
// old enough that it cannot be a call still inside its normal
// publish-then-commit window is eligible for collection.
func backdatePublishedBlob(t *testing.T, a *Account, blobID store.BlobID, modified time.Time) {
	t.Helper()
	if err := os.Chtimes(a.blobs.Path(blobID), modified, modified); err != nil {
		t.Fatalf("backdate published blob: %v", err)
	}
}

func backdateAttachmentOrphan(t *testing.T, a *Account, blobID store.BlobID, unixMS int64) {
	t.Helper()
	if _, err := a.db.Exec(`UPDATE attachments SET orphaned_unix_ms = ? WHERE blob_id = ?`, unixMS, blobID.Bytes()); err != nil {
		t.Fatalf("backdate attachment orphan: %v", err)
	}
}

func backdateNoteDeletion(t *testing.T, a *Account, noteID model.ID, unixMS int64) {
	t.Helper()
	if _, err := a.db.Exec(`UPDATE notes SET deleted_physical_ms = ? WHERE id = ?`, unixMS, noteID.Bytes()); err != nil {
		t.Fatalf("backdate note deletion: %v", err)
	}
}
