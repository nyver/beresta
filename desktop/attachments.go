package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/beresta-app/beresta/core/account"
)

// AttachmentDTO is the JS-facing shape of one attachment's display
// metadata. BlobID is hex-encoded (see blobIDString); the manifest and
// encryption key ID are internal and never leave the core layer.
type AttachmentDTO struct {
	BlobID      string `json:"blob_id"`
	WorkspaceID string `json:"workspace_id"`
	DisplayName string `json:"display_name"`
	MediaType   string `json:"media_type"`
	SizeBytes   uint64 `json:"size_bytes"`
}

func attachmentInfoDTO(workspaceID string, info account.AttachmentInfo) AttachmentDTO {
	return AttachmentDTO{
		BlobID:      blobIDString(info.BlobID),
		WorkspaceID: workspaceID,
		DisplayName: info.DisplayName,
		MediaType:   info.MediaType,
		SizeBytes:   info.SizeBytes,
	}
}

// runtimeContext returns the live Wails runtime context required by
// dialog/window calls, or an error before startup has wired one (for
// example, in a unit test that constructs an App directly).
func (a *App) runtimeContext() (context.Context, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ready {
		return nil, fmt.Errorf("app: the desktop window is not ready yet")
	}
	return a.ctx, nil
}

// PickAttachmentFile opens the native file picker and returns the chosen
// path, or "" if the user canceled.
func (a *App) PickAttachmentFile() (string, error) {
	ctx, err := a.runtimeContext()
	if err != nil {
		return "", mapError(err)
	}
	path, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{Title: "Choose a file to attach"})
	if err != nil {
		return "", mapError(err)
	}
	return path, nil
}

// PickSaveDestination opens the native save-file picker, pre-filled with
// defaultFileName, and returns the chosen path, or "" if the user
// canceled.
func (a *App) PickSaveDestination(defaultFileName string) (string, error) {
	ctx, err := a.runtimeContext()
	if err != nil {
		return "", mapError(err)
	}
	path, err := runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{Title: "Save attachment as", DefaultFilename: defaultFileName})
	if err != nil {
		return "", mapError(err)
	}
	return path, nil
}

// AddAttachmentFromFile encrypts and attaches the file at sourcePath to
// noteID, using its base name as the display name and a media type
// guessed from its extension.
func (a *App) AddAttachmentFromFile(noteID, sourcePath string) (AttachmentDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return AttachmentDTO{}, mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return AttachmentDTO{}, mapError(err)
	}
	f, err := os.Open(sourcePath)
	if err != nil {
		return AttachmentDTO{}, mapError(fmt.Errorf("open attachment source: %w", err))
	}
	defer f.Close()

	displayName := filepath.Base(sourcePath)
	mediaType := mime.TypeByExtension(filepath.Ext(sourcePath))
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}

	attachment, err := acc.AddAttachment(a.requestContext(), workspaceID, note, displayName, mediaType, f)
	if err != nil {
		return AttachmentDTO{}, mapError(err)
	}
	return AttachmentDTO{
		BlobID:      blobIDString(attachment.BlobID),
		WorkspaceID: idString(attachment.WorkspaceID),
		DisplayName: displayName,
		MediaType:   mediaType,
		SizeBytes:   attachment.SizeBytes,
	}, nil
}

// AddAttachmentFromBytes encrypts and attaches raw plaintext (base64-encoded
// for the JS bridge, see decodeBase64) to noteID under the given display
// name and media type. It exists alongside AddAttachmentFromFile for
// content that never has an on-disk source path - clipboard image paste in
// particular, whose data only ever exists as an in-memory blob in the
// frontend.
func (a *App) AddAttachmentFromBytes(noteID, displayName, mediaType, dataBase64 string) (AttachmentDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return AttachmentDTO{}, mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return AttachmentDTO{}, mapError(err)
	}
	data, err := decodeBase64(dataBase64)
	if err != nil {
		return AttachmentDTO{}, mapError(err)
	}

	attachment, err := acc.AddAttachment(a.requestContext(), workspaceID, note, displayName, mediaType, bytes.NewReader(data))
	if err != nil {
		return AttachmentDTO{}, mapError(err)
	}
	return AttachmentDTO{
		BlobID:      blobIDString(attachment.BlobID),
		WorkspaceID: idString(attachment.WorkspaceID),
		DisplayName: displayName,
		MediaType:   mediaType,
		SizeBytes:   attachment.SizeBytes,
	}, nil
}

// ListNoteAttachments returns the display metadata for every attachment
// currently present on noteID.
func (a *App) ListNoteAttachments(noteID string) ([]AttachmentDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return nil, mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return nil, mapError(err)
	}
	infos, err := acc.ListNoteAttachments(a.requestContext(), workspaceID, note)
	if err != nil {
		return nil, mapError(err)
	}
	workspaceIDStr := idString(workspaceID)
	out := make([]AttachmentDTO, len(infos))
	for i, info := range infos {
		out[i] = attachmentInfoDTO(workspaceIDStr, info)
	}
	return out, nil
}

