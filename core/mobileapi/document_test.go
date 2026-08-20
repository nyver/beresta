package mobileapi

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	dir, err := os.MkdirTemp("", "beresta-mobileapi-sqlcipher-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// SQLCipher closes before RunSQLCipherProbe returns, but Windows
		// endpoint protection can briefly retain a newly created WAL or test
		// executable. Unlike t.TempDir's single RemoveAll attempt, retry the
		// disposable directory cleanup so that scanner timing cannot turn a
		// successful encrypted round trip into a flaky test failure.
		deadline := time.Now().Add(3 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil {
				if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Errorf("remove SQLCipher probe directory %s: %v", dir, err)
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	})

	version, err := RunSQLCipherProbe(
		filepath.Join(dir, "mobile.db"),
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
