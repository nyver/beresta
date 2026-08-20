package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type BlobChunkSpec struct {
	Index  int    `json:"index"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type BlobInit struct {
	WorkspaceID       string          `json:"workspace_id"`
	BlobID            string          `json:"blob_id"`
	KeyID             string          `json:"key_id"`
	EncryptedManifest []byte          `json:"encrypted_manifest"`
	TotalBytes        int64           `json:"total_bytes"`
	Chunks            []BlobChunkSpec `json:"chunks"`
}

type BlobInfo struct {
	WorkspaceID       string          `json:"workspace_id"`
	BlobID            string          `json:"blob_id"`
	KeyID             string          `json:"key_id"`
	EncryptedManifest []byte          `json:"encrypted_manifest,omitempty"`
	TotalBytes        int64           `json:"total_bytes"`
	State             string          `json:"state"`
	Chunks            []BlobChunkSpec `json:"chunks"`
	Uploaded          []int           `json:"uploaded"`
	ReferenceCount    int64           `json:"reference_count"`
}

type BlobGCResult struct {
	WorkspaceID string `json:"workspace_id"`
	BlobID      string `json:"blob_id"`
	Bytes       int64  `json:"bytes"`
	Removed     bool   `json:"removed"`
}

func validateBlobID(value string) error {
	if len(value) != 64 || value != strings.ToLower(value) {
		return fmt.Errorf("%w: blob_id must contain 32 lowercase hexadecimal bytes", ErrInvalid)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("%w: blob_id must contain 32 lowercase hexadecimal bytes", ErrInvalid)
	}
	return nil
}

func (s *Storage) BeginBlob(ctx context.Context, userID string, request BlobInit, now time.Time) (BlobInfo, error) {
	if err := validateID(request.WorkspaceID, "workspace_id"); err != nil {
		return BlobInfo{}, err
	}
	if err := validateBlobID(request.BlobID); err != nil {
		return BlobInfo{}, err
	}
	if err := validateOpaqueID(request.KeyID, "key_id"); err != nil {
		return BlobInfo{}, err
	}
	if request.TotalBytes <= 0 || request.TotalBytes > s.config.Limits.MaxBlobBytes {
		return BlobInfo{}, fmt.Errorf("%w: blob total exceeds configured limit", ErrInvalid)
	}
	if len(request.EncryptedManifest) == 0 || int64(len(request.EncryptedManifest)) > s.config.Limits.MaxOperationBytes {
		return BlobInfo{}, fmt.Errorf("%w: encrypted blob manifest has an invalid size", ErrInvalid)
	}
	if err := s.validateChunkSpecs(request); err != nil {
		return BlobInfo{}, err
	}

	return withWriteTx(ctx, s, func(transaction *sql.Tx) (BlobInfo, error) {
		member, err := s.isActiveMember(ctx, transaction, userID, request.WorkspaceID)
		if err != nil {
			return BlobInfo{}, err
		}
		if !member {
			return BlobInfo{}, ErrForbidden
		}
		var keyExists int
		if err := transaction.QueryRowContext(ctx, `
			SELECT 1 FROM workspaces WHERE workspace_id = ? AND current_key_id = ?`,
			request.WorkspaceID, request.KeyID).Scan(&keyExists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return BlobInfo{}, fmt.Errorf("%w: blob does not use the current workspace key", ErrConflict)
			}
			return BlobInfo{}, err
		}
		var existing BlobInfo
		err = transaction.QueryRowContext(ctx, `
			SELECT key_id, encrypted_manifest, total_bytes, state, reference_count
			FROM blobs WHERE workspace_id = ? AND blob_id = ?`, request.WorkspaceID, request.BlobID,
		).Scan(&existing.KeyID, &existing.EncryptedManifest, &existing.TotalBytes, &existing.State, &existing.ReferenceCount)
		if err == nil {
			if existing.KeyID != request.KeyID || existing.TotalBytes != request.TotalBytes ||
				!equalBytes(existing.EncryptedManifest, request.EncryptedManifest) {
				return BlobInfo{}, fmt.Errorf("%w: blob identifier already has different metadata", ErrConflict)
			}
			existing.WorkspaceID = request.WorkspaceID
			existing.BlobID = request.BlobID
			existing.Chunks, existing.Uploaded, err = readBlobChunks(ctx, transaction, request.WorkspaceID, request.BlobID)
			if err == nil && !sameChunkSpecs(existing.Chunks, request.Chunks) {
				return BlobInfo{}, fmt.Errorf("%w: blob identifier already has different chunks", ErrConflict)
			}
			return existing, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return BlobInfo{}, err
		}

		result, err := transaction.ExecContext(ctx, `
			UPDATE users SET reserved_bytes = reserved_bytes + ?
			WHERE user_id = ? AND used_bytes + reserved_bytes + ? <= quota_bytes`,
			request.TotalBytes, userID, request.TotalBytes,
		)
		if err != nil {
			return BlobInfo{}, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return BlobInfo{}, err
		}
		if updated != 1 {
			return BlobInfo{}, ErrQuota
		}
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO blobs(
				workspace_id, blob_id, owner_user_id, key_id, encrypted_manifest,
				total_bytes, chunk_count, state, reserved_bytes, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'staging', ?, ?)`,
			request.WorkspaceID, request.BlobID, userID, request.KeyID, request.EncryptedManifest,
			request.TotalBytes, len(request.Chunks), request.TotalBytes, unixNow(now),
		); err != nil {
			return BlobInfo{}, err
		}
		for _, chunk := range request.Chunks {
			digest, _ := hex.DecodeString(chunk.SHA256)
			if _, err := transaction.ExecContext(ctx, `
				INSERT INTO blob_chunks(workspace_id, blob_id, chunk_index, expected_bytes, expected_hash)
				VALUES (?, ?, ?, ?, ?)`, request.WorkspaceID, request.BlobID, chunk.Index, chunk.Bytes, digest,
			); err != nil {
				return BlobInfo{}, err
			}
		}
		return BlobInfo{
			WorkspaceID: request.WorkspaceID, BlobID: request.BlobID, KeyID: request.KeyID,
			EncryptedManifest: request.EncryptedManifest, TotalBytes: request.TotalBytes,
			State: "staging", Chunks: request.Chunks,
		}, nil
	})
}

