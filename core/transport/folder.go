package transport

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/beresta-app/beresta/core/model"
	coresync "github.com/beresta-app/beresta/core/sync"
)

// Folder is the optional shared-directory synchronization transport: two or
// more devices that can both read and write the same directory (a local
// network share, or a folder kept in sync by unrelated software) exchange
// already-encrypted operations and blobs without any server. It publishes
// every segment and blob chunk by writing a temporary file, flushing it,
// and renaming it into place, so a reader never observes a partially
// written file, and a writer that crashes mid-publish leaves only a
// harmless orphaned temp file (see pruneAbandonedTemp) rather than
// corrupting anything already published.
//
// Unlike the HTTP transport, Folder has no central authority to assign a
// workspace's operation sequence: it approximates one with a small
// manifest file (workspaces/<id>/manifest.json) protected by a short,
// best-effort exclusive lock file for the moment a writer allocates the
// next sequence range and publishes the segment that claims it. This works
// correctly for directories with immediate, coherent visibility between
// writers (a local disk or LAN share, including the two-writer convergence
// tests in folder_test.go); a folder replicated by an eventually consistent
// external syncer could let two writers observe the manifest at the same
// stale state and race. Push detects that race after the fact by
// rescanning for an overlapping segment before trusting its own manifest
// update, and reports it as a transient error so the caller's normal retry
// behavior (see core/sync.Worker) resolves it on the next attempt.
type Folder struct {
	root      string
	deviceID  model.ID
	lockStale time.Duration
	tempStale time.Duration
	lockRetry time.Duration
	lockWait  time.Duration
}

// FolderConfig configures a Folder transport.
type FolderConfig struct {
	// RootDirectory is the shared directory. It is created if missing.
	RootDirectory string
	// DeviceID names this device's segments and blob chunk claims; it need
	// not (and for privacy should not) reveal which account owns it beyond
	// what synchronized ciphertext already implies.
	DeviceID model.ID
	// LockStaleAfter bounds how long a manifest lock file may exist before
	// a later writer treats it as abandoned (the writer that created it
	// crashed or lost access to the directory) and removes it. Zero
	// selects a five-second default.
	LockStaleAfter time.Duration
	// TempStaleAfter bounds how long an orphaned *.tmp-* publication file
	// may exist before pruneAbandonedTemp removes it. Zero selects a
	// one-hour default.
	TempStaleAfter time.Duration
}

var (
	ErrFolderLockTimeout = errors.New("transport: timed out acquiring the folder manifest lock")
	ErrFolderRace        = errors.New("transport: another writer published to this folder concurrently")
)

// NewFolder validates config and prepares the shared root directory. It
// performs no locking or publication.
func NewFolder(config FolderConfig) (*Folder, error) {
	if config.RootDirectory == "" || config.DeviceID.Validate() != nil {
		return nil, errors.New("transport: folder root and device ID are required")
	}
	if err := os.MkdirAll(config.RootDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("transport: create folder root: %w", err)
	}
	lockStale := config.LockStaleAfter
	if lockStale <= 0 {
		lockStale = 5 * time.Second
	}
	tempStale := config.TempStaleAfter
	if tempStale <= 0 {
		tempStale = time.Hour
	}
	return &Folder{
		root: config.RootDirectory, deviceID: config.DeviceID,
		lockStale: lockStale, tempStale: tempStale,
		lockRetry: 20 * time.Millisecond, lockWait: 3 * time.Second,
	}, nil
}

// Status reports StatusOffline only if the shared root is not currently
// reachable (for example, an unmounted network share); it never blocks on
// another writer's lock.
func (f *Folder) Status(context.Context) Status {
	if info, err := os.Stat(f.root); err != nil || !info.IsDir() {
		return StatusOffline
	}
	return StatusCurrent
}

type folderManifest struct {
	Epoch     uint32 `json:"epoch"`
	LatestSeq uint64 `json:"latest_seq"`
}

