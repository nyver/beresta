package account

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/model"
	"github.com/beresta-app/beresta/core/store"
	"github.com/beresta-app/beresta/core/sync"
)

const (
	maxAttachmentDisplayNameBytes = 1024
	maxAttachmentMediaTypeBytes   = 255
)

var (
	// ErrInvalidAttachmentMetadata reports an empty, oversized, or otherwise
	// invalid display name or media type.
	ErrInvalidAttachmentMetadata = errors.New("account: invalid attachment metadata")
	// ErrAttachmentBlobOrphaned reports that this content's blob was already
	// published to the content-addressed store but has no attachment row
	// (the local record of an earlier attempt that crashed between
	// publishing the blob and committing its manifest). The blob's
	// encryption nonces are lost with that crashed attempt, so it can never
	// be completed; the caller must wait for garbage collection (task 4.9)
	// to reclaim the orphaned blob before retrying.
	ErrAttachmentBlobOrphaned = errors.New("account: attachment content already published without a manifest; wait for garbage collection")
)

// AddAttachment encrypts and durably publishes plaintext as one attachment
// in workspaceID and attaches it to noteID, recording the reference as a
// signed encrypted outbox operation. It is dedup-safe: attaching plaintext
// already attached anywhere else in the same workspace reuses the existing
// published blob and manifest instead of re-encrypting.
func (a *Account) AddAttachment(ctx context.Context, workspaceID, noteID model.ID, displayName, mediaType string, plaintext io.Reader) (store.Attachment, error) {
	if err := validateAttachmentDisplayName(displayName); err != nil {
		return store.Attachment{}, err
	}
	if err := validateAttachmentMediaType(mediaType); err != nil {
		return store.Attachment{}, err
	}

	db, entry, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return store.Attachment{}, err
	}
	// Validate the note before doing any staging/encryption/publish work:
	// otherwise a wrong-workspace noteID would still publish a blob and
	// create its attachment row, then fail only at the final note-reference
	// step below, leaving a row with no note reference and no way to ever
	// trigger the orphan tracking that (only) SetNoteAttachment starts.
	note, err := store.GetNote(ctx, db, noteID)
	if err != nil {
		return store.Attachment{}, err
	}
	if note.WorkspaceID != workspaceID {
		return store.Attachment{}, store.ErrWrongWorkspace
	}

	staged, err := a.stagePlaintext(plaintext)
	if err != nil {
		return store.Attachment{}, err
	}
	defer func() {
		staged.Close()
		os.Remove(staged.Name())
	}()

	blobIDBytes, totalSize, err := corecrypto.ComputeBlobID(ctx, corecrypto.CryptoProfileV1, entry.Key, workspaceID.Bytes(), staged)
	if err != nil {
		return store.Attachment{}, err
	}
	blobID, err := store.ParseBlobID(blobIDBytes)
	if err != nil {
		return store.Attachment{}, err
	}

	attachment, err := store.GetAttachment(ctx, db, blobID)
	switch {
	case err == nil:
		// Already published and recorded by an earlier attach (possibly for
		// a different note); reuse it.
	case errors.Is(err, store.ErrNotFound):
		published, existsErr := a.blobs.Exists(blobID)
		if existsErr != nil {
			return store.Attachment{}, existsErr
		}
		if published {
			return store.Attachment{}, ErrAttachmentBlobOrphaned
		}
		attachment, err = a.publishAndRecordAttachment(ctx, db, entry, workspaceID, blobID, staged, totalSize, displayName, mediaType)
		if err != nil {
			return store.Attachment{}, err
		}
	default:
		return store.Attachment{}, err
	}

	if err := a.commitNoteMetadata(ctx, workspaceID, sync.NoteMetadataOperation{
		NoteID: noteID, Kind: sync.NoteMetadataKindAttachment, AttachmentBlobID: blobID.Bytes(), AttachmentPresent: true,
	}, func(ctx context.Context, tx store.Executor, clock model.HLC) error {
		return store.SetNoteAttachment(ctx, tx, noteID, blobID, true, clock, int64(clock.PhysicalMS))
	}); err != nil {
		return store.Attachment{}, err
	}
	return attachment, nil
}

// AttachmentInfo is one attachment's display metadata for a note, decrypted
// from its manifest: everything a client UI needs to list, preview, and
// offer to save an attachment without exposing the raw manifest or chunk
// layout.
type AttachmentInfo struct {
	BlobID      store.BlobID
	DisplayName string
	MediaType   string
	SizeBytes   uint64
}

