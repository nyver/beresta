package sync

import (
	"errors"
	"fmt"

	"github.com/beresta-app/beresta/core/model"
)

// ErrMalformedNoteMetadataOperation reports plaintext operation-payload
// bytes that are truncated, carry an unknown version or kind, or otherwise
// fail to decode.
var ErrMalformedNoteMetadataOperation = errors.New("sync: malformed note metadata operation")

const noteMetadataOperationVersion = 1

// AttachmentBlobIDBytes is the length of the workspace-private attachment
// identifier carried by a NoteMetadataKindAttachment operation (see
// core/crypto.ComputeBlobID / core/store.BlobIDBytes). It is duplicated here
// rather than imported to keep this package's dependency on domain
// identifiers narrow; both constants are pinned to the same crypto_profile_v1
// value.
const AttachmentBlobIDBytes = 32

// NoteMetadataKind identifies which single metadata field a
// NoteMetadataOperation changes.
type NoteMetadataKind uint8

const (
	// NoteMetadataKindNotebook reassigns a note's notebook (NotebookID; the
	// zero ID means the workspace root).
	NoteMetadataKindNotebook NoteMetadataKind = iota + 1
	// NoteMetadataKindTag adds or removes the note's membership in one tag
	// (TagID, TagPresent).
	NoteMetadataKindTag
	// NoteMetadataKindFlags replaces the note's flag bitmask (Flags).
	NoteMetadataKindFlags
	// NoteMetadataKindDeleted sets or clears the note's delete tombstone
	// (Deleted).
	NoteMetadataKindDeleted
	// NoteMetadataKindAttachment adds or removes the note's reference to one
	// attachment (AttachmentBlobID, AttachmentPresent).
	NoteMetadataKindAttachment
)

func (k NoteMetadataKind) valid() bool {
	return k >= NoteMetadataKindNotebook && k <= NoteMetadataKindAttachment
}

// NoteMetadataOperation is the decrypted payload of one outbox/inbox
// operation recording a local metadata-only note command: notebook
// assignment, tag membership, flags, the delete tombstone, or an attachment
// reference. Exactly one field group is populated, selected by Kind. Body
// edits and title changes use NoteBodyOperation instead. Like
// NoteBodyOperation, this is Beresta's own internal outbox encoding, not the
// final cross-language wire codec formalized in tasks.md phase 6/7.
type NoteMetadataOperation struct {
	NoteID model.ID
	Kind   NoteMetadataKind

	NotebookID model.ID // Kind == NoteMetadataKindNotebook

	TagID      model.ID // Kind == NoteMetadataKindTag
	TagPresent bool     // Kind == NoteMetadataKindTag

	Flags model.NoteFlags // Kind == NoteMetadataKindFlags

	Deleted bool // Kind == NoteMetadataKindDeleted

	AttachmentBlobID  []byte // Kind == NoteMetadataKindAttachment, 32 bytes
	AttachmentPresent bool   // Kind == NoteMetadataKindAttachment
}

// EncodeNoteMetadataOperation serializes op into plaintext operation-payload
// bytes, ready for encryption.
func EncodeNoteMetadataOperation(op NoteMetadataOperation) ([]byte, error) {
	if op.NoteID.IsZero() {
		return nil, fmt.Errorf("%w: missing note ID", ErrMalformedNoteMetadataOperation)
	}
	if !op.Kind.valid() {
		return nil, fmt.Errorf("%w: invalid kind", ErrMalformedNoteMetadataOperation)
	}

	buf := make([]byte, 0, 18+AttachmentBlobIDBytes)
	buf = append(buf, noteMetadataOperationVersion)
	buf = append(buf, op.NoteID.Bytes()...)
	buf = append(buf, byte(op.Kind))

	switch op.Kind {
	case NoteMetadataKindNotebook:
		buf = append(buf, op.NotebookID.Bytes()...)
	case NoteMetadataKindTag:
		if op.TagID.IsZero() {
			return nil, fmt.Errorf("%w: missing tag ID", ErrMalformedNoteMetadataOperation)
		}
		buf = append(buf, op.TagID.Bytes()...)
		buf = append(buf, boolByte(op.TagPresent))
	case NoteMetadataKindFlags:
		buf = append(buf, byte(op.Flags>>24), byte(op.Flags>>16), byte(op.Flags>>8), byte(op.Flags))
	case NoteMetadataKindDeleted:
		buf = append(buf, boolByte(op.Deleted))
	case NoteMetadataKindAttachment:
		if len(op.AttachmentBlobID) != AttachmentBlobIDBytes {
			return nil, fmt.Errorf("%w: invalid attachment blob ID", ErrMalformedNoteMetadataOperation)
		}
		buf = append(buf, op.AttachmentBlobID...)
		buf = append(buf, boolByte(op.AttachmentPresent))
	}
	return buf, nil
}