func (f *Folder) workspaceDir(workspaceID model.ID) string {
	return filepath.Join(f.root, "workspaces", hex.EncodeToString(workspaceID.Bytes()))
}

func (f *Folder) segmentsDir(workspaceID model.ID) string {
	return filepath.Join(f.workspaceDir(workspaceID), "segments")
}

func (f *Folder) manifestPath(workspaceID model.ID) string {
	return filepath.Join(f.workspaceDir(workspaceID), "manifest.json")
}

func (f *Folder) lockPath(workspaceID model.ID) string {
	return filepath.Join(f.workspaceDir(workspaceID), "manifest.lock")
}

// readManifest returns the workspace's current manifest, or a fresh
// epoch-1 manifest if none has been published yet.
func (f *Folder) readManifest(workspaceID model.ID) (folderManifest, error) {
	data, err := os.ReadFile(f.manifestPath(workspaceID))
	if errors.Is(err, os.ErrNotExist) {
		return folderManifest{Epoch: 1}, nil
	}
	if err != nil {
		return folderManifest{}, err
	}
	var manifest folderManifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Epoch == 0 {
		return folderManifest{}, fmt.Errorf("transport: malformed folder manifest: %w", err)
	}
	return manifest, nil
}

func (f *Folder) writeManifestAtomic(workspaceID model.ID, manifest folderManifest) error {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return writeFileAtomic(f.workspaceDir(workspaceID), "manifest.json", encoded)
}

// acquireManifestLock takes the workspace's short-lived exclusive lock,
// removing it first if it looks abandoned (older than f.lockStale). The
// caller must call release exactly once.
func (f *Folder) acquireManifestLock(workspaceID model.ID) (release func(), err error) {
	lockPath := f.lockPath(workspaceID)
	deadline := time.Now().Add(f.lockWait)
	for {
		handle, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			handle.Close()
			return func() { os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("transport: acquire folder manifest lock: %w", err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > f.lockStale {
			os.Remove(lockPath) // a prior writer crashed or lost access before releasing it
			continue
		}
		if time.Now().After(deadline) {
			return nil, ErrFolderLockTimeout
		}
		time.Sleep(f.lockRetry)
	}
}

// Pull returns every operation from segments published after cursor. It
// requires no lock: segments are immutable once published, so concurrent
// reading is always safe.
func (f *Folder) Pull(ctx context.Context, workspaceID model.ID, cursor coresync.Cursor, limit int) (coresync.PullPage, error) {
	if limit <= 0 {
		return coresync.PullPage{}, errors.New("transport: invalid pull limit")
	}
	segments, err := f.listSegments(workspaceID)
	if err != nil {
		return coresync.PullPage{}, err
	}
	manifest, err := f.readManifest(workspaceID)
	if err != nil {
		return coresync.PullPage{}, err
	}
	epoch := manifest.Epoch
	if cursor.LastSequence == 0 && cursor.Epoch != 0 {
		epoch = cursor.Epoch
	}

	var collected []coresync.WireOperation
	for _, seg := range segments {
		if seg.epoch != epoch || seg.end <= cursor.LastSequence {
			continue
		}
		if err := ctx.Err(); err != nil {
			return coresync.PullPage{}, err
		}
		ops, err := readSegment(filepath.Join(f.segmentsDir(workspaceID), seg.name))
		if err != nil {
			return coresync.PullPage{}, fmt.Errorf("transport: read folder segment %s: %w", seg.name, err)
		}
		for _, op := range ops {
			if op.Sequence > cursor.LastSequence {
				collected = append(collected, op)
			}
		}
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].Sequence < collected[j].Sequence })

	more := false
	if len(collected) > limit {
		collected = collected[:limit]
		more = true
	}
	newCursor := cursor
	newCursor.WorkspaceID = workspaceID
	newCursor.Epoch = epoch
	if len(collected) > 0 {
		newCursor.LastSequence = collected[len(collected)-1].Sequence
	} else if cursor.Epoch != epoch {
		newCursor.LastSequence = 0
	}
	return coresync.PullPage{Cursor: newCursor, Operations: collected, More: more}, nil
}