// ListNoteAttachments returns the display metadata for every attachment
// currently present on noteID, in no particular guaranteed order.
func (a *Account) ListNoteAttachments(ctx context.Context, workspaceID, noteID model.ID) ([]AttachmentInfo, error) {
	db, entry, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return nil, err
	}
	note, err := store.GetNote(ctx, db, noteID)
	if err != nil {
		return nil, err
	}
	if note.WorkspaceID != workspaceID {
		return nil, store.ErrWrongWorkspace
	}

	blobIDs, err := store.NoteAttachmentBlobIDs(ctx, db, noteID)
	if err != nil {
		return nil, err
	}
	infos := make([]AttachmentInfo, 0, len(blobIDs))
	for _, blobID := range blobIDs {
		row, err := store.GetAttachment(ctx, db, blobID)
		if err != nil {
			return nil, err
		}
		if row.WorkspaceID != workspaceID {
			return nil, store.ErrWrongWorkspace
		}
		metadata := corecrypto.AttachmentMetadata{
			SchemaVersion: corecrypto.AttachmentSchemaVersion,
			CryptoProfile: corecrypto.CryptoProfileV1,
			WorkspaceID:   workspaceID.Bytes(),
			BlobID:        blobID.Bytes(),
			KeyID:         row.KeyID,
		}
		payload, err := openAttachmentManifest(entry.Key, metadata, row.Manifest)
		if err != nil {
			return nil, err
		}
		infos = append(infos, AttachmentInfo{
			BlobID:      blobID,
			DisplayName: payload.displayName,
			MediaType:   payload.mediaType,
			SizeBytes:   payload.plaintextSize,
		})
	}
	return infos, nil
}

// RemoveAttachment removes noteID's reference to one attachment. The
// published blob itself is left in place for garbage collection (task 4.9)
// to reclaim once it is unreferenced and past the minimum retention window.
func (a *Account) RemoveAttachment(ctx context.Context, workspaceID, noteID model.ID, blobID store.BlobID) error {
	return a.commitNoteMetadata(ctx, workspaceID, sync.NoteMetadataOperation{
		NoteID: noteID, Kind: sync.NoteMetadataKindAttachment, AttachmentBlobID: blobID.Bytes(), AttachmentPresent: false,
	}, func(ctx context.Context, tx store.Executor, clock model.HLC) error {
		return store.SetNoteAttachment(ctx, tx, noteID, blobID, false, clock, int64(clock.PhysicalMS))
	})
}

// ReadAttachment authenticates and streams one attachment's plaintext to
// destination, returning its display name and media type. It fails closed:
// destination receives no output that has not passed complete AEAD and
// content-address verification.
func (a *Account) ReadAttachment(ctx context.Context, workspaceID model.ID, blobID store.BlobID, destination io.Writer) (displayName, mediaType string, err error) {
	db, entry, _, _, err := a.workspaceSession(workspaceID)
	if err != nil {
		return "", "", err
	}
	return readAttachmentFrom(ctx, db, a.blobs, entry.Key, workspaceID, blobID, destination)
}

// readAttachmentFrom authenticates and streams one attachment's plaintext
// to destination, sourcing its catalog row from sourceDB and its published
// blob from sourceBlobs. It is shared by ReadAttachment (the live account)
// and whole/selective restore, which read the same shape of data out of a
// backup set's plaintext database export and blob directory instead.
func readAttachmentFrom(ctx context.Context, sourceDB store.Executor, sourceBlobs *store.BlobStore, workspaceKey *corecrypto.Secret, workspaceID model.ID, blobID store.BlobID, destination io.Writer) (displayName, mediaType string, err error) {
	row, err := store.GetAttachment(ctx, sourceDB, blobID)
	if err != nil {
		return "", "", err
	}
	if row.WorkspaceID != workspaceID {
		return "", "", store.ErrWrongWorkspace
	}

	metadata := corecrypto.AttachmentMetadata{
		SchemaVersion: corecrypto.AttachmentSchemaVersion,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		BlobID:        blobID.Bytes(),
		KeyID:         row.KeyID,
	}
	payload, err := openAttachmentManifest(workspaceKey, metadata, row.Manifest)
	if err != nil {
		return "", "", err
	}

	blobFile, err := sourceBlobs.Open(blobID)
	if err != nil {
		return "", "", err
	}
	defer blobFile.Close()

	chunks := make([]corecrypto.EncryptedAttachmentChunk, len(payload.chunks))
	for i, rec := range payload.chunks {
		ciphertext := make([]byte, rec.ciphertextSize)
		if _, err := io.ReadFull(blobFile, ciphertext); err != nil {
			return "", "", fmt.Errorf("account: read attachment chunk: %w", err)
		}
		chunks[i] = corecrypto.EncryptedAttachmentChunk{
			Metadata: corecrypto.AttachmentChunkMetadata{
				CryptoProfile: corecrypto.CryptoProfileV1,
				WorkspaceID:   workspaceID.Bytes(),
				BlobID:        blobID.Bytes(),
				KeyID:         row.KeyID,
				ChunkIndex:    uint32(i),
				PlaintextSize: rec.plaintextSize,
			},
			Nonce:            append([]byte(nil), rec.nonce[:]...),
			Ciphertext:       ciphertext,
			CiphertextSHA256: append([]byte(nil), rec.ciphertextSHA256[:]...),
		}
	}

	if _, err := corecrypto.VerifyAttachment(ctx, workspaceKey, metadata, chunks, destination); err != nil {
		return "", "", err
	}
	return payload.displayName, payload.mediaType, nil
}

