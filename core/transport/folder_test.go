package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func sortChunksByIndex(chunks []BlobChunk) {
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].Index < chunks[j].Index })
}

func folderTestID(salt byte) model.ID {
	raw := bytes.Repeat([]byte{salt}, 16)
	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80
	id, err := model.ParseID(raw)
	if err != nil {
		panic(err)
	}
	return id
}

func folderTestOperation(workspaceID, deviceID model.ID, opSalt byte) coresync.WireOperation {
	return coresync.WireOperation{
		OpID: folderTestID(opSalt), WorkspaceID: workspaceID, DeviceID: deviceID,
		Clock:      model.HLC{PhysicalMS: 1000 + uint64(opSalt), Logical: 0, DeviceID: deviceID},
		KeyID:      bytes.Repeat([]byte{9}, 16),
		Nonce:      bytes.Repeat([]byte{opSalt}, 24),
		Ciphertext: bytes.Repeat([]byte{opSalt, 1, 2, 3}, 8),
		Signature:  bytes.Repeat([]byte{opSalt}, 64),
	}
}

func TestFolderTransportPushPullRoundTrip(t *testing.T) {
	root := t.TempDir()
	deviceID := folderTestID(1)
	workspaceID := folderTestID(2)
	folder, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: deviceID})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	op1 := folderTestOperation(workspaceID, deviceID, 10)
	op2 := folderTestOperation(workspaceID, deviceID, 11)
	results, err := folder.Push(ctx, workspaceID, []coresync.WireOperation{op1, op2})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(results) != 2 || results[0].Sequence != 1 || results[1].Sequence != 2 {
		t.Fatalf("unexpected push results: %+v", results)
	}

	page, err := folder.Pull(ctx, workspaceID, coresync.Cursor{WorkspaceID: workspaceID}, 10)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if len(page.Operations) != 2 || page.More {
		t.Fatalf("unexpected pull page: %+v", page)
	}
	if page.Operations[0].OpID != op1.OpID || page.Operations[1].OpID != op2.OpID {
		t.Fatal("pulled operations out of order")
	}
	if page.Cursor.LastSequence != 2 {
		t.Fatalf("unexpected cursor: %+v", page.Cursor)
	}

	// A second pull from the returned cursor sees nothing new.
	empty, err := folder.Pull(ctx, workspaceID, page.Cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Operations) != 0 {
		t.Fatalf("expected no new operations, got %d", len(empty.Operations))
	}
}

func TestFolderTransportPullRespectsLimitAndReportsMore(t *testing.T) {
	root := t.TempDir()
	deviceID := folderTestID(1)
	workspaceID := folderTestID(2)
	folder, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: deviceID})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ops := make([]coresync.WireOperation, 5)
	for i := range ops {
		ops[i] = folderTestOperation(workspaceID, deviceID, byte(20+i))
	}
	if _, err := folder.Push(ctx, workspaceID, ops); err != nil {
		t.Fatal(err)
	}
	page, err := folder.Pull(ctx, workspaceID, coresync.Cursor{WorkspaceID: workspaceID}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Operations) != 3 || !page.More {
		t.Fatalf("expected a bounded page with More=true, got %+v", page)
	}
	rest, err := folder.Pull(ctx, workspaceID, page.Cursor, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.Operations) != 2 || rest.More {
		t.Fatalf("expected the remaining 2 operations, got %+v", rest)
	}
}

