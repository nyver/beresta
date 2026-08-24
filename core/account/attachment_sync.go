package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
)

// AttachmentChunk describes one opaque ciphertext chunk stored remotely.
type AttachmentChunk struct {
	Index  uint32
	Bytes  int64
	SHA256 []byte
}

// RemoteAttachment describes an attachment blob's authenticated server-side
// metadata. The server cannot decrypt either the manifest or the chunks.
type RemoteAttachment struct {
	KeyID    []byte
	Manifest []byte
	Bytes    int64
	Complete bool
	Chunks   []AttachmentChunk
	Uploaded map[uint32]bool
}

// AttachmentSyncTransport exchanges encrypted attachment ciphertext with a
// synchronization server. It deliberately exposes no plaintext or workspace
// keys to the transport layer.
type AttachmentSyncTransport interface {
	GetAttachment(context.Context, model.ID, store.BlobID) (RemoteAttachment, bool, error)
	BeginAttachment(context.Context, model.ID, store.BlobID, []byte, []byte, int64, []AttachmentChunk) (RemoteAttachment, error)
	PutAttachmentChunk(context.Context, model.ID, store.BlobID, uint32, []byte) error
	CompleteAttachment(context.Context, model.ID, store.BlobID) (RemoteAttachment, error)
	ReadAttachmentChunk(context.Context, model.ID, store.BlobID, uint32) ([]byte, error)
}

// SynchronizeWorkspaceAttachments makes every locally catalogued encrypted
// attachment available on both the device and the synchronization server.
// It is called after snapshot review and before note operations are pushed,
// so a remote device never observes a note reference before the corresponding
// attachment ciphertext can be fetched.
func (a *Account) SynchronizeWorkspaceAttachments(ctx context.Context, workspaceID model.ID, remote AttachmentSyncTransport) error {
	if remote == nil {
		return errors.New("account: attachment sync transport is required")
	}
	db, entry, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return err
	}
	attachments, err := store.ListAttachments(ctx, db, workspaceID)
	if err != nil {
		return err
	}
	for _, attachment := range attachments {
		if attachment.IsPlaceholder() {
			continue
		}
		if err := a.synchronizeAttachment(ctx, entry, workspaceID, attachment, remote); err != nil {
			return err
		}
	}
	return nil
}

func (a *Account) synchronizeAttachment(ctx context.Context, entry workspaceKeyEntry, workspaceID model.ID, attachment store.Attachment, remote AttachmentSyncTransport) error {
	chunks, totalBytes, err := attachmentSyncManifest(entry.Key, workspaceID, attachment)
	if err != nil {
		return err
	}
	remoteAttachment, remoteExists, err := remote.GetAttachment(ctx, workspaceID, attachment.BlobID)
	if err != nil {
		return err
	}
	localExists, err := a.blobs.Exists(attachment.BlobID)
	if err != nil {
		return err
	}
	if !remoteExists {
		if !localExists {
			return fmt.Errorf("account: attachment %x is unavailable on this device and server", attachment.BlobID.Bytes())
		}
		remoteAttachment, err = remote.BeginAttachment(ctx, workspaceID, attachment.BlobID, attachment.KeyID, attachment.Manifest, totalBytes, chunks)
		if err != nil {
			return err
		}
	}
	if err := validateRemoteAttachment(attachment, chunks, totalBytes, remoteAttachment); err != nil {
		return err
	}
	if !remoteAttachment.Complete {
		if !localExists {
			return fmt.Errorf("account: attachment %x is not complete on server", attachment.BlobID.Bytes())
		}
		if err := a.uploadAttachmentChunks(ctx, workspaceID, attachment.BlobID, chunks, remoteAttachment.Uploaded, remote); err != nil {
			return err
		}
		remoteAttachment, err = remote.CompleteAttachment(ctx, workspaceID, attachment.BlobID)
		if err != nil {
			return err
		}
		if err := validateRemoteAttachment(attachment, chunks, totalBytes, remoteAttachment); err != nil {
			return err
		}
	}
	if !remoteAttachment.Complete {
		return errors.New("account: server did not complete attachment publication")
	}
	if !localExists {
		if err := a.downloadAttachmentChunks(ctx, workspaceID, attachment.BlobID, chunks, remote); err != nil {
			return err
		}
	}
	return nil
}

