package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// injectingFile wraps a real *os.File so a test can fail a chosen call
// (Write or Sync) while the rest of the durable-write sequence still runs
// against the real filesystem, exercising the actual temp-write/fsync/rename
// code path rather than a fully mocked one.
type injectingFile struct {
	*os.File
	fs *injectingFS
}

func (f *injectingFile) Write(p []byte) (int, error) {
	if f.fs.failWrite {
		return 0, errors.New("injected write failure")
	}
	return f.File.Write(p)
}

func (f *injectingFile) Sync() error {
	if f.fs.failSync {
		return errors.New("injected fsync failure")
	}
	return f.File.Sync()
}

// injectingFS lets a test simulate termination at each durability boundary
// Publish sequences: mid-write, at fsync (after data is written but before
// it is durable), and at the atomic rename (after data is durable but
// before it is visible at the blob's content-addressed path).
type injectingFS struct {
	osBlobFS
	failWrite, failSync, failRename bool
	renameCalled                    bool
}

func (f *injectingFS) createTemp(dir, pattern string) (blobFile, error) {
	real, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &injectingFile{File: real, fs: f}, nil
}

func (f *injectingFS) rename(oldpath, newpath string) error {
	f.renameCalled = true
	if f.failRename {
		return errors.New("injected rename failure")
	}
	return f.osBlobFS.rename(oldpath, newpath)
}

func newTestBlobStore(t *testing.T) (*BlobStore, *injectingFS) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "blobs")
	tmp := filepath.Join(t.TempDir(), "runtime")
	fs := &injectingFS{}
	return &BlobStore{root: root, tmp: tmp, fs: fs}, fs
}