func TestFolderTransportTwoWriterConvergence(t *testing.T) {
	root := t.TempDir()
	workspaceID := folderTestID(2)
	deviceA := folderTestID(1)
	deviceB := folderTestID(3)

	folderA, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: deviceA})
	if err != nil {
		t.Fatal(err)
	}
	folderB, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: deviceB})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Two independent "devices" push interleaved, disjoint batches to the
	// same shared directory without coordinating with each other beyond the
	// directory itself.
	if _, err := folderA.Push(ctx, workspaceID, []coresync.WireOperation{folderTestOperation(workspaceID, deviceA, 30)}); err != nil {
		t.Fatalf("device A push 1: %v", err)
	}
	if _, err := folderB.Push(ctx, workspaceID, []coresync.WireOperation{folderTestOperation(workspaceID, deviceB, 31)}); err != nil {
		t.Fatalf("device B push 1: %v", err)
	}
	if _, err := folderA.Push(ctx, workspaceID, []coresync.WireOperation{folderTestOperation(workspaceID, deviceA, 32)}); err != nil {
		t.Fatalf("device A push 2: %v", err)
	}

	pageFromA, err := folderA.Pull(ctx, workspaceID, coresync.Cursor{WorkspaceID: workspaceID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	pageFromB, err := folderB.Pull(ctx, workspaceID, coresync.Cursor{WorkspaceID: workspaceID}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pageFromA.Operations) != 3 || len(pageFromB.Operations) != 3 {
		t.Fatalf("expected both readers to see all 3 operations: A=%d B=%d", len(pageFromA.Operations), len(pageFromB.Operations))
	}
	// Both readers must derive the identical total order and sequence
	// assignment from the same set of published segments - that is the
	// convergence property a shared, sequence-allocating manifest exists to
	// guarantee.
	seen := make(map[uint64]model.ID)
	for i := range pageFromA.Operations {
		if pageFromA.Operations[i].Sequence != pageFromB.Operations[i].Sequence || pageFromA.Operations[i].OpID != pageFromB.Operations[i].OpID {
			t.Fatalf("readers diverged at index %d: A=%+v B=%+v", i, pageFromA.Operations[i], pageFromB.Operations[i])
		}
		if existing, dup := seen[pageFromA.Operations[i].Sequence]; dup {
			t.Fatalf("sequence %d claimed by both %s and %s", pageFromA.Operations[i].Sequence, existing, pageFromA.Operations[i].OpID)
		}
		seen[pageFromA.Operations[i].Sequence] = pageFromA.Operations[i].OpID
	}
}

func TestFolderTransportAbandonedLockIsRecovered(t *testing.T) {
	root := t.TempDir()
	deviceID := folderTestID(1)
	workspaceID := folderTestID(2)
	folder, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: deviceID, LockStaleAfter: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(folder.workspaceDir(workspaceID), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := folder.lockPath(workspaceID)
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	if _, err := folder.Push(context.Background(), workspaceID, []coresync.WireOperation{folderTestOperation(workspaceID, deviceID, 40)}); err != nil {
		t.Fatalf("Push should recover from an abandoned lock: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Push took too long recovering an abandoned lock: %v", elapsed)
	}
}

func TestFolderTransportPruneAbandonedTemp(t *testing.T) {
	root := t.TempDir()
	deviceID := folderTestID(1)
	folder, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: deviceID, TempStaleAfter: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "workspaces", "abc", "segments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(dir, tempFilePrefix+"orphaned")
	if err := os.WriteFile(stalePath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stalePath, old, old); err != nil {
		t.Fatal(err)
	}
	freshPath := filepath.Join(dir, tempFilePrefix+"in-progress")
	if err := os.WriteFile(freshPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := folder.PruneAbandonedTemp()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected exactly 1 removal, got %d", removed)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatal("the stale temp file should have been removed")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatal("an in-progress temp file must not be removed")
	}
}

func TestFolderTransportBlobUploadDownloadRoundTrip(t *testing.T) {
	root := t.TempDir()
	workspaceID := folderTestID(2)
	uploader, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: folderTestID(1)})
	if err != nil {
		t.Fatal(err)
	}
	downloader, err := NewFolder(FolderConfig{RootDirectory: root, DeviceID: folderTestID(3)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	blobID := bytes.Repeat([]byte{7}, 32)
	keyID := bytes.Repeat([]byte{8}, 16)
	chunkContents := map[int][]byte{0: bytes.Repeat([]byte{1}, 100), 1: bytes.Repeat([]byte{2}, 50)}
	chunks := make([]BlobChunk, 0, len(chunkContents))
	for index, contents := range chunkContents {
		chunks = append(chunks, BlobChunk{Index: index, Bytes: int64(len(contents)), SHA256: sha256Hex(contents)})
	}
	sortChunksByIndex(chunks)
	var total int64
	for _, c := range chunks {
		total += c.Bytes
	}

	upload := BlobUpload{
		WorkspaceID: workspaceID, BlobID: blobID, KeyID: keyID, EncryptedManifest: []byte("opaque-manifest"),
		TotalBytes: total, Chunks: chunks,
		ReadChunk: func(_ context.Context, index int) ([]byte, error) { return chunkContents[index], nil },
	}
	if err := uploader.UploadBlob(ctx, upload); err != nil {
		t.Fatalf("UploadBlob: %v", err)
	}

	received := map[int][]byte{}
	download := BlobDownload{
		WorkspaceID: workspaceID, BlobID: blobID, KeyID: keyID, EncryptedManifest: []byte("opaque-manifest"),
		TotalBytes: total, Chunks: chunks,
		WriteChunk: func(_ context.Context, index int, contents []byte) error {
			received[index] = append([]byte(nil), contents...)
			return nil
		},
	}
	if err := downloader.DownloadBlob(ctx, download); err != nil {
		t.Fatalf("DownloadBlob: %v", err)
	}
	if len(received) != len(chunkContents) {
		t.Fatalf("expected %d chunks, got %d", len(chunkContents), len(received))
	}
	for index, want := range chunkContents {
		if !bytes.Equal(received[index], want) {
			t.Fatalf("chunk %d mismatch", index)
		}
	}
}