// publishAndRecordAttachment encrypts staged (already positioned or
// positionable at its start) into independent chunks, durably publishes
// them as one blob, and records the resulting manifest and attachment row.
// It is called only once per distinct BlobID: the caller has already
// confirmed no attachment row and no published blob exist for it.
func (a *Account) publishAndRecordAttachment(ctx context.Context, db store.Executor, entry workspaceKeyEntry, workspaceID model.ID, blobID store.BlobID, staged *os.File, totalSize uint64, displayName, mediaType string) (store.Attachment, error) {
	metaTemplate := corecrypto.AttachmentMetadata{
		SchemaVersion: corecrypto.AttachmentSchemaVersion,
		CryptoProfile: corecrypto.CryptoProfileV1,
		WorkspaceID:   workspaceID.Bytes(),
		BlobID:        blobID.Bytes(),
		KeyID:         entry.KeyID,
	}

	var chunks []attachmentChunkRecord
	publish := func(w io.Writer) error {
		if _, err := staged.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("account: seek staged attachment: %w", err)
		}
		sealer := corecrypto.NewAttachmentChunkSealer()
		buffer := make([]byte, corecrypto.AttachmentChunkBytes)
		index := uint32(0)
		for {
			n, readErr := io.ReadFull(staged, buffer)
			if n > 0 {
				if err := sealAndWriteChunk(w, sealer, entry.Key, metaTemplate, index, buffer[:n], &chunks); err != nil {
					return err
				}
				index++
			}
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				break
			}
			if readErr != nil {
				return fmt.Errorf("account: read staged attachment: %w", readErr)
			}
		}
		if index == 0 {
			// schema/v1/attachment-manifest.md: an empty attachment still has
			// exactly one, zero-length chunk.
			if err := sealAndWriteChunk(w, sealer, entry.Key, metaTemplate, 0, nil, &chunks); err != nil {
				return err
			}
		}
		return nil
	}
	if _, err := a.blobs.Publish(ctx, blobID, publish); err != nil {
		return store.Attachment{}, fmt.Errorf("account: publish attachment blob: %w", err)
	}

	manifestPlaintext, err := encodeAttachmentManifest(attachmentManifestPayload{
		plaintextSize: totalSize,
		mediaType:     mediaType,
		displayName:   displayName,
		chunks:        chunks,
	})
	if err != nil {
		return store.Attachment{}, err
	}
	manifestSecret, err := corecrypto.TakeSecret(manifestPlaintext)
	if err != nil {
		return store.Attachment{}, err
	}
	defer manifestSecret.Close()

	envelope, err := corecrypto.EncryptManifest(entry.Key, metaTemplate, manifestSecret)
	if err != nil {
		return store.Attachment{}, fmt.Errorf("account: encrypt attachment manifest: %w", err)
	}
	manifestBlob := make([]byte, 0, len(envelope.Nonce)+len(envelope.Ciphertext))
	manifestBlob = append(manifestBlob, envelope.Nonce...)
	manifestBlob = append(manifestBlob, envelope.Ciphertext...)

	return store.CreateAttachment(ctx, db, workspaceID, blobID, entry.KeyID, manifestBlob, totalSize, uint32(len(chunks)), time.Now().UnixMilli())
}

