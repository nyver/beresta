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
