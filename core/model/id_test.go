package model

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
	"testing/iotest"
	"time"
)

func TestNewIDIsValidUUIDv7(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if err := id.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if id[6]&0xf0 != 0x70 {
		t.Fatalf("version nibble = %x, want 7", id[6]&0xf0)
	}
	if id[8]&0xc0 != 0x80 {
		t.Fatalf("variant bits = %x, want 10", id[8]&0xc0>>6)
	}
}

func TestNewIDEncodesTimestamp(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	id, err := newID(rand.Reader, now)
	if err != nil {
		t.Fatalf("newID() error = %v", err)
	}
	wantMS := uint64(now.UnixMilli())
	gotMS := uint64(id[0])<<40 | uint64(id[1])<<32 | uint64(id[2])<<24 | uint64(id[3])<<16 | uint64(id[4])<<8 | uint64(id[5])
	if gotMS != wantMS {
		t.Fatalf("encoded timestamp = %d, want %d", gotMS, wantMS)
	}
}

func TestNewIDIsUniqueAcrossCalls(t *testing.T) {
	seen := make(map[ID]struct{})
	for i := 0; i < 1000; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatalf("NewID() error = %v", err)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNewIDRejectsRandomFailure(t *testing.T) {
	failing := iotest.ErrReader(errors.New("boom"))
	if _, err := newID(failing, time.Now()); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("newID() error = %v, want ErrInvalidID", err)
	}
}

func TestParseID(t *testing.T) {
	valid, err := NewID()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value []byte
	}{
		{"wrong length", valid.Bytes()[:15]},
		{"all zero", make([]byte, idBytes)},
		{"wrong version", tamperNibble(valid, 6, 0x40)},
		{"wrong variant", tamperNibble(valid, 8, 0x00)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseID(tt.value); !errors.Is(err, ErrInvalidID) {
				t.Fatalf("ParseID() error = %v, want ErrInvalidID", err)
			}
		})
	}

	parsed, err := ParseID(valid.Bytes())
	if err != nil {
		t.Fatalf("ParseID() error = %v", err)
	}
	if parsed != valid {
		t.Fatalf("ParseID() = %s, want %s", parsed, valid)
	}
}

func TestIDCompareAndIsZero(t *testing.T) {
	var low, high ID
	low[15] = 0x01
	high[15] = 0x02
	if Nil.Compare(low) >= 0 {
		t.Fatal("Nil should compare less than a non-zero ID")
	}
	if low.Compare(high) >= 0 {
		t.Fatal("low should compare less than high")
	}
	if high.Compare(low) <= 0 {
		t.Fatal("high should compare greater than low")
	}
	if low.Compare(low) != 0 {
		t.Fatal("an ID must compare equal to itself")
	}
	if !Nil.IsZero() {
		t.Fatal("Nil.IsZero() = false, want true")
	}
	if low.IsZero() {
		t.Fatal("low.IsZero() = true, want false")
	}
}

func TestIDStringFormat(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	s := id.String()
	if len(s) != 36 {
		t.Fatalf("len(String()) = %d, want 36", len(s))
	}
	for i, want := range []byte("________-____-____-____-____________") {
		if want == '-' && s[i] != '-' {
			t.Fatalf("String() = %q, missing dash at %d", s, i)
		}
	}
}

func TestIDBytesReturnsIndependentCopy(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b := id.Bytes()
	b[0] ^= 0xff
	if bytes.Equal(b, id.Bytes()) {
		t.Fatal("mutating the returned slice affected the ID")
	}
}

func tamperNibble(id ID, byteIndex int, mask byte) []byte {
	out := id.Bytes()
	if byteIndex == 6 {
		out[6] = (out[6] & 0x0f) | mask
	} else {
		out[8] = (out[8] & 0x3f) | mask
	}
	return out
}