// sealAndWriteChunk encrypts one attachment chunk, writes its ciphertext to
// w, and appends the manifest record a future ReadAttachment needs to
// reassemble and re-authenticate it.
func sealAndWriteChunk(w io.Writer, sealer *corecrypto.AttachmentChunkSealer, key *corecrypto.Secret, metaTemplate corecrypto.AttachmentMetadata, index uint32, plaintext []byte, chunks *[]attachmentChunkRecord) error {
	sealed, err := sealer.SealChunk(key, metaTemplate, index, plaintext)
	if err != nil {
		return fmt.Errorf("account: seal attachment chunk: %w", err)
	}
	if _, err := w.Write(sealed.Ciphertext); err != nil {
		return fmt.Errorf("account: write attachment chunk: %w", err)
	}
	var nonce [corecrypto.XChaCha20NonceBytes]byte
	copy(nonce[:], sealed.Nonce)
	var hash [sha256.Size]byte
	copy(hash[:], sealed.CiphertextSHA256)
	*chunks = append(*chunks, attachmentChunkRecord{
		nonce:            nonce,
		ciphertextSize:   uint32(len(sealed.Ciphertext)),
		plaintextSize:    uint32(len(plaintext)),
		ciphertextSHA256: hash,
	})
	return nil
}

// stagePlaintext buffers plaintext into a new temporary file on the blob
// store's staging volume, so callers that need two passes over the content
// (compute its content address, then encrypt it) can seek back to the
// start between passes without holding it all in memory. The caller owns
// the returned file and must close and remove it.
func (a *Account) stagePlaintext(plaintext io.Reader) (*os.File, error) {
	tempDir := a.blobs.TempDir()
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return nil, fmt.Errorf("account: create attachment staging directory: %w", err)
	}
	f, err := os.CreateTemp(tempDir, "attachment-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("account: create attachment staging file: %w", err)
	}
	cleanup := func() {
		f.Close()
		os.Remove(f.Name())
	}

	limited := io.LimitReader(plaintext, int64(corecrypto.MaxAttachmentPlaintextBytes)+1)
	written, err := io.Copy(f, limited)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("account: stage attachment plaintext: %w", err)
	}
	if written > int64(corecrypto.MaxAttachmentPlaintextBytes) {
		cleanup()
		return nil, corecrypto.ErrAttachmentResourceLimit
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("account: sync staged attachment: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("account: seek staged attachment: %w", err)
	}
	return f, nil
}

func validateAttachmentDisplayName(name string) error {
	if len(name) == 0 || len(name) > maxAttachmentDisplayNameBytes {
		return fmt.Errorf("%w: display name length", ErrInvalidAttachmentMetadata)
	}
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return fmt.Errorf("%w: display name contains a path separator or control character", ErrInvalidAttachmentMetadata)
		}
	}
	return nil
}

func validateAttachmentMediaType(mediaType string) error {
	if len(mediaType) == 0 || len(mediaType) > maxAttachmentMediaTypeBytes {
		return fmt.Errorf("%w: media type length", ErrInvalidAttachmentMetadata)
	}
	return nil
}

// attachmentManifestPayloadVersion identifies this package's private
// on-disk encoding for a decrypted attachment manifest. Like
// keybagPayloadVersion, this is not the wire-format
// beresta.attachment-manifest.v1 map from schema/v1/attachment-manifest.md:
// the manifest plaintext never leaves the device unencrypted, so its
// internal encoding is free to evolve independently of the synchronization
// wire format formalized in tasks.md phase 6/7.
const attachmentManifestPayloadVersion = 1

// ErrInvalidAttachmentManifest reports a malformed decrypted attachment
// manifest. This can only occur from local storage corruption, since the
// manifest is authenticated before this package ever decodes it.
var ErrInvalidAttachmentManifest = errors.New("account: invalid attachment manifest payload")

// attachmentChunkRecord is one manifest entry describing an independently
// encrypted, independently authenticated attachment chunk.
type attachmentChunkRecord struct {
	nonce            [corecrypto.XChaCha20NonceBytes]byte
	ciphertextSize   uint32
	plaintextSize    uint32
	ciphertextSHA256 [sha256.Size]byte
}

// attachmentManifestPayload is the decrypted content of one attachment
// manifest: everything needed to reassemble and re-authenticate the
// attachment's chunks, plus the caller-supplied display metadata.
type attachmentManifestPayload struct {
	plaintextSize uint64
	mediaType     string
	displayName   string
	chunks        []attachmentChunkRecord
}

func encodeAttachmentManifest(payload attachmentManifestPayload) ([]byte, error) {
	if len(payload.chunks) == 0 || len(payload.chunks) > corecrypto.MaxAttachmentChunks {
		return nil, fmt.Errorf("%w: chunk count", ErrInvalidAttachmentManifest)
	}
	buf := make([]byte, 0, 15+len(payload.mediaType)+len(payload.displayName)+len(payload.chunks)*attachmentChunkRecordBytes)
	buf = append(buf, attachmentManifestPayloadVersion)
	buf = binary.BigEndian.AppendUint64(buf, payload.plaintextSize)
	buf = appendShortString(buf, payload.mediaType)
	buf = appendShortString(buf, payload.displayName)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(payload.chunks)))
	for _, c := range payload.chunks {
		buf = append(buf, c.nonce[:]...)
		buf = binary.BigEndian.AppendUint32(buf, c.ciphertextSize)
		buf = binary.BigEndian.AppendUint32(buf, c.plaintextSize)
		buf = append(buf, c.ciphertextSHA256[:]...)
	}
	return buf, nil
}

