package sync

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/beresta-app/beresta/core/model"
)

// ErrMalformedNoteBodyOperation reports plaintext operation-payload bytes
// that are truncated, carry an unknown version, or otherwise fail to decode.
var ErrMalformedNoteBodyOperation = errors.New("sync: malformed note body operation")

const noteBodyOperationVersion = 1

// NoteBodyOperation is the decrypted payload of one outbox/inbox operation
// recording a local note command that touches a note's CRDT body, its
// title, or both. It covers the fields schema/v1/operation.md's
// `beresta.operation-payload.v1` needs for note-body commands specifically
// (mutation_kind, object_id, crdt_update, and a title metadata update);
// attachment references, tombstones, and causal context belong to the tasks
// that produce them and are not modeled here. The full cross-language wire
// codec with strict decoding, size limits, version negotiation, and fuzz
// testing is formalized once a synchronization transport exists (see
// tasks.md phase 6); until then this encoding only has to round-trip through
// Beresta's own local outbox and stay internally consistent.
type NoteBodyOperation struct {
	NoteID model.ID
	// CRDTUpdate is a Yjs update to apply to the note's body. Nil when this
	// command only changes metadata.
	CRDTUpdate []byte
	// Title, when non-nil, renames the note.
	Title *string
}

// EncodeNoteBodyOperation serializes op into plaintext operation-payload
// bytes, ready for encryption. At least one of CRDTUpdate or Title must be
// set, matching the schema's "at least one mutation field must be non-empty"
// rule.
func EncodeNoteBodyOperation(op NoteBodyOperation) ([]byte, error) {
	if op.NoteID.IsZero() {
		return nil, fmt.Errorf("%w: missing note ID", ErrMalformedNoteBodyOperation)
	}
	if op.CRDTUpdate == nil && op.Title == nil {
		return nil, fmt.Errorf("%w: no mutation field set", ErrMalformedNoteBodyOperation)
	}

	buf := make([]byte, 0, 18+len(op.CRDTUpdate))
	buf = append(buf, noteBodyOperationVersion)
	buf = append(buf, op.NoteID.Bytes()...)

	var flags byte
	if op.CRDTUpdate != nil {
		flags |= 1
	}
	if op.Title != nil {
		flags |= 2
	}
	buf = append(buf, flags)

	if op.CRDTUpdate != nil {
		buf = appendLengthPrefixed(buf, op.CRDTUpdate)
	}
	if op.Title != nil {
		buf = appendLengthPrefixed(buf, []byte(*op.Title))
	}
	return buf, nil
}

// DecodeNoteBodyOperation parses bytes produced by EncodeNoteBodyOperation.
func DecodeNoteBodyOperation(data []byte) (NoteBodyOperation, error) {
	const headerBytes = 1 + 16 + 1
	if len(data) < headerBytes {
		return NoteBodyOperation{}, fmt.Errorf("%w: truncated header", ErrMalformedNoteBodyOperation)
	}
	if data[0] != noteBodyOperationVersion {
		return NoteBodyOperation{}, fmt.Errorf("%w: unknown version %d", ErrMalformedNoteBodyOperation, data[0])
	}
	noteID, err := model.ParseID(data[1:17])
	if err != nil {
		return NoteBodyOperation{}, fmt.Errorf("%w: note ID: %v", ErrMalformedNoteBodyOperation, err)
	}
	flags := data[17]
	if flags == 0 || flags&^byte(3) != 0 {
		return NoteBodyOperation{}, fmt.Errorf("%w: invalid flags", ErrMalformedNoteBodyOperation)
	}
	rest := data[headerBytes:]

	op := NoteBodyOperation{NoteID: noteID}
	if flags&1 != 0 {
		update, tail, err := readLengthPrefixed(rest)
		if err != nil {
			return NoteBodyOperation{}, err
		}
		op.CRDTUpdate = update
		rest = tail
	}
	if flags&2 != 0 {
		titleBytes, tail, err := readLengthPrefixed(rest)
		if err != nil {
			return NoteBodyOperation{}, err
		}
		if !utf8.Valid(titleBytes) {
			return NoteBodyOperation{}, fmt.Errorf("%w: title is not valid UTF-8", ErrMalformedNoteBodyOperation)
		}
		title := string(titleBytes)
		op.Title = &title
		rest = tail
	}
	if len(rest) != 0 {
		return NoteBodyOperation{}, fmt.Errorf("%w: trailing bytes", ErrMalformedNoteBodyOperation)
	}
	return op, nil
}

func appendLengthPrefixed(dst, value []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func readLengthPrefixed(data []byte) (value, rest []byte, err error) {
	if len(data) < 4 {
		return nil, nil, fmt.Errorf("%w: truncated length prefix", ErrMalformedNoteBodyOperation)
	}
	length := binary.BigEndian.Uint32(data)
	data = data[4:]
	if uint64(length) > uint64(len(data)) {
		return nil, nil, fmt.Errorf("%w: length prefix exceeds remaining data", ErrMalformedNoteBodyOperation)
	}
	return data[:length], data[length:], nil
}
