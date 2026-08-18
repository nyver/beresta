package yjsadapter

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type yjsFixture struct {
	TextRoot string `json:"text_root"`
	Text     string `json:"text"`
	V1Hex    string `json:"v1_hex"`
	V2Hex    string `json:"v2_hex"`
}

func TestOfficialYjsUpdatesRoundTripV1AndV2(t *testing.T) {
	fixture := loadYjsFixture(t)
	v1 := decodeHex(t, fixture.V1Hex)
	v2 := decodeHex(t, fixture.V2Hex)

	for _, test := range []struct {
		name   string
		format Format
		input  []byte
	}{
		{name: "v1", format: FormatV1, input: v1},
		{name: "v2", format: FormatV2, input: v2},
	} {
		t.Run(test.name, func(t *testing.T) {
			doc := New()
			defer doc.Close()

			if err := doc.ApplyUpdate(test.format, test.input); err != nil {
				t.Fatalf("apply official update: %v", err)
			}
			text, err := doc.Text(fixture.TextRoot)
			if err != nil {
				t.Fatalf("read text: %v", err)
			}
			if text != fixture.Text {
				t.Fatalf("text = %q, want %q", text, fixture.Text)
			}

			got, err := doc.EncodeStateAsUpdate(test.format)
			if err != nil {
				t.Fatalf("encode state: %v", err)
			}
			if !bytes.Equal(got, test.input) {
				t.Fatalf("same-format round trip changed bytes\n got: %x\nwant: %x", got, test.input)
			}
		})
	}

	v1Doc := New()
	defer v1Doc.Close()
	if err := v1Doc.ApplyUpdate(FormatV1, v1); err != nil {
		t.Fatal(err)
	}
	converted, err := v1Doc.EncodeStateAsUpdate(FormatV2)
	if err != nil {
		t.Fatal(err)
	}
	v2Doc := New()
	defer v2Doc.Close()
	if err := v2Doc.ApplyUpdate(FormatV2, converted); err != nil {
		t.Fatalf("apply cross-format update: %v", err)
	}
	got, err := v2Doc.Text(fixture.TextRoot)
	if err != nil || got != fixture.Text {
		t.Fatalf("cross-format text = %q, err = %v", got, err)
	}
}

func TestDocumentRejectsUnsafeInputsAndUseAfterClose(t *testing.T) {
	doc := New()
	if err := doc.ApplyUpdate(FormatV1, make([]byte, MaxUpdateBytes+1)); !errors.Is(err, ErrUpdateTooLarge) {
		t.Fatalf("oversized update error = %v", err)
	}
	if err := doc.ApplyUpdate(0, []byte{0}); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("unsupported format error = %v", err)
	}
	if err := doc.ApplyUpdate(FormatV1, []byte{0xff}); !errors.Is(err, ErrInvalidUpdate) {
		t.Fatalf("malformed update error = %v", err)
	}
	if _, err := doc.Text(""); !errors.Is(err, ErrInvalidRootName) {
		t.Fatalf("root name error = %v", err)
	}
	doc.Close()
	doc.Close()
	if err := doc.ApplyUpdate(FormatV1, []byte{0, 0}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed apply error = %v", err)
	}
	if _, err := doc.EncodeStateAsUpdate(FormatV1); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed encode error = %v", err)
	}
}