// Push allocates the next sequence range under the manifest lock and
// publishes it as one new immutable segment via temp-write, fsync, rename.
// If another writer's segment already claims part of the allocated range
// (see the race note on Folder), it reports ErrFolderRace instead of
// silently overwriting or duplicating sequence numbers; the caller retries.
// maxFolderPushBatch bounds one Push call defensively, matching the HTTP
// transport's per-batch cap, in case a caller other than sync.Worker (whose
// own WorkerOptions.BatchSize already bounds it) invokes Push directly.
const maxFolderPushBatch = 256

func (f *Folder) Push(ctx context.Context, workspaceID model.ID, operations []coresync.WireOperation) ([]coresync.PushResult, error) {
	if len(operations) == 0 || len(operations) > maxFolderPushBatch {
		return nil, errors.New("transport: invalid push batch")
	}
	if err := os.MkdirAll(f.segmentsDir(workspaceID), 0o700); err != nil {
		return nil, err
	}
	release, err := f.acquireManifestLock(workspaceID)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	manifest, err := f.readManifest(workspaceID)
	if err != nil {
		return nil, err
	}
	start := manifest.LatestSeq + 1
	assigned := make([]coresync.WireOperation, len(operations))
	results := make([]coresync.PushResult, len(operations))
	for i, op := range operations {
		op.WorkspaceID = workspaceID
		op.Sequence = start + uint64(i)
		assigned[i] = op
		results[i] = coresync.PushResult{OpID: op.OpID, Sequence: op.Sequence}
	}
	end := start + uint64(len(operations)) - 1

	if collides, err := f.rangeClaimed(workspaceID, start, end); err != nil {
		return nil, err
	} else if collides {
		return nil, ErrFolderRace
	}

	name := fmt.Sprintf("seg-%010d-%020d-%020d-%s.bin", manifest.Epoch, start, end, hex.EncodeToString(f.deviceID.Bytes()))
	if err := writeSegment(f.segmentsDir(workspaceID), name, assigned); err != nil {
		return nil, err
	}
	manifest.LatestSeq = end
	if err := f.writeManifestAtomic(workspaceID, manifest); err != nil {
		return nil, err
	}
	return results, nil
}

func (f *Folder) rangeClaimed(workspaceID model.ID, start, end uint64) (bool, error) {
	segments, err := f.listSegments(workspaceID)
	if err != nil {
		return false, err
	}
	for _, seg := range segments {
		if seg.start <= end && start <= seg.end {
			return true, nil
		}
	}
	return false, nil
}

type segmentRef struct {
	name       string
	epoch      uint32
	start, end uint64
}

// listSegments returns every finalized (non-temp) segment file, ignoring
// anything this device cannot parse as one of its own filenames - a
// forward-compatibility stance matching the rest of the schema's strict
// decoders, but scoped to "skip", since an unreadable stray file in a
// shared folder is not this device's authority to reject outright.
func (f *Folder) listSegments(workspaceID model.ID) ([]segmentRef, error) {
	entries, err := os.ReadDir(f.segmentsDir(workspaceID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []segmentRef
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), tempFilePrefix) {
			continue
		}
		ref, ok := parseSegmentName(entry.Name())
		if !ok {
			continue
		}
		result = append(result, ref)
	}
	return result, nil
}

