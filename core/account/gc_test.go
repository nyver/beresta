package account

import (
	"bytes"
	"context"
	"errors"
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
