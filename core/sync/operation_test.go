package sync

import (
	"errors"
	"testing"

	"github.com/beresta-app/beresta/core/model"
)

func testNoteID(t *testing.T) model.ID {
	t.Helper()
	id, err := model.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNoteBodyOperationRoundTrip(t *testing.T) {
	noteID := testNoteID(t)
	title := "My note"

	for _, op := range []NoteBodyOperation{
		{NoteID: noteID, CRDTUpdate: []byte{1, 2, 3, 4}},
		{NoteID: noteID, Title: &title},
		{NoteID: noteID, CRDTUpdate: []byte{5, 6}, Title: &title},
	} {
		encoded, err := EncodeNoteBodyOperation(op)
		if err != nil {
			t.Fatalf("encode %+v: %v", op, err)
		}
		decoded, err := DecodeNoteBodyOperation(encoded)
		if err != nil {
			t.Fatalf("decode %+v: %v", op, err)
		}
		if decoded.NoteID != op.NoteID {
			t.Fatalf("NoteID = %v, want %v", decoded.NoteID, op.NoteID)
		}
		if string(decoded.CRDTUpdate) != string(op.CRDTUpdate) {
			t.Fatalf("CRDTUpdate = %v, want %v", decoded.CRDTUpdate, op.CRDTUpdate)
		}
		if (decoded.Title == nil) != (op.Title == nil) || (decoded.Title != nil && *decoded.Title != *op.Title) {
			t.Fatalf("Title = %v, want %v", decoded.Title, op.Title)
		}
	}
}

func TestEncodeNoteBodyOperationRejectsEmptyCommand(t *testing.T) {
	if _, err := EncodeNoteBodyOperation(NoteBodyOperation{NoteID: testNoteID(t)}); !errors.Is(err, ErrMalformedNoteBodyOperation) {
		t.Fatalf("error = %v, want ErrMalformedNoteBodyOperation", err)
	}
	title := "x"
	if _, err := EncodeNoteBodyOperation(NoteBodyOperation{Title: &title}); !errors.Is(err, ErrMalformedNoteBodyOperation) {
		t.Fatalf("missing note ID error = %v, want ErrMalformedNoteBodyOperation", err)
	}
}

func TestDecodeNoteBodyOperationRejectsMalformedInput(t *testing.T) {
	title := "hello"
	valid, err := EncodeNoteBodyOperation(NoteBodyOperation{NoteID: testNoteID(t), Title: &title})
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string][]byte{
		"empty":            {},
		"truncated header": valid[:10],
		"bad version":      append([]byte{0xff}, valid[1:]...),
		"invalid flags":    append(append([]byte{}, valid[:17]...), 0xff),
		"trailing bytes":   append(append([]byte{}, valid...), 0x00),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeNoteBodyOperation(data); !errors.Is(err, ErrMalformedNoteBodyOperation) {
				t.Fatalf("error = %v, want ErrMalformedNoteBodyOperation", err)
			}
		})
	}

	// A length prefix claiming more bytes than remain must be rejected too.
	truncatedLength := append([]byte{}, valid[:18]...)
	truncatedLength = append(truncatedLength, 0x00, 0x00, 0x00, 0x7f) // claims 127 bytes, none present
	if _, err := DecodeNoteBodyOperation(truncatedLength); !errors.Is(err, ErrMalformedNoteBodyOperation) {
		t.Fatalf("oversized length prefix error = %v, want ErrMalformedNoteBodyOperation", err)
	}
}

func TestDecodeNoteBodyOperationRejectsInvalidUTF8Title(t *testing.T) {
	title := "x"
	valid, err := EncodeNoteBodyOperation(NoteBodyOperation{NoteID: testNoteID(t), Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the single-byte title payload (last byte) with an invalid UTF-8 lead byte.
	corrupted := append([]byte{}, valid...)
	corrupted[len(corrupted)-1] = 0xff
	if _, err := DecodeNoteBodyOperation(corrupted); !errors.Is(err, ErrMalformedNoteBodyOperation) {
		t.Fatalf("error = %v, want ErrMalformedNoteBodyOperation", err)
	}
}