func parseSegmentName(name string) (segmentRef, bool) {
	if !strings.HasSuffix(name, ".bin") || !strings.HasPrefix(name, "seg-") {
		return segmentRef{}, false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "seg-"), ".bin"), "-")
	if len(parts) != 4 {
		return segmentRef{}, false
	}
	epoch, err1 := strconv.ParseUint(parts[0], 10, 32)
	start, err2 := strconv.ParseUint(parts[1], 10, 64)
	end, err3 := strconv.ParseUint(parts[2], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || start == 0 || end < start {
		return segmentRef{}, false
	}
	if _, err := hex.DecodeString(parts[3]); err != nil {
		return segmentRef{}, false
	}
	return segmentRef{name: name, epoch: uint32(epoch), start: start, end: end}, true
}

const segmentMagic = "BSEG0001"

func writeSegment(dir, name string, operations []coresync.WireOperation) error {
	buffer := []byte(segmentMagic)
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(operations)))
	for _, op := range operations {
		encoded, err := coresync.EncodeOperation(op)
		if err != nil {
			return err
		}
		buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(encoded)))
		buffer = append(buffer, encoded...)
	}
	return writeFileAtomic(dir, name, buffer)
}

func readSegment(path string) ([]coresync.WireOperation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < len(segmentMagic)+4 || string(data[:len(segmentMagic)]) != segmentMagic {
		return nil, errors.New("transport: malformed folder segment")
	}
	data = data[len(segmentMagic):]
	count := binary.BigEndian.Uint32(data[:4])
	data = data[4:]
	result := make([]coresync.WireOperation, 0, count)
	for range count {
		if len(data) < 4 {
			return nil, errors.New("transport: truncated folder segment")
		}
		length := binary.BigEndian.Uint32(data[:4])
		data = data[4:]
		if uint64(length) > uint64(len(data)) {
			return nil, errors.New("transport: truncated folder segment entry")
		}
		op, err := coresync.DecodeOperation(data[:length], coresync.CodecLimits{})
		if err != nil {
			return nil, err
		}
		result = append(result, op)
		data = data[length:]
	}
	if len(data) != 0 {
		return nil, errors.New("transport: trailing folder segment data")
	}
	return result, nil
}

// tempFilePrefix marks a not-yet-published file. pruneAbandonedTemp only
// ever removes files with this prefix, never a finalized segment or blob.
const tempFilePrefix = ".tmp-"

// writeFileAtomic writes contents to a randomly named temp file inside dir,
// flushes it to storage, and renames it into place as name. A reader can
// never observe a partially written final file; a crash before rename
// leaves only an orphaned temp file (see pruneAbandonedTemp).
func writeFileAtomic(dir, name string, contents []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp := filepath.Join(dir, tempFilePrefix+strconv.FormatUint(rand.Uint64(), 36))
	handle, err := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := handle.Write(contents); err != nil {
		handle.Close()
		os.Remove(temp)
		return err
	}
	if err := handle.Sync(); err != nil {
		handle.Close()
		os.Remove(temp)
		return err
	}
	if err := handle.Close(); err != nil {
		os.Remove(temp)
		return err
	}
	if err := os.Rename(temp, filepath.Join(dir, name)); err != nil {
		os.Remove(temp)
		return err
	}
	return nil
}

// PruneAbandonedTemp removes every *.tmp-* publication file older than the
// configured staleness window, anywhere under the folder root. It is safe
// to call at any time, from any device sharing the folder, since a
// finalized file is never named with the temp prefix and an in-progress
// writer's temp file is always younger than the window in normal
// operation.
func (f *Folder) PruneAbandonedTemp() (int, error) {
	removed := 0
	err := filepath.WalkDir(f.root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasPrefix(entry.Name(), tempFilePrefix) {
			return nil //nolint:nilerr -- best-effort sweep; one unreadable entry must not abort the rest.
		}
		info, err := entry.Info()
		if err != nil || time.Since(info.ModTime()) <= f.tempStale {
			return nil
		}
		if os.Remove(path) == nil {
			removed++
		}
		return nil
	})
	return removed, err
}

var _ coresync.OperationTransport = (*Folder)(nil)
var _ SyncTransport = (*Folder)(nil)

// ---- Blob exchange ----

