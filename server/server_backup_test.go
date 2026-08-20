package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerBackupRestoresDestroyedStateWithMatchingHashes(t *testing.T) {
	runtime := newTestRuntime(t)
	actor := registerTestActor(t, runtime, "Backup owner")
	chunk := []byte("opaque backup chunk")
	chunkDigest := sha256.Sum256(chunk)
	blobDigest := sha256.Sum256([]byte("backup blob identifier"))
	blobID := hex.EncodeToString(blobDigest[:])
	_, err := runtime.Storage.BeginBlob(context.Background(), actor.UserID, BlobInit{
		WorkspaceID:       actor.WorkspaceID,
		BlobID:            blobID,
		KeyID:             actor.KeyID,
		EncryptedManifest: []byte("opaque manifest"),
		TotalBytes:        int64(len(chunk)),
		Chunks:            []BlobChunkSpec{{Index: 0, Bytes: int64(len(chunk)), SHA256: hex.EncodeToString(chunkDigest[:])}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Storage.PutBlobChunk(context.Background(), actor.UserID, actor.WorkspaceID, blobID, 0, chunk, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Storage.CompleteBlob(context.Background(), actor.UserID, actor.WorkspaceID, blobID, time.Now()); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "server-backups")
	backup, err := runtime.Storage.CreateServerBackup(context.Background(), destination, "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dataRoot := runtime.DataDirectory
	cfg := runtime.Config
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dataRoot, "beresta.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dataRoot, "blobs")); err != nil {
		t.Fatal(err)
	}
	if err := RestoreServerBackup(dataRoot, backup.Path); err != nil {
		t.Fatal(err)
	}
	for _, item := range backup.Manifest.Files {
		digest, err := hashFile(filepath.Join(dataRoot, filepath.FromSlash(item.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if actual := hex.EncodeToString(digest); actual != item.SHA256 {
			t.Fatalf("restored hash for %s = %s, want %s", item.Path, actual, item.SHA256)
		}
	}

	restored, err := Initialize(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Storage.VerifyServerState(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServerBackupVerificationRejectsTampering(t *testing.T) {
	runtime := newTestRuntime(t)
	registerTestActor(t, runtime, "Tamper owner")
	backup, err := runtime.Storage.CreateServerBackup(context.Background(), t.TempDir(), "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(backup.Path, "beresta.db")
	file, err := os.OpenFile(databasePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("tampered"), 0); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyServerBackup(backup.Path); err == nil {
		t.Fatal("tampered server backup was accepted")
	}
}

func TestDailyServerBackupRotationKeepsExactlySevenNewestBackups(t *testing.T) {
	runtime := newTestRuntime(t)
	registerTestActor(t, runtime, "Rotation owner")
	destination := t.TempDir()
	start := time.Date(2026, 8, 1, 2, 0, 0, 0, time.UTC)
	for day := 0; day < 8; day++ {
		if _, err := runtime.Storage.CreateServerBackup(context.Background(), destination, "daily", start.AddDate(0, 0, day)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	var valid []ServerBackup
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		backup, err := VerifyServerBackup(filepath.Join(destination, entry.Name()))
		if err == nil {
			valid = append(valid, backup)
		}
	}
	if len(valid) != 7 {
		t.Fatalf("retained backups = %d, want 7", len(valid))
	}
	for _, backup := range valid {
		if !backup.Manifest.CreatedAt.After(start) {
			t.Fatalf("rotation retained an unexpected backup from %s", backup.Manifest.CreatedAt)
		}
	}
}