func TestBlobStorePublishAndOpenRoundTrip(t *testing.T) {
	store, _ := newTestBlobStore(t)
	id := testBlobID(t, 10)
	content := bytes.Repeat([]byte("beresta-encrypted-chunk-"), 1000)

	published, err := store.Publish(context.Background(), id, func(w io.Writer) error {
		_, err := w.Write(content)
		return err
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if !published {
		t.Fatal("Publish() published = false on first write, want true")
	}

	f, err := store.Open(id)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("Open() content does not match published content")
	}
}

func TestBlobStorePathTwoLevelFanout(t *testing.T) {
	store, _ := newTestBlobStore(t)
	id := testBlobID(t, 11)
	got := store.Path(id)
	hexID := id.hex()
	want := filepath.Join(store.root, hexID[0:2], hexID[2:4], hexID)
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestBlobStoreOpenNotFound(t *testing.T) {
	store, _ := newTestBlobStore(t)
	_, err := store.Open(testBlobID(t, 12))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open() error = %v, want ErrNotFound", err)
	}
}

func TestBlobStorePublishDedupSkipsSecondWrite(t *testing.T) {
	store, _ := newTestBlobStore(t)
	id := testBlobID(t, 13)
	ctx := context.Background()

	calls := 0
	write := func(w io.Writer) error {
		calls++
		_, err := w.Write([]byte("content"))
		return err
	}
	first, err := store.Publish(ctx, id, write)
	if err != nil || !first {
		t.Fatalf("Publish() first call = (%v, %v), want (true, nil)", first, err)
	}
	second, err := store.Publish(ctx, id, func(io.Writer) error {
		t.Fatal("Publish() invoked write on an already-published blob")
		return nil
	})
	if err != nil || second {
		t.Fatalf("Publish() second call = (%v, %v), want (false, nil)", second, err)
	}
	if calls != 1 {
		t.Fatalf("write was called %d times, want 1", calls)
	}
}

func TestBlobStorePublishRejectsCanceledContext(t *testing.T) {
	store, _ := newTestBlobStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Publish(ctx, testBlobID(t, 14), func(io.Writer) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish() error = %v, want context.Canceled", err)
	}
}

// The following three tests inject a termination at each of Publish's three
// durability boundaries (write, fsync, rename) and prove the same
// postcondition docs/architecture.md requires: a crash can never leave a
// partially written blob visible at its content-addressed path, and never
// leaks a temp file for a normal (non-crash) error return.
func TestBlobStorePublishTerminatedDuringWriteLeavesNoVisibleBlob(t *testing.T) {
	store, fs := newTestBlobStore(t)
	fs.failWrite = true
	id := testBlobID(t, 15)

	_, err := store.Publish(context.Background(), id, func(w io.Writer) error {
		_, werr := w.Write([]byte("partial"))
		return werr
	})
	if err == nil {
		t.Fatal("Publish() error = nil, want the injected write failure")
	}
	assertBlobNotPublished(t, store, id)
}

func TestBlobStorePublishTerminatedAtFsyncLeavesNoVisibleBlob(t *testing.T) {
	store, fs := newTestBlobStore(t)
	fs.failSync = true
	id := testBlobID(t, 16)

	_, err := store.Publish(context.Background(), id, func(w io.Writer) error {
		_, werr := w.Write([]byte("durable-pending"))
		return werr
	})
	if err == nil {
		t.Fatal("Publish() error = nil, want the injected fsync failure")
	}
	assertBlobNotPublished(t, store, id)
}

func TestBlobStorePublishTerminatedAtRenameLeavesNoVisibleBlob(t *testing.T) {
	store, fs := newTestBlobStore(t)
	fs.failRename = true
	id := testBlobID(t, 17)

	_, err := store.Publish(context.Background(), id, func(w io.Writer) error {
		_, werr := w.Write([]byte("fsynced-but-not-renamed"))
		return werr
	})
	if err == nil {
		t.Fatal("Publish() error = nil, want the injected rename failure")
	}
	if !fs.renameCalled {
		t.Fatal("rename was never attempted; test did not exercise the intended boundary")
	}
	assertBlobNotPublished(t, store, id)
}

// TestBlobStorePublishRetryAfterRenameRecoversWithoutDBCommit simulates the
// one crash window Publish's contract explicitly allows: the process dies
// after the atomic rename succeeds (the blob file is now durable and
// content-complete) but before the caller's database transaction that
// references it commits. A retried Publish for the same content must be a
// safe no-op (dedup, not a second write) so the caller's normal "create
// attachment" retry path — Publish, then CreateAttachment in one
// transaction — succeeds cleanly on the second attempt.
func TestBlobStorePublishRetryAfterRenameRecoversWithoutDBCommit(t *testing.T) {
	store, _ := newTestBlobStore(t)
	ctx := context.Background()
	id := testBlobID(t, 18)
	content := []byte("attachment-ciphertext")

	published, err := store.Publish(ctx, id, func(w io.Writer) error {
		_, err := w.Write(content)
		return err
	})
	if err != nil || !published {
		t.Fatalf("Publish() = (%v, %v), want (true, nil)", published, err)
	}
	// No CreateAttachment call here: this stands in for a crash before the
	// database transaction referencing id ever committed.

	retried, err := store.Publish(ctx, id, func(w io.Writer) error {
		_, err := w.Write(content)
		return err
	})
	if err != nil {
		t.Fatalf("Publish() retry error = %v", err)
	}
	if retried {
		t.Fatal("Publish() retry published = true, want false (dedup on the already-durable blob)")
	}

	f, err := store.Open(id)
	if err != nil {
		t.Fatalf("Open() after retry error = %v", err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("Open() after retry: content does not match what was published before the simulated crash")
	}
}

func assertBlobNotPublished(t *testing.T, store *BlobStore, id BlobID) {
	t.Helper()
	exists, err := store.Exists(id)
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if exists {
		t.Fatal("Exists() = true after a terminated publish, want false")
	}
	entries, err := os.ReadDir(store.tmp)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read temp directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp directory has %d leftover entries after a terminated publish, want 0", len(entries))
	}
}
