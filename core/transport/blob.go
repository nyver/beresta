package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/beresta-app/beresta/core/model"
)

const BlobChunkBytes = 4 << 20

type BlobChunk struct {
	Index  int    `json:"index"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type BlobUpload struct {
	WorkspaceID       model.ID
	BlobID            []byte
	KeyID             []byte
	EncryptedManifest []byte
	TotalBytes        int64
	Chunks            []BlobChunk
	ReadChunk         func(context.Context, int) ([]byte, error)
}

type BlobDownload struct {
	WorkspaceID       model.ID
	BlobID            []byte
	KeyID             []byte
	EncryptedManifest []byte
	TotalBytes        int64
	Chunks            []BlobChunk
	WriteChunk        func(context.Context, int, []byte) error
	HasVerifiedChunk  func(context.Context, int, string) (bool, error)
}

type blobInfoJSON struct {
	WorkspaceID       string      `json:"workspace_id"`
	BlobID            string      `json:"blob_id"`
	KeyID             string      `json:"key_id"`
	EncryptedManifest []byte      `json:"encrypted_manifest"`
	TotalBytes        int64       `json:"total_bytes"`
	State             string      `json:"state"`
	Chunks            []BlobChunk `json:"chunks"`
	Uploaded          []int       `json:"uploaded"`
	ReferenceCount    int64       `json:"reference_count"`
}

// UploadBlob resumes at the first server-missing chunk and verifies every
// local encrypted chunk before sending it. Completion remains the server's
// atomic publication boundary.
func (h *HTTP) UploadBlob(ctx context.Context, upload BlobUpload) error {
	if err := validateBlobTransfer(upload.WorkspaceID, upload.BlobID, upload.KeyID, upload.TotalBytes, upload.Chunks); err != nil {
		return err
	}
	if upload.ReadChunk == nil || len(upload.EncryptedManifest) == 0 {
		return errors.New("transport: blob upload callbacks and manifest are required")
	}
	request := struct {
		WorkspaceID       string      `json:"workspace_id"`
		BlobID            string      `json:"blob_id"`
		KeyID             string      `json:"key_id"`
		EncryptedManifest []byte      `json:"encrypted_manifest"`
		TotalBytes        int64       `json:"total_bytes"`
		Chunks            []BlobChunk `json:"chunks"`
	}{upload.WorkspaceID.String(), hex.EncodeToString(upload.BlobID), hex.EncodeToString(upload.KeyID), upload.EncryptedManifest, upload.TotalBytes, upload.Chunks}
	var info blobInfoJSON
	if err := h.doJSON(ctx, http.MethodPost, "/v1/blobs/init", request, &info, true); err != nil {
		return err
	}
	uploaded := make(map[int]bool, len(info.Uploaded))
	for _, index := range info.Uploaded {
		uploaded[index] = true
	}
	for _, spec := range upload.Chunks {
		if uploaded[spec.Index] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		contents, err := upload.ReadChunk(ctx, spec.Index)
		if err != nil {
			return err
		}
		if err := verifyEncryptedChunk(contents, spec); err != nil {
			return err
		}
		path := "/v1/blobs/" + hex.EncodeToString(upload.BlobID) + "/chunks/" + strconv.Itoa(spec.Index) + "?" +
			url.Values{"workspace_id": {upload.WorkspaceID.String()}}.Encode()
		if err := h.doBytes(ctx, http.MethodPut, path, contents); err != nil {
			return err
		}
	}
	path := "/v1/blobs/" + hex.EncodeToString(upload.BlobID) + "/complete?" + url.Values{"workspace_id": {upload.WorkspaceID.String()}}.Encode()
	return h.doJSON(ctx, http.MethodPost, path, struct{}{}, &info, true)
}

// DownloadBlob resumes from locally verified chunks, verifies every received
// chunk before persisting it, and returns only after the complete declared set
// has been written. The caller publishes the attachment after this returns.
func (h *HTTP) DownloadBlob(ctx context.Context, download BlobDownload) error {
	if err := validateBlobTransfer(download.WorkspaceID, download.BlobID, download.KeyID, download.TotalBytes, download.Chunks); err != nil {
		return err
	}
	if download.WriteChunk == nil {
		return errors.New("transport: blob download writer is required")
	}
	base := "/v1/blobs/" + hex.EncodeToString(download.BlobID)
	query := "?" + url.Values{"workspace_id": {download.WorkspaceID.String()}}.Encode()
	var info blobInfoJSON
	if err := h.doJSON(ctx, http.MethodGet, base+query, nil, &info, true); err != nil {
		return err
	}
	if info.State != "complete" || info.WorkspaceID != download.WorkspaceID.String() ||
		info.BlobID != hex.EncodeToString(download.BlobID) || info.KeyID != hex.EncodeToString(download.KeyID) ||
		info.TotalBytes != download.TotalBytes || !bytes.Equal(info.EncryptedManifest, download.EncryptedManifest) ||
		!sameBlobChunks(info.Chunks, download.Chunks) {
		return errors.New("transport: remote blob manifest does not match the expected encrypted blob")
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
		contents, err := h.getBytes(ctx, base+"/chunks/"+strconv.Itoa(spec.Index)+query, BlobChunkBytes+64)
		if err != nil {
			return err
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

func (h *HTTP) doBytes(ctx context.Context, method, path string, contents []byte) error {
	for attempt := 0; attempt < 2; attempt++ {
		if err := h.ensureSession(ctx, false); err != nil {
			return err
		}
		h.mu.Lock()
		token := h.session
		h.mu.Unlock()
		request, err := http.NewRequestWithContext(ctx, method, h.resolve(path), bytes.NewReader(contents))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/octet-stream")
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := h.client.Do(request)
		if err != nil {
			return err
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			response.Body.Close()
			h.clearSession()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			defer response.Body.Close()
			return decodeHTTPError(response)
		}
		response.Body.Close()
		return nil
	}
	return ErrAuthentication
}

func (h *HTTP) getBytes(ctx context.Context, path string, max int64) ([]byte, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := h.ensureSession(ctx, false); err != nil {
			return nil, err
		}
		h.mu.Lock()
		token := h.session
		h.mu.Unlock()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.resolve(path), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := h.client.Do(request)
		if err != nil {
			return nil, err
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			response.Body.Close()
			h.clearSession()
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			defer response.Body.Close()
			return nil, decodeHTTPError(response)
		}
		contents, err := io.ReadAll(io.LimitReader(response.Body, max+1))
		response.Body.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(contents)) > max {
			return nil, errors.New("transport: blob chunk exceeds limit")
		}
		return contents, nil
	}
	return nil, ErrAuthentication
}

func validateBlobTransfer(workspaceID model.ID, blobID, keyID []byte, totalBytes int64, chunks []BlobChunk) error {
	if err := workspaceID.Validate(); err != nil || len(blobID) != sha256.Size || len(keyID) != 16 || totalBytes <= 0 || len(chunks) == 0 {
		return errors.New("transport: invalid blob transfer")
	}
	ordered := append([]BlobChunk(nil), chunks...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	var total int64
	for index, chunk := range ordered {
		if chunk.Index != index || chunk.Bytes <= 0 || chunk.Bytes > BlobChunkBytes+64 {
			return errors.New("transport: invalid blob chunk layout")
		}
		digest, err := hex.DecodeString(chunk.SHA256)
		if err != nil || len(digest) != sha256.Size || stringsLower(chunk.SHA256) != chunk.SHA256 {
			return errors.New("transport: invalid blob chunk digest")
		}
		total += chunk.Bytes
	}
	if total != totalBytes {
		return errors.New("transport: blob total does not match chunks")
	}
	return nil
}

func verifyEncryptedChunk(contents []byte, spec BlobChunk) error {
	digest := sha256.Sum256(contents)
	if int64(len(contents)) != spec.Bytes || hex.EncodeToString(digest[:]) != spec.SHA256 {
		return errors.New("transport: encrypted blob chunk verification failed")
	}
	return nil
}

func sameBlobChunks(left, right []BlobChunk) bool {
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

func stringsLower(value string) string {
	buffer := []byte(value)
	for index, b := range buffer {
		if b >= 'A' && b <= 'Z' {
			buffer[index] = b + ('a' - 'A')
		}
	}
	return string(buffer)
}