func sameChunkSpecs(left, right []BlobChunkSpec) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (s *Storage) validateChunkSpecs(request BlobInit) error {
	if len(request.Chunks) == 0 || len(request.Chunks) > int(request.TotalBytes)+1 {
		return fmt.Errorf("%w: invalid blob chunk count", ErrInvalid)
	}
	var total int64
	for index, chunk := range request.Chunks {
		if chunk.Index != index || chunk.Bytes <= 0 || chunk.Bytes > s.config.Limits.BlobChunkBytes+64 {
			return fmt.Errorf("%w: invalid chunk %d size or index", ErrInvalid, index)
		}
		digest, err := hex.DecodeString(chunk.SHA256)
		if err != nil || len(digest) != sha256.Size || chunk.SHA256 != strings.ToLower(chunk.SHA256) {
			return fmt.Errorf("%w: invalid chunk %d SHA-256", ErrInvalid, index)
		}
		total += chunk.Bytes
	}
	if total != request.TotalBytes {
		return fmt.Errorf("%w: chunk sizes do not equal total_bytes", ErrInvalid)
	}
	return nil
}

func (s *Storage) PutBlobChunk(ctx context.Context, userID, workspaceID, blobID string, index int, contents []byte, now time.Time) error {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return err
	}
	if err := validateBlobID(blobID); err != nil {
		return err
	}
	if index < 0 || int64(len(contents)) > s.config.Limits.BlobChunkBytes+64 {
		return fmt.Errorf("%w: invalid blob chunk size or index", ErrInvalid)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	member, err := s.isActiveMember(ctx, s.db, userID, workspaceID)
	if err != nil {
		return err
	}
	if !member {
		return ErrForbidden
	}
	var state string
	var expectedBytes int64
	var expectedHash []byte
	err = s.db.QueryRowContext(ctx, `
		SELECT b.state, c.expected_bytes, c.expected_hash
		FROM blobs b JOIN blob_chunks c USING(workspace_id, blob_id)
		WHERE b.workspace_id = ? AND b.blob_id = ? AND c.chunk_index = ?`, workspaceID, blobID, index,
	).Scan(&state, &expectedBytes, &expectedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state == "complete" {
		return s.verifyChunkFile(s.finalChunkPath(workspaceID, blobID, index), expectedBytes, expectedHash)
	}
	digest := sha256.Sum256(contents)
	if int64(len(contents)) != expectedBytes || !equalBytes(digest[:], expectedHash) {
		return fmt.Errorf("%w: blob chunk does not match declared size and hash", ErrInvalid)
	}
	directory := s.stagingBlobPath(workspaceID, blobID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := restrictDirectory(directory); err != nil {
		return err
	}
	target := filepath.Join(directory, chunkFilename(index))
	if _, err := os.Stat(target); err == nil {
		if err := s.verifyChunkFile(target, expectedBytes, expectedHash); err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `
			UPDATE blob_chunks SET uploaded_at = ?
			WHERE workspace_id = ? AND blob_id = ? AND chunk_index = ?`, unixNow(now), workspaceID, blobID, index,
		)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeAtomicChunk(directory, target, contents); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE blob_chunks SET uploaded_at = ?
		WHERE workspace_id = ? AND blob_id = ? AND chunk_index = ?`, unixNow(now), workspaceID, blobID, index,
	)
	return err
}

func (s *Storage) CompleteBlob(ctx context.Context, userID, workspaceID, blobID string, now time.Time) (BlobInfo, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return BlobInfo{}, err
	}
	if err := validateBlobID(blobID); err != nil {
		return BlobInfo{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	member, err := s.isActiveMember(ctx, s.db, userID, workspaceID)
	if err != nil {
		return BlobInfo{}, err
	}
	if !member {
		return BlobInfo{}, ErrForbidden
	}
	info, err := s.blobInfo(ctx, s.db, workspaceID, blobID)
	if err != nil {
		return BlobInfo{}, err
	}
	if info.State == "complete" {
		return info, nil
	}
	staging := s.stagingBlobPath(workspaceID, blobID)
	final := s.finalBlobPath(workspaceID, blobID)
	source := staging
	if _, statErr := os.Stat(final); statErr == nil {
		source = final
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return BlobInfo{}, statErr
	}
	for _, chunk := range info.Chunks {
		digest, _ := hex.DecodeString(chunk.SHA256)
		if err := s.verifyChunkFile(filepath.Join(source, chunkFilename(chunk.Index)), chunk.Bytes, digest); err != nil {
			return BlobInfo{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return BlobInfo{}, err
	}
	if _, err := os.Stat(final); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(staging, final); err != nil {
			return BlobInfo{}, fmt.Errorf("publish completed blob: %w", err)
		}
		if err := syncDirectory(filepath.Dir(final)); err != nil {
			return BlobInfo{}, fmt.Errorf("sync completed blob: %w", err)
		}
	} else if err != nil {
		return BlobInfo{}, err
	}

	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BlobInfo{}, err
	}
	defer transaction.Rollback()
	var owner string
	var reserved int64
	if err := transaction.QueryRowContext(ctx, `SELECT owner_user_id, reserved_bytes FROM blobs
		WHERE workspace_id = ? AND blob_id = ? AND state = 'staging'`, workspaceID, blobID).Scan(&owner, &reserved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.blobInfo(ctx, transaction, workspaceID, blobID)
		}
		return BlobInfo{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE blobs SET state = 'complete', reserved_bytes = 0, completed_at = ?,
			unreferenced_at = CASE WHEN reference_count = 0 THEN ? ELSE NULL END
		WHERE workspace_id = ? AND blob_id = ?`, unixNow(now), unixNow(now), workspaceID, blobID); err != nil {
		return BlobInfo{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE users SET reserved_bytes = reserved_bytes - ?, used_bytes = used_bytes + ? WHERE user_id = ?`,
		reserved, reserved, owner); err != nil {
		return BlobInfo{}, err
	}
	if err := transaction.Commit(); err != nil {
		return BlobInfo{}, err
	}
	info.State = "complete"
	info.Uploaded = make([]int, len(info.Chunks))
	for index := range info.Chunks {
		info.Uploaded[index] = index
	}
	return info, nil
}

func (s *Storage) GetBlob(ctx context.Context, userID, workspaceID, blobID string) (BlobInfo, error) {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return BlobInfo{}, err
	}
	if err := validateBlobID(blobID); err != nil {
		return BlobInfo{}, err
	}
	member, err := s.isActiveMember(ctx, s.db, userID, workspaceID)
	if err != nil {
		return BlobInfo{}, err
	}
	if !member {
		return BlobInfo{}, ErrForbidden
	}
	return s.blobInfo(ctx, s.db, workspaceID, blobID)
}

func (s *Storage) ReadBlobChunk(ctx context.Context, userID, workspaceID, blobID string, index int) ([]byte, error) {
	info, err := s.GetBlob(ctx, userID, workspaceID, blobID)
	if err != nil {
		return nil, err
	}
	if info.State != "complete" || index < 0 || index >= len(info.Chunks) {
		return nil, ErrNotFound
	}
	contents, err := os.ReadFile(s.finalChunkPath(workspaceID, blobID, index))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	digest, _ := hex.DecodeString(info.Chunks[index].SHA256)
	if err := verifyChunk(contents, info.Chunks[index].Bytes, digest); err != nil {
		return nil, err
	}
	return contents, nil
}

func (s *Storage) SetBlobReferenced(ctx context.Context, userID, workspaceID, blobID, referenceID string, referenced bool, now time.Time) error {
	if err := validateID(workspaceID, "workspace_id"); err != nil {
		return err
	}
	if err := validateBlobID(blobID); err != nil {
		return err
	}
	if err := validateID(referenceID, "reference_id"); err != nil {
		return err
	}
	_, err := withWriteTx(ctx, s, func(transaction *sql.Tx) (struct{}, error) {
		member, err := s.isActiveMember(ctx, transaction, userID, workspaceID)
		if err != nil || !member {
			if err == nil {
				err = ErrForbidden
			}
			return struct{}{}, err
		}
		var state string
		if err := transaction.QueryRowContext(ctx, `SELECT state FROM blobs WHERE workspace_id = ? AND blob_id = ?`, workspaceID, blobID).
			Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return struct{}{}, ErrNotFound
			}
			return struct{}{}, err
		}
		if state != "complete" {
			return struct{}{}, ErrConflict
		}
		if referenced {
			result, err := transaction.ExecContext(ctx, `
				INSERT OR IGNORE INTO blob_references(workspace_id, blob_id, reference_id, created_at)
				VALUES (?, ?, ?, ?)`, workspaceID, blobID, referenceID, unixNow(now))
			if err != nil {
				return struct{}{}, classifyConstraint(err)
			}
			inserted, _ := result.RowsAffected()
			if inserted == 0 {
				var existingBlobID string
				if err := transaction.QueryRowContext(ctx, `
					SELECT blob_id FROM blob_references WHERE workspace_id = ? AND reference_id = ?`, workspaceID, referenceID).
					Scan(&existingBlobID); err != nil {
					return struct{}{}, err
				}
				if existingBlobID != blobID {
					return struct{}{}, fmt.Errorf("%w: blob reference identifier is already bound", ErrConflict)
				}
				return struct{}{}, nil
			}
			_, err = transaction.ExecContext(ctx, `
				UPDATE blobs SET reference_count = reference_count + 1, unreferenced_at = NULL
				WHERE workspace_id = ? AND blob_id = ?`, workspaceID, blobID)
			return struct{}{}, err
		}
		result, err := transaction.ExecContext(ctx, `
			DELETE FROM blob_references WHERE workspace_id = ? AND blob_id = ? AND reference_id = ?`,
			workspaceID, blobID, referenceID)
		if err != nil {
			return struct{}{}, err
		}
		removed, _ := result.RowsAffected()
		if removed == 0 {
			return struct{}{}, nil
		}
		_, err = transaction.ExecContext(ctx, `
			UPDATE blobs SET reference_count = reference_count - 1,
				unreferenced_at = CASE WHEN reference_count = 1 THEN ? ELSE NULL END
			WHERE workspace_id = ? AND blob_id = ? AND reference_count > 0`, unixNow(now), workspaceID, blobID)
		return struct{}{}, err
	})
	return err
}

func (s *Storage) GarbageCollectBlobs(ctx context.Context, before time.Time, dryRun bool) ([]BlobGCResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT workspace_id, blob_id, total_bytes FROM blobs
		WHERE state = 'complete' AND reference_count = 0 AND unreferenced_at IS NOT NULL AND unreferenced_at <= ?
		ORDER BY workspace_id, blob_id`, unixNow(before))
	if err != nil {
		return nil, err
	}
	var results []BlobGCResult
	for rows.Next() {
		var item BlobGCResult
		if err := rows.Scan(&item.WorkspaceID, &item.BlobID, &item.Bytes); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	iterationErr := rows.Err()
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if iterationErr != nil {
		return nil, iterationErr
	}
	if dryRun || len(results) == 0 {
		return results, nil
	}
	for index := range results {
		if err := s.collectBlob(ctx, results[index]); err != nil {
			return results, err
		}
		results[index].Removed = true
	}
	return results, nil
}

func (s *Storage) collectBlob(ctx context.Context, item BlobGCResult) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	final := s.finalBlobPath(item.WorkspaceID, item.BlobID)
	trashRoot := filepath.Join(s.dataRoot, "blobs", ".trash")
	if err := os.MkdirAll(trashRoot, 0o700); err != nil {
		return err
	}
	trash, err := os.MkdirTemp(trashRoot, "blob-")
	if err != nil {
		return err
	}
	if err := os.Remove(trash); err != nil {
		return err
	}
	if err := os.Rename(final, trash); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(final)); err != nil {
		os.Rename(trash, final)
		return err
	}
	if err := syncDirectory(trashRoot); err != nil {
		os.Rename(trash, final)
		return err
	}
	restore := true
	defer func() {
		if restore {
			os.Rename(trash, final)
		}
	}()
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var owner string
	var size int64
	if err := transaction.QueryRowContext(ctx, `
		SELECT owner_user_id, total_bytes FROM blobs
		WHERE workspace_id = ? AND blob_id = ? AND state = 'complete' AND reference_count = 0`,
		item.WorkspaceID, item.BlobID).Scan(&owner, &size); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM blobs WHERE workspace_id = ? AND blob_id = ?`, item.WorkspaceID, item.BlobID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE users SET used_bytes = used_bytes - ? WHERE user_id = ?`, size, owner); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return err
	}
	restore = false
	return os.RemoveAll(trash)
}