func (f *Folder) blobDir(workspaceID model.ID, blobID []byte) string {
	hexID := hex.EncodeToString(blobID)
	return filepath.Join(f.workspaceDir(workspaceID), "blobs", hexID[0:2], hexID[2:4], hexID)
}

type folderBlobManifest struct {
	KeyID             string      `json:"key_id"`
	EncryptedManifest []byte      `json:"encrypted_manifest"`
	TotalBytes        int64       `json:"total_bytes"`
	Chunks            []BlobChunk `json:"chunks"`
}

// UploadBlob publishes every chunk this device has not already published
// (checked by content hash, so two writers uploading the identical
// attachment converge without duplicated work) and finally the blob's
// manifest, each via the same temp-write/fsync/rename discipline as
// operation segments.
func (f *Folder) UploadBlob(ctx context.Context, upload BlobUpload) error {
	if err := validateBlobTransfer(upload.WorkspaceID, upload.BlobID, upload.KeyID, upload.TotalBytes, upload.Chunks); err != nil {
		return err
	}
	if upload.ReadChunk == nil || len(upload.EncryptedManifest) == 0 {
		return errors.New("transport: blob upload callbacks and manifest are required")
	}
	dir := f.blobDir(upload.WorkspaceID, upload.BlobID)
	chunkDir := filepath.Join(dir, "chunks")
	for _, spec := range upload.Chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunkPath := filepath.Join(chunkDir, strconv.Itoa(spec.Index)+".bin")
		if existing, err := os.ReadFile(chunkPath); err == nil {
			if verifyEncryptedChunk(existing, spec) == nil {
				continue
			}
		}
		contents, err := upload.ReadChunk(ctx, spec.Index)
		if err != nil {
			return err
		}
		if err := verifyEncryptedChunk(contents, spec); err != nil {
			return err
		}
		if err := writeFileAtomic(chunkDir, strconv.Itoa(spec.Index)+".bin", contents); err != nil {
			return err
		}
	}
	manifest := folderBlobManifest{
		KeyID: hex.EncodeToString(upload.KeyID), EncryptedManifest: upload.EncryptedManifest,
		TotalBytes: upload.TotalBytes, Chunks: upload.Chunks,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	return writeFileAtomic(dir, "manifest.json", encoded)
}

// DownloadBlob reads a manifest a fellow device already published and every
// chunk it has not already verified locally.
func (f *Folder) DownloadBlob(ctx context.Context, download BlobDownload) error {
	if err := validateBlobTransfer(download.WorkspaceID, download.BlobID, download.KeyID, download.TotalBytes, download.Chunks); err != nil {
		return err
	}
	if download.WriteChunk == nil {
		return errors.New("transport: blob download writer is required")
	}
	dir := f.blobDir(download.WorkspaceID, download.BlobID)
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var manifest folderBlobManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("transport: malformed folder blob manifest: %w", err)
	}
	if manifest.KeyID != hex.EncodeToString(download.KeyID) || manifest.TotalBytes != download.TotalBytes ||
		string(manifest.EncryptedManifest) != string(download.EncryptedManifest) || !sameBlobChunks(manifest.Chunks, download.Chunks) {
		return errors.New("transport: folder blob manifest does not match the expected encrypted blob")
	}
	for _, spec := range download.Chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if download.HasVerifiedChunk != nil {
			present, err := download.HasVerifiedChunk(ctx, spec.Index, spec.SHA256)
			if err != nil {
				return err
			}
			if present {
				continue
			}
		}
		contents, err := os.ReadFile(filepath.Join(dir, "chunks", strconv.Itoa(spec.Index)+".bin"))
		if err != nil {
			return fmt.Errorf("transport: read folder blob chunk: %w", err)
		}
		if err := verifyEncryptedChunk(contents, spec); err != nil {
			return err
		}
		if err := download.WriteChunk(ctx, spec.Index, contents); err != nil {
			return err
		}
	}
	return nil
}
