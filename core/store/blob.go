package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// BlobIDBytes is the length of a content-addressed blob identifier: the
// 32-byte HMAC-SHA-256 private attachment identity computed by
// core/crypto.ComputeBlobID over an attachment's plaintext under a
// per-workspace key.
const BlobIDBytes = 32

// BlobID is a content-addressed, per-workspace-private attachment
// identifier. It never varies with device or replica; two devices that
// attach the same plaintext to the same workspace compute the same BlobID
// and therefore publish the same on-disk blob at most once.
type BlobID [BlobIDBytes]byte

// ParseBlobID validates raw bytes as a well-formed BlobID.
func ParseBlobID(value []byte) (BlobID, error) {
	if len(value) != BlobIDBytes {
		return BlobID{}, fmt.Errorf("store: invalid blob ID length %d, want %d", len(value), BlobIDBytes)
	}
	var id BlobID
	copy(id[:], value)
	return id, nil
}

// Bytes returns an independent copy of the identifier's bytes.
func (id BlobID) Bytes() []byte {
	return append([]byte(nil), id[:]...)
}

func (id BlobID) hex() string {
	return hex.EncodeToString(id[:])
}

// blobFile is the subset of *os.File that BlobStore.Publish uses. Tests
// inject fakes implementing it to fail at a chosen durability boundary.
type blobFile interface {
	io.Writer
	Name() string
	Sync() error
	Close() error
}

// blobFS abstracts the filesystem calls BlobStore.Publish sequences, so
// tests can inject a failure at each step (temp write, fsync, rename) to
// prove the publication protocol never leaves a partially written blob
// visible at its content-addressed path — the crash-safety contract
// documented in docs/architecture.md and docs/threat-model.md ("Injected
// termination at every durability boundary").
type blobFS interface {
	createTemp(dir, pattern string) (blobFile, error)
	rename(oldpath, newpath string) error
	mkdirAll(path string) error
	stat(path string) (fs.FileInfo, error)
	remove(path string) error
}

type osBlobFS struct{}

func (osBlobFS) createTemp(dir, pattern string) (blobFile, error) { return os.CreateTemp(dir, pattern) }
func (osBlobFS) rename(oldpath, newpath string) error             { return os.Rename(oldpath, newpath) }
func (osBlobFS) mkdirAll(path string) error                       { return os.MkdirAll(path, 0o700) }
func (osBlobFS) stat(path string) (fs.FileInfo, error)            { return os.Stat(path) }
func (osBlobFS) remove(path string) error                         { return os.Remove(path) }

// BlobStore durably publishes and reads back opaque encrypted attachment
// content under a two-level content-addressed path
// (<root>/<aa>/<bb>/<blob-id-hex>, see docs/architecture.md), separate from
// the SQLCipher database that tracks each blob's metadata and note
// references. BlobStore knows nothing about attachment chunk framing or
// manifests; it stores whatever bytes the caller writes as one immutable
// file per BlobID.
type BlobStore struct {
	root string
	tmp  string
	fs   blobFS
}

// NewBlobStore returns a BlobStore rooted at root, using tmpDir for staging
// files before they are published. tmpDir should be a directory on the same
// filesystem/volume as root so the final publish step is a same-volume
// rename (atomic), not a copy.
func NewBlobStore(root, tmpDir string) *BlobStore {
	return &BlobStore{root: root, tmp: tmpDir, fs: osBlobFS{}}
}

// Path returns the content-addressed file path a blob is published to or
// read from. It performs no filesystem access.
func (s *BlobStore) Path(id BlobID) string {
	hexID := id.hex()
	return filepath.Join(s.root, hexID[0:2], hexID[2:4], hexID)
}

// Exists reports whether id has already been durably published.
func (s *BlobStore) Exists(id BlobID) (bool, error) {
	_, err := s.fs.stat(s.Path(id))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("store: stat blob: %w", err)
}

// Publish durably writes one blob's content: write, is called once with a
// temporary file to stream the ciphertext into, and Publish then fsyncs and
// atomically renames the temp file into id's content-addressed path. Only
// after Publish returns success is the blob visible to Open or Exists —
// docs/architecture.md's crash-safety contract requires the caller to
// commit any database reference to id only after Publish succeeds, never
// before, so a crash can strand an unreferenced blob (later garbage
// collected) but never a committed reference to a partial one.
//
// Publish is dedup-safe: because id is a content address, a blob already
// published under it is byte-identical to what write would produce, so
// Publish returns (false, nil) without invoking write again. This makes
// re-attaching identical content, or retrying a publish that crashed after
// its rename but before the caller's database commit, both safe no-ops.
func (s *BlobStore) Publish(ctx context.Context, id BlobID, write func(io.Writer) error) (published bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	finalPath := s.Path(id)
	exists, err := s.Exists(id)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	if err := s.fs.mkdirAll(s.tmp); err != nil {
		return false, fmt.Errorf("store: create blob temp directory: %w", err)
	}
	tmpFile, err := s.fs.createTemp(s.tmp, "blob-*.tmp")
	if err != nil {
		return false, fmt.Errorf("store: create blob temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		// A crash between here and the rename below leaves tmpPath behind;
		// that is expected and harmless (docs/architecture.md documents
		// runtime/temp files as replaceable), never a partially visible
		// published blob, because Open/Exists only ever look at finalPath.
		if cleanup {
			_ = s.fs.remove(tmpPath)
		}
	}()

	if err := write(tmpFile); err != nil {
		_ = tmpFile.Close()
		return false, fmt.Errorf("store: write blob content: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = tmpFile.Close()
		return false, err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return false, fmt.Errorf("store: fsync blob temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return false, fmt.Errorf("store: close blob temp file: %w", err)
	}

	if err := s.fs.mkdirAll(filepath.Dir(finalPath)); err != nil {
		return false, fmt.Errorf("store: create blob directory: %w", err)
	}
	if err := s.fs.rename(tmpPath, finalPath); err != nil {
		return false, fmt.Errorf("store: publish blob: %w", err)
	}
	cleanup = false
	return true, nil
}

// Open returns a read handle to a previously published blob. It reports
// ErrNotFound if id has never been published (or was published to a
// different BlobStore root).
//
// Open does not defend against a symlink placed at the content-addressed
// path (os.Open follows symlinks, so the regular-file check below applies
// to the link's target, not the directory entry itself). This is an
// accepted gap for locally attacker-controlled storage: substituted content
// is still AEAD-sealed ciphertext under a key derived from id, so a caller
// decrypting what Open returns detects the substitution during
// authentication rather than at this layer.
func (s *BlobStore) Open(id BlobID) (*os.File, error) {
	f, err := os.Open(s.Path(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: open blob: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("store: stat open blob: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("store: blob path is not a regular file")
	}
	return f, nil
}
