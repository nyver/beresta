package model

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

const idBytes = 16

// ErrInvalidID reports a malformed, wrong-length, or structurally invalid
// identifier.
var ErrInvalidID = errors.New("model: invalid identifier")

// ID is a validated 16-byte RFC 9562 UUIDv7 identifier used for accounts,
// devices, workspaces, operations, objects, and snapshots.
//
// UUID timestamp bits are informational only; callers must not infer
// authorization, causality, or trust from identifier ordering.
type ID [idBytes]byte

// Nil is the zero-value identifier. It never passes Validate.
var Nil ID

// NewID generates a random RFC 9562 UUIDv7 identifier from the current time
// and the operating-system CSPRNG.
func NewID() (ID, error) {
	return newID(rand.Reader, time.Now())
}

func newID(random io.Reader, now time.Time) (ID, error) {
	var id ID
	if _, err := io.ReadFull(random, id[:]); err != nil {
		return Nil, fmt.Errorf("%w: random source failed", ErrInvalidID)
	}

	ms := uint64(now.UnixMilli()) & 0xffffffffffff // 48-bit big-endian timestamp field
	id[0] = byte(ms >> 40)
	id[1] = byte(ms >> 32)
	id[2] = byte(ms >> 24)
	id[3] = byte(ms >> 16)
	id[4] = byte(ms >> 8)
	id[5] = byte(ms)
	id[6] = (id[6] & 0x0f) | 0x70 // version 7
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 9562 variant 10
	return id, nil
}

// ParseID validates raw bytes as a well-formed UUIDv7: the correct length,
// version nibble, and variant bits, and not the all-zero value. It does not
// check timestamp plausibility.
func ParseID(value []byte) (ID, error) {
	if len(value) != idBytes {
		return Nil, ErrInvalidID
	}
	var id ID
	copy(id[:], value)
	if err := id.Validate(); err != nil {
		return Nil, err
	}
	return id, nil
}

// ParseIDString parses the canonical dashed hexadecimal text form produced
// by String, round-tripping through it to reject any non-canonical
// rendering (wrong case, missing dashes, extra characters) of an otherwise
// valid identifier.
func ParseIDString(value string) (ID, error) {
	if len(value) != 36 {
		return Nil, ErrInvalidID
	}
	raw := make([]byte, 0, idBytes)
	for i, segment := range []struct{ start, end int }{{0, 8}, {9, 13}, {14, 18}, {19, 23}, {24, 36}} {
		if i > 0 && value[segment.start-1] != '-' {
			return Nil, ErrInvalidID
		}
		decoded, err := hex.DecodeString(value[segment.start:segment.end])
		if err != nil {
			return Nil, ErrInvalidID
		}
		raw = append(raw, decoded...)
	}
	id, err := ParseID(raw)
	if err != nil || id.String() != value {
		return Nil, ErrInvalidID
	}
	return id, nil
}

// Validate reports whether id carries the required UUIDv7 version and
// variant bits and is not the all-zero value.
func (id ID) Validate() error {
	if id == Nil {
		return ErrInvalidID
	}
	if id[6]&0xf0 != 0x70 {
		return ErrInvalidID
	}
	if id[8]&0xc0 != 0x80 {
		return ErrInvalidID
	}
	return nil
}

// IsZero reports whether id is the all-zero sentinel value.
func (id ID) IsZero() bool {
	return id == Nil
}

// Bytes returns an independent copy of the identifier's 16 bytes.
func (id ID) Bytes() []byte {
	return append([]byte(nil), id[:]...)
}

// Compare returns -1, 0, or 1 by bytewise comparison. It is the fixed
// deterministic tie break used when two records otherwise sort equally.
func (id ID) Compare(other ID) int {
	return bytes.Compare(id[:], other[:])
}

// String renders the canonical dashed hexadecimal UUID text form. It is for
// diagnostics only; wire and signed encodings always use raw bytes.
func (id ID) String() string {
	var buf [36]byte
	hex.Encode(buf[0:8], id[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], id[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], id[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], id[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], id[10:16])
	return string(buf[:])
}