func TestDocumentSerializesConcurrentOperations(t *testing.T) {
	fixture := loadYjsFixture(t)
	update := decodeHex(t, fixture.V1Hex)
	doc := New()
	defer doc.Close()

	const workers = 16
	errors := make(chan error, workers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for range workers {
		go func() {
			defer waitGroup.Done()
			if err := doc.ApplyUpdate(FormatV1, update); err != nil {
				errors <- err
				return
			}
			if _, err := doc.EncodeStateAsUpdate(FormatV2); err != nil {
				errors <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errors)
	for err := range errors {
		t.Fatalf("concurrent document operation: %v", err)
	}

	text, err := doc.Text(fixture.TextRoot)
	if err != nil {
		t.Fatalf("read text after concurrent operations: %v", err)
	}
	if text != fixture.Text {
		t.Fatalf("text = %q, want %q", text, fixture.Text)
	}
}

func TestDocumentRichTextRoundTrip(t *testing.T) {
	doc := New()
	defer doc.Close()

	if err := doc.Insert("body", 0, "hello world", nil); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := doc.Format("body", 0, 5, Attributes{AttrBold: true}); err != nil {
		t.Fatalf("format: %v", err)
	}
	if err := doc.Delete("body", 5, 6); err != nil {
		t.Fatalf("delete: %v", err)
	}

	text, err := doc.Text("body")
	if err != nil {
		t.Fatalf("text: %v", err)
	}
	if text != "hello" {
		t.Fatalf("text = %q, want %q", text, "hello")
	}

	markdown, err := doc.Markdown("body")
	if err != nil {
		t.Fatalf("markdown: %v", err)
	}
	if markdown != "**hello**" {
		t.Fatalf("markdown = %q, want %q", markdown, "**hello**")
	}
}

func TestDocumentMutationsRejectInvalidInput(t *testing.T) {
	doc := New()
	defer doc.Close()

	if err := doc.Insert("", 0, "x", nil); !errors.Is(err, ErrInvalidRootName) {
		t.Fatalf("empty root error = %v", err)
	}
	if err := doc.Insert("body", 0, "\xff", nil); !errors.Is(err, ErrInvalidText) {
		t.Fatalf("invalid utf8 error = %v", err)
	}
	if err := doc.Insert("body", 0, "x", Attributes{"k": struct{}{}}); !errors.Is(err, ErrInvalidAttributes) {
		t.Fatalf("invalid attribute error = %v", err)
	}
	if err := doc.Insert("body", 0, "x", Attributes{AttrHeader: 99}); !errors.Is(err, ErrInvalidAttributes) {
		t.Fatalf("out of range header error = %v", err)
	}
	if err := doc.Insert("body", 0, "x", Attributes{AttrBold: "yes"}); !errors.Is(err, ErrInvalidAttributes) {
		t.Fatalf("wrong-typed bold error = %v", err)
	}
	if err := doc.Insert("body", 0, "x", Attributes{AttrList: "square"}); !errors.Is(err, ErrInvalidAttributes) {
		t.Fatalf("unknown list value error = %v", err)
	}
	if err := doc.Insert("body", 0, "x", Attributes{AttrLink: ""}); !errors.Is(err, ErrInvalidAttributes) {
		t.Fatalf("empty link error = %v", err)
	}
	if err := doc.Insert("body", 5, "x", nil); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("out of range insert error = %v", err)
	}
	if err := doc.Insert("body", -1, "x", nil); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("negative index insert error = %v", err)
	}
	if err := doc.Insert("body", 0, "hello", nil); err != nil {
		t.Fatalf("insert setup: %v", err)
	}
	if err := doc.Delete("body", 0, 100); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("out of range delete error = %v", err)
	}
	if err := doc.Delete("body", -1, 1); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("negative index delete error = %v", err)
	}
	if err := doc.Format("body", 0, 100, Attributes{AttrBold: true}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("out of range format error = %v", err)
	}
	if err := doc.Format("body", 0, 0, Attributes{AttrBold: true}); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("zero length format error = %v", err)
	}

	doc.Close()
	if err := doc.Insert("body", 0, "x", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("insert after close error = %v", err)
	}
	if err := doc.Delete("body", 0, 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("delete after close error = %v", err)
	}
	if err := doc.Format("body", 0, 1, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("format after close error = %v", err)
	}
	if _, err := doc.Markdown("body"); !errors.Is(err, ErrClosed) {
		t.Fatalf("markdown after close error = %v", err)
	}
	if _, err := doc.EncodeStateVector(); !errors.Is(err, ErrClosed) {
		t.Fatalf("state vector after close error = %v", err)
	}
}

func TestEncodeStateVectorReflectsAppliedUpdate(t *testing.T) {
	fixture := loadYjsFixture(t)
	update := decodeHex(t, fixture.V1Hex)

	empty := New()
	defer empty.Close()
	emptySV, err := empty.EncodeStateVector()
	if err != nil {
		t.Fatalf("empty state vector: %v", err)
	}

	applied := New()
	defer applied.Close()
	if err := applied.ApplyUpdate(FormatV1, update); err != nil {
		t.Fatalf("apply update: %v", err)
	}
	appliedSV, err := applied.EncodeStateVector()
	if err != nil {
		t.Fatalf("applied state vector: %v", err)
	}
	if bytes.Equal(emptySV, appliedSV) {
		t.Fatal("state vector did not change after applying an update")
	}
}

func TestRestoreRoundTripsSnapshotAndRejectsMalformedInput(t *testing.T) {
	original := New()
	defer original.Close()
	if err := original.Insert("body", 0, "hello", Attributes{AttrBold: true}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	snapshot, err := original.EncodeStateAsUpdate(FormatV2)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored, err := Restore(FormatV2, snapshot)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	defer restored.Close()

	text, err := restored.Text("body")
	if err != nil || text != "hello" {
		t.Fatalf("restored text = %q, err = %v", text, err)
	}
	markdown, err := restored.Markdown("body")
	if err != nil || markdown != "**hello**" {
		t.Fatalf("restored markdown = %q, err = %v", markdown, err)
	}

	if _, err := Restore(FormatV2, []byte{0xff, 0xff, 0xff}); !errors.Is(err, ErrInvalidUpdate) {
		t.Fatalf("malformed snapshot error = %v", err)
	}
}

func loadYjsFixture(t *testing.T) yjsFixture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "schema", "testdata", "v1", "yjs-updates.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture yjsFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return fixture
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return decoded
}
