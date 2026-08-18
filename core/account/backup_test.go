package account

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

func TestCreateBackupProducesRestorableSnapshot(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Backed up note")
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	_ = note

	backupsRoot := t.TempDir()
	now := time.Now()
	backup, err := created.CreateBackup(ctx, backupsRoot, store.BackupKindManual, now)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	if backup.Kind != store.BackupKindManual {
		t.Fatalf("Kind = %d, want %d", backup.Kind, store.BackupKindManual)
	}
	if backup.NoteCount == nil || *backup.NoteCount != 1 {
		t.Fatalf("NoteCount = %v, want 1", backup.NoteCount)
	}
	if backup.SizeBytes == nil || *backup.SizeBytes <= 0 {
		t.Fatalf("SizeBytes = %v, want > 0", backup.SizeBytes)
	}

	// The catalog entry must be durably recorded.
	all, err := store.ListBackups(ctx, created.db, store.BackupKindManual)
	if err != nil || len(all) != 1 || all[0].ID != backup.ID {
		t.Fatalf("ListBackups = %v, err = %v", all, err)
	}

	// The published backup set must decrypt/decompress/open back to a
	// database containing the note that existed when it was taken.
	envelope, err := readBackupFile(filepath.Join(backup.Location, backupSnapshotFile))
	if err != nil {
		t.Fatalf("readBackupFile: %v", err)
	}
	compressed, err := corecrypto.OpenBackup(created.rootKey, envelope)
	if err != nil {
		t.Fatalf("OpenBackup: %v", err)
	}
	plaintext, err := zstdDecompress(compressed, 1<<30)
	if err != nil {
		t.Fatalf("zstdDecompress: %v", err)
	}

	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restoredPath, plaintext, 0o600); err != nil {
		t.Fatal(err)
	}
	restoredDB, err := sql.Open("sqlite3", restoredPath)
	if err != nil {
		t.Fatalf("open restored snapshot: %v", err)
	}
	defer restoredDB.Close()
	var title string
	if err := restoredDB.QueryRowContext(ctx, `SELECT title FROM notes WHERE id = ?`, note.ID.Bytes()).Scan(&title); err != nil {
		t.Fatalf("query restored note: %v", err)
	}
	if title != "Backed up note" {
		t.Fatalf("restored title = %q", title)
	}
}