func decodeAttachmentManifest(data []byte) (attachmentManifestPayload, error) {
	if len(data) < 1+8+2 {
		return attachmentManifestPayload{}, fmt.Errorf("%w: truncated header", ErrInvalidAttachmentManifest)
	}
	if data[0] != attachmentManifestPayloadVersion {
		return attachmentManifestPayload{}, fmt.Errorf("%w: unknown version %d", ErrInvalidAttachmentManifest, data[0])
	}
	plaintextSize := binary.BigEndian.Uint64(data[1:9])
	rest := data[9:]

	mediaType, rest, err := readShortString(rest)
	if err != nil {
		return attachmentManifestPayload{}, err
	}
	displayName, rest, err := readShortString(rest)
	if err != nil {
		return attachmentManifestPayload{}, err
	}
	if len(rest) < 4 {
		return attachmentManifestPayload{}, fmt.Errorf("%w: truncated chunk count", ErrInvalidAttachmentManifest)
	}
	chunkCount := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if chunkCount == 0 || chunkCount > corecrypto.MaxAttachmentChunks {
		return attachmentManifestPayload{}, fmt.Errorf("%w: chunk count", ErrInvalidAttachmentManifest)
	}
	if len(rest) != int(chunkCount)*attachmentChunkRecordBytes {
		return attachmentManifestPayload{}, fmt.Errorf("%w: chunk records length", ErrInvalidAttachmentManifest)
	}

	chunks := make([]attachmentChunkRecord, chunkCount)
	for i := range chunks {
		var rec attachmentChunkRecord
		copy(rec.nonce[:], rest[:corecrypto.XChaCha20NonceBytes])
		rest = rest[corecrypto.XChaCha20NonceBytes:]
		rec.ciphertextSize = binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		rec.plaintextSize = binary.BigEndian.Uint32(rest[:4])
		rest = rest[4:]
		copy(rec.ciphertextSHA256[:], rest[:sha256.Size])
		rest = rest[sha256.Size:]
		chunks[i] = rec
	}
	return attachmentManifestPayload{plaintextSize: plaintextSize, mediaType: mediaType, displayName: displayName, chunks: chunks}, nil
}

const attachmentChunkRecordBytes = corecrypto.XChaCha20NonceBytes + 4 + 4 + sha256.Size

// openAttachmentManifest unpacks and authenticates a manifest blob produced
// by publishAndRecordAttachment (nonce || ciphertext; the manifest's key ID
// is stored separately in the attachments row and supplied via
// metadata.KeyID).
func openAttachmentManifest(workspaceKey *corecrypto.Secret, metadata corecrypto.AttachmentMetadata, manifestBlob []byte) (attachmentManifestPayload, error) {
	if len(manifestBlob) < corecrypto.XChaCha20NonceBytes+corecrypto.AEADTagBytes {
		return attachmentManifestPayload{}, fmt.Errorf("%w: truncated manifest blob", ErrInvalidAttachmentManifest)
	}
	envelope := corecrypto.EncryptedAttachmentManifest{
		Metadata:   metadata,
		Nonce:      manifestBlob[:corecrypto.XChaCha20NonceBytes],
		Ciphertext: manifestBlob[corecrypto.XChaCha20NonceBytes:],
	}
	secret, err := corecrypto.OpenManifest(workspaceKey, envelope)
	if err != nil {
		return attachmentManifestPayload{}, err
	}
	defer secret.Close()

	var payload attachmentManifestPayload
	if err := secret.Use(func(b []byte) error {
		var decodeErr error
		payload, decodeErr = decodeAttachmentManifest(b)
		return decodeErr
	}); err != nil {
		return attachmentManifestPayload{}, err
	}
	return payload, nil
}

func appendShortString(dst []byte, s string) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(s)))
	return append(dst, s...)
}

func readShortString(data []byte) (string, []byte, error) {
	if len(data) < 2 {
		return "", nil, fmt.Errorf("%w: truncated string length", ErrInvalidAttachmentManifest)
	}
	n := binary.BigEndian.Uint16(data)
	data = data[2:]
	if len(data) < int(n) {
		return "", nil, fmt.Errorf("%w: truncated string", ErrInvalidAttachmentManifest)
	}
	return string(data[:n]), data[n:], nil
}
