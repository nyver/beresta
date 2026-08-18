package sqlcipherdb

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeEncryptedRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beresta.db")
	key := bytes.Repeat([]byte{0x5a}, 32)
	marker := "beresta-sqlcipher-round-trip-marker"

	result, err := Probe(path, key, marker)
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != marker {
		t.Fatalf("value = %q, want %q", result.Value, marker)
	}
	if result.CipherVersion == "" {
		t.Fatal("cipher version is empty")
	}
	t.Logf("SQLCipher version: %s", result.CipherVersion)

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(contents, sqliteHeader) {
		t.Fatal("encrypted database starts with a plaintext SQLite header")
	}
	if bytes.Contains(contents, []byte(marker)) {
		t.Fatal("plaintext marker is visible in the encrypted database")
	}

	wrongKey := bytes.Repeat([]byte{0xa5}, 32)
	db, err := sql.Open(driverName, dataSourceName(path, wrongKey))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM beresta_sqlcipher_probe WHERE id = 1").Scan(&value); err == nil {
		t.Fatal("database was readable with the wrong key")
	}
}

func TestProbeRejectsInvalidInput(t *testing.T) {
	validKey := bytes.Repeat([]byte{0x01}, 32)
	tests := []struct {
		name  string
		path  string
		key   []byte
		value string
	}{
		{name: "empty path", key: validKey, value: "value"},
		{name: "query delimiter", path: "bad?path", key: validKey, value: "value"},
		{name: "short key", path: "db", key: []byte("short"), value: "value"},
		{name: "short value", path: "db", key: validKey, value: "too-short"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Probe(tt.path, tt.key, tt.value); err == nil {
				t.Fatal("Probe returned no error")
			}
		})
	}
}

func TestContainsPlaintextAcrossFirstReadBoundary(t *testing.T) {
	marker := []byte("boundary-plaintext-marker")
	prefixLength := 64*1024 - len(marker)/2
	contents := append(bytes.Repeat([]byte{0xa5}, prefixLength), marker...)
	path := filepath.Join(t.TempDir(), "encrypted.db")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	visible, err := containsPlaintext(path, marker)
	if err != nil {
		t.Fatal(err)
	}
	if !visible {
		t.Fatal("plaintext marker spanning the first read boundary was not detected")
	}
}