func attachmentSyncManifest(key *corecrypto.Secret, workspaceID model.ID, attachment store.Attachment) ([]AttachmentChunk, int64, error) {
	metadata := corecrypto.AttachmentMetadata{
		SchemaVersion: corecrypto.AttachmentSchemaVersion,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		BlobID:        attachment.BlobID.Bytes(),
		KeyID:         attachment.KeyID,
	}
	manifest, err := openAttachmentManifest(key, metadata, attachment.Manifest)
	if err != nil {
		return nil, 0, err
	}
	if len(manifest.chunks) != int(attachment.ChunkCount) {
		return nil, 0, errors.New("account: attachment catalog chunk count mismatch")
	}
	chunks := make([]AttachmentChunk, len(manifest.chunks))
	var total int64
	for index, chunk := range manifest.chunks {
		if chunk.ciphertextSize == 0 || chunk.ciphertextSize > corecrypto.AttachmentChunkBytes+corecrypto.AEADTagBytes || total > int64(^uint64(0)>>1)-int64(chunk.ciphertextSize) {
			return nil, 0, errors.New("account: attachment ciphertext size is invalid")
		}
		chunks[index] = AttachmentChunk{Index: uint32(index), Bytes: int64(chunk.ciphertextSize), SHA256: append([]byte(nil), chunk.ciphertextSHA256[:]...)}
		total += int64(chunk.ciphertextSize)
	}
	return chunks, total, nil
}

func validateRemoteAttachment(attachment store.Attachment, chunks []AttachmentChunk, total int64, remote RemoteAttachment) error {
	if !bytes.Equal(remote.KeyID, attachment.KeyID) || !bytes.Equal(remote.Manifest, attachment.Manifest) || remote.Bytes != total || len(remote.Chunks) != len(chunks) {
		return errors.New("account: remote attachment metadata mismatch")
	}
	for index, expected := range chunks {
		actual := remote.Chunks[index]
		if actual.Index != expected.Index || actual.Bytes != expected.Bytes || !bytes.Equal(actual.SHA256, expected.SHA256) {
			return errors.New("account: remote attachment chunk metadata mismatch")
		}
	}
	return nil
}

func (a *Account) uploadAttachmentChunks(ctx context.Context, workspaceID model.ID, blobID store.BlobID, chunks []AttachmentChunk, uploaded map[uint32]bool, remote AttachmentSyncTransport) error {
	file, err := a.blobs.Open(blobID)
	if err != nil {
		return err
	}
	defer file.Close()
	for _, chunk := range chunks {
		contents := make([]byte, chunk.Bytes)
		if _, err := io.ReadFull(file, contents); err != nil {
			return fmt.Errorf("account: read local attachment chunk: %w", err)
		}
		if digest := sha256.Sum256(contents); !bytes.Equal(digest[:], chunk.SHA256) {
			return errors.New("account: local attachment chunk hash mismatch")
		}
		if uploaded[chunk.Index] {
			continue
		}
		if err := remote.PutAttachmentChunk(ctx, workspaceID, blobID, chunk.Index, contents); err != nil {
			return err
		}
	}
	return nil
}

func (a *Account) downloadAttachmentChunks(ctx context.Context, workspaceID model.ID, blobID store.BlobID, chunks []AttachmentChunk, remote AttachmentSyncTransport) error {
	_, err := a.blobs.Publish(ctx, blobID, func(destination io.Writer) error {
		for _, chunk := range chunks {
			contents, err := remote.ReadAttachmentChunk(ctx, workspaceID, blobID, chunk.Index)
			if err != nil {
				return err
			}
			if int64(len(contents)) != chunk.Bytes {
				return errors.New("account: remote attachment chunk size mismatch")
			}
			digest := sha256.Sum256(contents)
			if !bytes.Equal(digest[:], chunk.SHA256) {
				return errors.New("account: remote attachment chunk hash mismatch")
			}
			if _, err := destination.Write(contents); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("account: publish downloaded attachment: %w", err)
	}
	return nil
}
