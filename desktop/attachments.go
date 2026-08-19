package main

import (
	"context"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/beresta-app/beresta/core/store"
)

// AttachmentDTO is the JS-facing shape of a store.Attachment. BlobID is
// hex-encoded (see blobIDString); the manifest and encryption key ID are
// internal and never leave the core layer.
type AttachmentDTO struct {
	BlobID      string `json:"blob_id"`
	WorkspaceID string `json:"workspace_id"`
	SizeBytes   uint64 `json:"size_bytes"`
}

func attachmentDTO(a store.Attachment) AttachmentDTO {
	return AttachmentDTO{BlobID: blobIDString(a.BlobID), WorkspaceID: idString(a.WorkspaceID), SizeBytes: a.SizeBytes}
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
	return attachmentDTO(attachment), nil
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