func (s *Storage) blobInfo(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, workspaceID, blobID string) (BlobInfo, error) {
	var info BlobInfo
	info.WorkspaceID, info.BlobID = workspaceID, blobID
	err := query.QueryRowContext(ctx, `
		SELECT key_id, encrypted_manifest, total_bytes, state, reference_count
		FROM blobs WHERE workspace_id = ? AND blob_id = ?`, workspaceID, blobID,
	).Scan(&info.KeyID, &info.EncryptedManifest, &info.TotalBytes, &info.State, &info.ReferenceCount)
	if errors.Is(err, sql.ErrNoRows) {
		return BlobInfo{}, ErrNotFound
	}
	if err != nil {
		return BlobInfo{}, err
	}
	info.Chunks, info.Uploaded, err = readBlobChunks(ctx, query, workspaceID, blobID)
	return info, err
}

func readBlobChunks(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, workspaceID, blobID string) ([]BlobChunkSpec, []int, error) {
	rows, err := query.QueryContext(ctx, `
		SELECT chunk_index, expected_bytes, expected_hash, uploaded_at
		FROM blob_chunks WHERE workspace_id = ? AND blob_id = ? ORDER BY chunk_index`, workspaceID, blobID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var specs []BlobChunkSpec
	var uploaded []int
	for rows.Next() {
		var spec BlobChunkSpec
		var digest []byte
		var uploadedAt sql.NullInt64
		if err := rows.Scan(&spec.Index, &spec.Bytes, &digest, &uploadedAt); err != nil {
			return nil, nil, err
		}
		spec.SHA256 = hex.EncodeToString(digest)
		specs = append(specs, spec)
		if uploadedAt.Valid {
			uploaded = append(uploaded, spec.Index)
		}
	}
	return specs, uploaded, rows.Err()
}

func (s *Storage) stagingBlobPath(workspaceID, blobID string) string {
	return filepath.Join(s.dataRoot, "blobs", ".staging", workspaceID, blobID)
}

func (s *Storage) finalBlobPath(workspaceID, blobID string) string {
	return filepath.Join(s.dataRoot, "blobs", blobID[:2], blobID[2:4], blobID, workspaceID)
}

func (s *Storage) finalChunkPath(workspaceID, blobID string, index int) string {
	return filepath.Join(s.finalBlobPath(workspaceID, blobID), chunkFilename(index))
}

func chunkFilename(index int) string { return strconv.Itoa(index) + ".chunk" }

func writeAtomicChunk(directory, target string, contents []byte) error {
	file, err := os.CreateTemp(directory, ".chunk-")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := restrictFile(temporary); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func (s *Storage) verifyChunkFile(path string, expectedBytes int64, expectedHash []byte) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, expectedBytes+1))
	if err != nil {
		return err
	}
	if written != expectedBytes || !equalBytes(hash.Sum(nil), expectedHash) {
		return fmt.Errorf("%w: stored blob chunk failed verification", ErrConflict)
	}
	return nil
}

func verifyChunk(contents []byte, expectedBytes int64, expectedHash []byte) error {
	digest := sha256.Sum256(contents)
	if int64(len(contents)) != expectedBytes || !equalBytes(digest[:], expectedHash) {
		return fmt.Errorf("%w: stored blob chunk failed verification", ErrConflict)
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func sortedInts(values []int) []int {
	result := append([]int(nil), values...)
	sort.Ints(result)
	return result
}
