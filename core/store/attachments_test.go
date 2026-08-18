package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func testBlobID(t testing.TB, seed byte) BlobID {
	t.Helper()
	sum := sha256.Sum256([]byte{seed})
	id, err := ParseBlobID(sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCreateAttachmentDedupesOnBlobID(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	blobID := testBlobID(t, 1)

	first, err := CreateAttachment(ctx, db, workspaceID, blobID, []byte("key1"), []byte("manifest-v1"), 100, 1, 1000)
	if err != nil {
		t.Fatalf("CreateAttachment() error = %v", err)
	}
	second, err := CreateAttachment(ctx, db, workspaceID, blobID, []byte("key2"), []byte("manifest-v2"), 200, 2, 2000)
	if err != nil {
		t.Fatalf("CreateAttachment() second call error = %v", err)
	}
	if string(second.Manifest) != "manifest-v1" || second.SizeBytes != 100 || second.ChunkCount != 1 {
		t.Fatalf("CreateAttachment() dedup = %+v, want the first call's row unchanged (%+v)", second, first)
	}
}

func TestGetAttachmentNotFound(t *testing.T) {
	db := repoTestDB(t)
	_, err := GetAttachment(context.Background(), db, testBlobID(t, 2))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAttachment() error = %v, want ErrNotFound", err)
	}
}

func TestSetNoteAttachmentReferenceAndOrphanTracking(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	blobID := testBlobID(t, 3)

	if _, err := CreateAttachment(ctx, db, workspaceID, blobID, []byte("key"), []byte("manifest"), 10, 1, 1000); err != nil {
		t.Fatal(err)
	}
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "note with attachment", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	// Attaching leaves the attachment referenced (not orphaned).
	if err := SetNoteAttachment(ctx, db, note.ID, blobID, true, repoClock(t, 10, 0, 0x02), 1500); err != nil {
		t.Fatalf("SetNoteAttachment(present) error = %v", err)
	}
	got, err := GetAttachment(ctx, db, blobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OrphanedUnixMS != nil {
		t.Fatalf("GetAttachment() OrphanedUnixMS = %v, want nil while referenced", got.OrphanedUnixMS)
	}
	ids, err := NoteAttachmentBlobIDs(ctx, db, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != blobID {
		t.Fatalf("NoteAttachmentBlobIDs() = %v, want [%v]", ids, blobID)
	}

	// Removing the only reference marks it orphaned at the given time.
	if err := SetNoteAttachment(ctx, db, note.ID, blobID, false, repoClock(t, 20, 0, 0x02), 2000); err != nil {
		t.Fatalf("SetNoteAttachment(absent) error = %v", err)
	}
	got, err = GetAttachment(ctx, db, blobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OrphanedUnixMS == nil || *got.OrphanedUnixMS != 2000 {
		t.Fatalf("GetAttachment() OrphanedUnixMS = %v, want 2000", got.OrphanedUnixMS)
	}

	// Re-attaching clears the orphan mark again.
	if err := SetNoteAttachment(ctx, db, note.ID, blobID, true, repoClock(t, 30, 0, 0x02), 2500); err != nil {
		t.Fatalf("SetNoteAttachment(present again) error = %v", err)
	}
	got, err = GetAttachment(ctx, db, blobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OrphanedUnixMS != nil {
		t.Fatalf("GetAttachment() OrphanedUnixMS = %v, want nil after re-attaching", got.OrphanedUnixMS)
	}
}

func TestListOrphanedAttachments(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	note, err := CreateNote(ctx, db, workspaceID, model.Nil, "note", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}

	oldOrphan := testBlobID(t, 4)
	recentOrphan := testBlobID(t, 5)
	stillReferenced := testBlobID(t, 6)
	for _, id := range []BlobID{oldOrphan, recentOrphan, stillReferenced} {
		if _, err := CreateAttachment(ctx, db, workspaceID, id, []byte("key"), []byte("manifest"), 10, 1, 1000); err != nil {
			t.Fatal(err)
		}
		if err := SetNoteAttachment(ctx, db, note.ID, id, true, repoClock(t, 10, 0, 0x02), 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetNoteAttachment(ctx, db, note.ID, oldOrphan, false, repoClock(t, 20, 0, 0x02), 5000); err != nil {
		t.Fatal(err)
	}
	if err := SetNoteAttachment(ctx, db, note.ID, recentOrphan, false, repoClock(t, 21, 0, 0x02), 9000); err != nil {
		t.Fatal(err)
	}

	orphaned, err := ListOrphanedAttachments(ctx, db, workspaceID, 8000)
	if err != nil {
		t.Fatalf("ListOrphanedAttachments() error = %v", err)
	}
	if len(orphaned) != 1 || orphaned[0].BlobID != oldOrphan {
		t.Fatalf("ListOrphanedAttachments() = %+v, want just the attachment orphaned at or before the cutoff", orphaned)
	}
}

// TestSetNoteAttachmentComposesWithNoteCreationInOneTransaction proves the
// "reference transactions" requirement: a note's creation and its
// attachment reference commit or roll back together, exactly like
// TestRepositoryFunctionsComposeIntoOneTransaction proves for other
// repository calls sharing one *sql.Tx.
func TestSetNoteAttachmentComposesWithNoteCreationInOneTransaction(t *testing.T) {
	db := repoTestDB(t)
	ctx := context.Background()
	workspaceID := seedWorkspace(t, db)
	blobID := testBlobID(t, 7)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateAttachment(ctx, tx, workspaceID, blobID, []byte("key"), []byte("manifest"), 10, 1, 1000); err != nil {
		t.Fatal(err)
	}
	note, err := CreateNote(ctx, tx, workspaceID, model.Nil, "note", repoClock(t, 1, 0, 0x02))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetNoteAttachment(ctx, tx, note.ID, blobID, true, repoClock(t, 2, 0, 0x02), 1000); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	if _, err := GetAttachment(ctx, db, blobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAttachment() after rollback error = %v, want ErrNotFound", err)
	}
	if _, err := GetNote(ctx, db, note.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetNote() after rollback error = %v, want ErrNotFound", err)
	}
}