// DecodeNoteMetadataOperation parses bytes produced by
// EncodeNoteMetadataOperation.
func DecodeNoteMetadataOperation(data []byte) (NoteMetadataOperation, error) {
	const headerBytes = 1 + 16 + 1
	if len(data) < headerBytes {
		return NoteMetadataOperation{}, fmt.Errorf("%w: truncated header", ErrMalformedNoteMetadataOperation)
	}
	if data[0] != noteMetadataOperationVersion {
		return NoteMetadataOperation{}, fmt.Errorf("%w: unknown version %d", ErrMalformedNoteMetadataOperation, data[0])
	}
	noteID, err := model.ParseID(data[1:17])
	if err != nil {
		return NoteMetadataOperation{}, fmt.Errorf("%w: note ID: %v", ErrMalformedNoteMetadataOperation, err)
	}
	kind := NoteMetadataKind(data[17])
	if !kind.valid() {
		return NoteMetadataOperation{}, fmt.Errorf("%w: unknown kind %d", ErrMalformedNoteMetadataOperation, kind)
	}
	rest := data[headerBytes:]
	op := NoteMetadataOperation{NoteID: noteID, Kind: kind}

	switch kind {
	case NoteMetadataKindNotebook:
		if len(rest) != 16 {
			return NoteMetadataOperation{}, fmt.Errorf("%w: notebook payload length", ErrMalformedNoteMetadataOperation)
		}
		// The zero ID (model.Nil) is a valid payload here: it means "file at
		// the workspace root", not "missing field". model.ParseID rejects
		// the all-zero value outright, so it only applies to a non-zero
		// candidate.
		var notebookID model.ID
		copy(notebookID[:], rest)
		if notebookID != model.Nil {
			if err := notebookID.Validate(); err != nil {
				return NoteMetadataOperation{}, fmt.Errorf("%w: notebook ID: %v", ErrMalformedNoteMetadataOperation, err)
			}
		}
		op.NotebookID = notebookID
	case NoteMetadataKindTag:
		if len(rest) != 17 {
			return NoteMetadataOperation{}, fmt.Errorf("%w: tag payload length", ErrMalformedNoteMetadataOperation)
		}
		tagID, err := model.ParseID(rest[:16])
		if err != nil {
			return NoteMetadataOperation{}, fmt.Errorf("%w: tag ID: %v", ErrMalformedNoteMetadataOperation, err)
		}
		present, err := decodeBoolByte(rest[16])
		if err != nil {
			return NoteMetadataOperation{}, err
		}
		op.TagID, op.TagPresent = tagID, present
	case NoteMetadataKindFlags:
		if len(rest) != 4 {
			return NoteMetadataOperation{}, fmt.Errorf("%w: flags payload length", ErrMalformedNoteMetadataOperation)
		}
		op.Flags = model.NoteFlags(uint32(rest[0])<<24 | uint32(rest[1])<<16 | uint32(rest[2])<<8 | uint32(rest[3]))
	case NoteMetadataKindDeleted:
		if len(rest) != 1 {
			return NoteMetadataOperation{}, fmt.Errorf("%w: deleted payload length", ErrMalformedNoteMetadataOperation)
		}
		deleted, err := decodeBoolByte(rest[0])
		if err != nil {
			return NoteMetadataOperation{}, err
		}
		op.Deleted = deleted
	case NoteMetadataKindAttachment:
		if len(rest) != AttachmentBlobIDBytes+1 {
			return NoteMetadataOperation{}, fmt.Errorf("%w: attachment payload length", ErrMalformedNoteMetadataOperation)
		}
		op.AttachmentBlobID = append([]byte(nil), rest[:AttachmentBlobIDBytes]...)
		present, err := decodeBoolByte(rest[AttachmentBlobIDBytes])
		if err != nil {
			return NoteMetadataOperation{}, err
		}
		op.AttachmentPresent = present
	}
	return op, nil
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func decodeBoolByte(b byte) (bool, error) {
	switch b {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("%w: invalid boolean byte", ErrMalformedNoteMetadataOperation)
	}
}