// RemoveAttachment removes a note's reference to one attachment; the
// published content itself is reclaimed later by garbage collection.
func (a *App) RemoveAttachment(noteID, blobIDHex string) error {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return mapError(err)
	}
	note, err := parseID(noteID)
	if err != nil {
		return mapError(err)
	}
	blobID, err := parseBlobID(blobIDHex)
	if err != nil {
		return mapError(err)
	}
	return mapError(acc.RemoveAttachment(a.requestContext(), workspaceID, note, blobID))
}

// AttachmentSaveResult reports what SaveAttachmentToFile wrote.
type AttachmentSaveResult struct {
	DisplayName string `json:"display_name"`
	MediaType   string `json:"media_type"`
}

// SaveAttachmentToFile authenticates and decrypts one attachment's
// plaintext to destPath. It stages the write to a temporary file beside
// destPath and renames it into place only once the read has fully
// authenticated, so a decryption failure, a canceled context, or a write
// error never truncates or deletes a pre-existing file the user chose to
// overwrite.
func (a *App) SaveAttachmentToFile(blobIDHex, destPath string) (AttachmentSaveResult, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return AttachmentSaveResult{}, mapError(err)
	}
	blobID, err := parseBlobID(blobIDHex)
	if err != nil {
		return AttachmentSaveResult{}, mapError(err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".attachment-*.tmp")
	if err != nil {
		return AttachmentSaveResult{}, mapError(fmt.Errorf("create attachment destination: %w", err))
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpPath)
		}
	}()

	displayName, mediaType, err := acc.ReadAttachment(a.requestContext(), workspaceID, blobID, tmp)
	if err != nil {
		tmp.Close()
		return AttachmentSaveResult{}, mapError(err)
	}
	if err := tmp.Close(); err != nil {
		return AttachmentSaveResult{}, mapError(fmt.Errorf("close attachment destination: %w", err))
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return AttachmentSaveResult{}, mapError(fmt.Errorf("publish attachment destination: %w", err))
	}
	cleanup = false
	return AttachmentSaveResult{DisplayName: displayName, MediaType: mediaType}, nil
}

// maxAttachmentPreviewBytes bounds ReadAttachmentPreview so an inline
// in-app preview never buffers an unbounded amount of decrypted plaintext
// in memory or across the JS bridge. The frontend already gates preview
// calls on AttachmentDTO.SizeBytes; this is defense in depth, not the
// primary control.
const maxAttachmentPreviewBytes = 8 * 1024 * 1024

// errAttachmentPreviewTooLarge reports that an attachment exceeds
// maxAttachmentPreviewBytes and cannot be inline-previewed; the caller must
// use SaveAttachmentToFile instead.
var errAttachmentPreviewTooLarge = fmt.Errorf("app: attachment exceeds the %d-byte inline preview limit", maxAttachmentPreviewBytes)

// boundedBuffer is a bytes.Buffer that refuses writes once it has
// accumulated more than limit bytes, so a decrypt-to-memory call fails
// closed instead of buffering an oversized attachment in full before the
// caller can react. docs/threat-model.md prohibits plaintext attachment
// caches on disk, so previews stay entirely in memory and are bounded here
// rather than by relying on the OS to reclaim a temp file later.
type boundedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errAttachmentPreviewTooLarge
	}
	return w.buf.Write(p)
}

// AttachmentPreviewDTO is one attachment's plaintext, base64-encoded for
// inline rendering (an <img> data URL for an image, for example) without
// ever writing decrypted content to disk.
type AttachmentPreviewDTO struct {
	DisplayName string `json:"display_name"`
	MediaType   string `json:"media_type"`
	DataBase64  string `json:"data_base64"`
}

// ReadAttachmentPreview authenticates and decrypts one attachment entirely
// into memory for inline preview. Callers should only invoke this for
// attachments already known (from ListNoteAttachments' SizeBytes) to be
// reasonably small; it still fails closed at maxAttachmentPreviewBytes
// either way.
func (a *App) ReadAttachmentPreview(blobIDHex string) (AttachmentPreviewDTO, error) {
	acc, workspaceID, err := a.primaryWorkspace()
	if err != nil {
		return AttachmentPreviewDTO{}, mapError(err)
	}
	blobID, err := parseBlobID(blobIDHex)
	if err != nil {
		return AttachmentPreviewDTO{}, mapError(err)
	}

	dest := &boundedBuffer{limit: maxAttachmentPreviewBytes}
	displayName, mediaType, err := acc.ReadAttachment(a.requestContext(), workspaceID, blobID, dest)
	if err != nil {
		return AttachmentPreviewDTO{}, mapError(err)
	}
	return AttachmentPreviewDTO{
		DisplayName: displayName,
		MediaType:   mediaType,
		DataBase64:  base64.StdEncoding.EncodeToString(dest.buf.Bytes()),
	}, nil
}