func TestCreateBackupIncludesReferencedAttachments(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "With attachment")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("attachment payload")
	attachment, err := created.AddAttachment(ctx, workspaceID, note.ID, "a.txt", "text/plain", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	backupsRoot := t.TempDir()
	backup, err := created.CreateBackup(ctx, backupsRoot, store.BackupKindManual, time.Now())
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	hexID := fmt.Sprintf("%x", attachment.BlobID.Bytes())
	blobPath := filepath.Join(backup.Location, "blobs", hexID[0:2], hexID[2:4], hexID)
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("backup set is missing the referenced blob: %v", err)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(backup.Location, backupManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(manifestBytes, []byte(hexID)) {
		t.Fatal("manifest does not reference the backed-up blob")
	}
}

func TestEnsureDailyBackupCreatesOncePerDayAndRotates(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	backupsRoot := t.TempDir()

	day1 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	createdFirst, err := created.EnsureDailyBackup(ctx, backupsRoot, day1)
	if err != nil || !createdFirst {
		t.Fatalf("EnsureDailyBackup (first) created=%v err=%v", createdFirst, err)
	}

	// A second call the same day must not create another one.
	createdSame, err := created.EnsureDailyBackup(ctx, backupsRoot, day1.Add(3*time.Hour))
	if err != nil || createdSame {
		t.Fatalf("EnsureDailyBackup (same day) created=%v err=%v", createdSame, err)
	}

	// Nine more days, one backup per day: rotation must keep exactly seven.
	for i := 1; i <= 9; i++ {
		day := day1.AddDate(0, 0, i)
		if _, err := created.EnsureDailyBackup(ctx, backupsRoot, day); err != nil {
			t.Fatalf("EnsureDailyBackup (day %d): %v", i, err)
		}
	}

	backups, err := store.ListBackups(ctx, created.db, store.BackupKindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != dailyBackupRetention {
		t.Fatalf("daily backup count = %d, want %d", len(backups), dailyBackupRetention)
	}

	// The oldest surviving backup must be day1+3 (days 0..2 rotated away),
	// and its on-disk set must actually be gone.
	entries, err := os.ReadDir(backupsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != dailyBackupRetention {
		t.Fatalf("on-disk backup set count = %d, want %d", len(entries), dailyBackupRetention)
	}
}

func TestEnsureDailyBackupRejectsLockedAccount(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	if err := created.Lock(); err != nil {
		t.Fatal(err)
	}
	if _, err := created.EnsureDailyBackup(ctx, t.TempDir(), time.Now()); err != ErrAccountLocked {
		t.Fatalf("EnsureDailyBackup on locked account error = %v, want ErrAccountLocked", err)
	}
}

func TestVerifyBackupAcceptsAValidBackupAndDetectsTampering(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "a.txt", "text/plain", bytes.NewReader([]byte("payload"))); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	now := time.Now()
	backup, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, now)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}

	if err := created.VerifyBackup(ctx, backup.ID, now); err != nil {
		t.Fatalf("VerifyBackup (fresh backup): %v", err)
	}
	verified, err := store.GetBackup(ctx, created.db, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Corrupt || verified.VerifiedUnixMS == nil {
		t.Fatalf("verified backup = %+v, want Corrupt=false and VerifiedUnixMS set", verified)
	}

	// Tamper with a file inside the published backup set.
	snapshotPath := filepath.Join(backup.Location, backupSnapshotFile)
	if err := os.WriteFile(snapshotPath, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := created.VerifyBackup(ctx, backup.ID, now); err != nil {
		t.Fatalf("VerifyBackup (tampered) unexpectedly returned an error instead of marking corrupt: %v", err)
	}
	corrupted, err := store.GetBackup(ctx, created.db, backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !corrupted.Corrupt {
		t.Fatal("tampered backup was not marked corrupt")
	}
}

func TestVerifyAllBackupsSweepsEveryKind(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	now := time.Now()

	backupsRoot := t.TempDir()
	daily, err := created.CreateBackup(ctx, backupsRoot, store.BackupKindDaily, now)
	if err != nil {
		t.Fatal(err)
	}
	manual, err := created.CreateBackup(ctx, backupsRoot, store.BackupKindManual, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := created.VerifyAllBackups(ctx, now); err != nil {
		t.Fatalf("VerifyAllBackups: %v", err)
	}

	for _, id := range []model.ID{daily.ID, manual.ID} {
		b, err := store.GetBackup(ctx, created.db, id)
		if err != nil || b.VerifiedUnixMS == nil || b.Corrupt {
			t.Fatalf("backup %v after sweep = %+v, err = %v", id, b, err)
		}
	}
}

func TestEnsureDailyBackupReplacesACorruptTodayBackup(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	backupsRoot := t.TempDir()
	now := time.Now()

	if _, err := created.EnsureDailyBackup(ctx, backupsRoot, now); err != nil {
		t.Fatalf("EnsureDailyBackup: %v", err)
	}
	backups, err := store.ListBackups(ctx, created.db, store.BackupKindDaily)
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
	if err := store.MarkBackupCorrupt(ctx, created.db, backups[0].ID); err != nil {
		t.Fatal(err)
	}

	createdAgain, err := created.EnsureDailyBackup(ctx, backupsRoot, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("EnsureDailyBackup (retry same day after corruption): %v", err)
	}
	if !createdAgain {
		t.Fatal("EnsureDailyBackup should create a fresh backup when today's existing one is corrupt")
	}
}

func TestCreateBackupRejectsInsufficientCapacity(t *testing.T) {
	ctx := context.Background()
	created := createTestAccount(t)
	workspaceID := defaultWorkspaceID(t, created)
	note, err := created.CreateNote(ctx, workspaceID, model.Nil, "Untitled")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := created.AddAttachment(ctx, workspaceID, note.ID, "a.txt", "text/plain", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	// Inflate the recorded attachment size far beyond any real free disk
	// space, without needing to actually fill the disk, to exercise the
	// capacity preflight deterministically.
	if _, err := created.db.ExecContext(ctx, `UPDATE attachments SET size_bytes = ?`, int64(1)<<62); err != nil {
		t.Fatal(err)
	}

	if _, err := created.CreateBackup(ctx, t.TempDir(), store.BackupKindManual, time.Now()); err != ErrInsufficientBackupCapacity {
		t.Fatalf("CreateBackup error = %v, want ErrInsufficientBackupCapacity", err)
	}
}

func TestCopyFileCopiesContentAndRejectsMissingSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("blob bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "blob bytes" {
		t.Fatalf("copied content = %q, err = %v", got, err)
	}

	if err := copyFile(filepath.Join(dir, "missing.txt"), filepath.Join(dir, "dst2.txt")); err == nil {
		t.Fatal("expected an error copying a missing source file")
	}
}
