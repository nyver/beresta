package mobileapi

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"testing"
)

const officialHelloV1 = "0101d680b68a0d00040107636f6e74656e740d48656c6c6f2c20776f726c642100"

func TestDocumentFacade(t *testing.T) {
	update, err := hex.DecodeString(officialHelloV1)
	if err != nil {
		t.Fatal(err)
	}
	doc := NewDocument()
	defer doc.Close()
	if err := doc.ApplyUpdateV1(update); err != nil {
		t.Fatal(err)
	}
	got, err := doc.GetText("content")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello, world!" {
		t.Fatalf("text = %q", got)
	}
	if _, err := doc.EncodeStateAsUpdateV2(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLCipherProbeFacade(t *testing.T) {
	version, err := RunSQLCipherProbe(
		filepath.Join(t.TempDir(), "mobile.db"),
		bytes.Repeat([]byte{0x42}, 32),
		"mobile-sqlcipher-round-trip-marker",
	)
	if err != nil {
		t.Fatal(err)
	}
	if version == "" {
		t.Fatal("cipher version is empty")
	}
}
