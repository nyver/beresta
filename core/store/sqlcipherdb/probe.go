// Package sqlcipherdb provides the encrypted SQLite connection boundary.
package sqlcipherdb

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	sqlite3 "github.com/AnoRebel/go-sqlcipher"
)

const (
	driverName  = "sqlite3"
	pageSize    = 4096
	minProbeLen = 16
	maxProbeLen = 4096
)

var sqliteHeader = []byte("SQLite format 3\x00")

// ProbeResult describes a successful encrypted database round trip.
type ProbeResult struct {
	CipherVersion string
	Value         string
}

// Probe creates or reopens an encrypted database, writes value transactionally,
// and verifies that the value can be read after reopening with the same key.
// It is intentionally small and will be replaced by the repository layer.
func Probe(path string, key []byte, value string) (ProbeResult, error) {
	if path == "" {
		return ProbeResult{}, errors.New("SQLCipher database path is required")
	}
	if strings.ContainsAny(path, "?#") {
		return ProbeResult{}, errors.New("SQLCipher database path must not contain '?' or '#'")
	}
	if len(key) != 32 {
		return ProbeResult{}, fmt.Errorf("SQLCipher key must be 32 bytes, got %d", len(key))
	}
	if len(value) < minProbeLen || len(value) > maxProbeLen {
		return ProbeResult{}, fmt.Errorf("probe value length must be between %d and %d bytes", minProbeLen, maxProbeLen)
	}

	dsn := dataSourceName(path, key)
	version, err := writeProbe(dsn, value)
	if err != nil {
		return ProbeResult{}, err
	}

	readValue, err := readProbe(dsn)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("reopen encrypted database: %w", err)
	}
	if readValue != value {
		return ProbeResult{}, errors.New("encrypted database round trip returned a different value")
	}

	encrypted, err := sqlite3.IsEncrypted(path)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("inspect encrypted database: %w", err)
	}
	if !encrypted {
		return ProbeResult{}, errors.New("database has a plaintext SQLite header")
	}
	for _, encryptedPath := range []string{path, path + "-wal"} {
		visible, err := containsPlaintext(encryptedPath, []byte(value))
		if err != nil {
			return ProbeResult{}, fmt.Errorf("inspect encrypted database file: %w", err)
		}
		if visible {
			return ProbeResult{}, fmt.Errorf("probe value is visible in %s", encryptedPath)
		}
	}

	return ProbeResult{CipherVersion: version, Value: readValue}, nil
}

func dataSourceName(path string, key []byte) string {
	rawKey := "x'" + hex.EncodeToString(key) + "'"
	return path + "?_pragma_key=" + url.QueryEscape(rawKey) +
		fmt.Sprintf("&_pragma_cipher_page_size=%d", pageSize) +
		"&_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL&_synchronous=NORMAL"
}

func writeProbe(dsn, value string) (string, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return "", fmt.Errorf("open encrypted database: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	var version string
	if err := db.QueryRow("PRAGMA cipher_version").Scan(&version); err != nil {
		return "", fmt.Errorf("query SQLCipher version: %w", err)
	}
	if version == "" {
		return "", errors.New("SQLCipher version is empty")
	}

	tx, err := db.Begin()
	if err != nil {
		return "", fmt.Errorf("begin probe transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS beresta_sqlcipher_probe (
        id INTEGER PRIMARY KEY CHECK (id = 1),
        value TEXT NOT NULL
    )`); err != nil {
		return "", fmt.Errorf("create probe table: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO beresta_sqlcipher_probe (id, value) VALUES (1, ?)
        ON CONFLICT(id) DO UPDATE SET value = excluded.value`, value); err != nil {
		return "", fmt.Errorf("write probe value: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit probe transaction: %w", err)
	}
	return version, nil
}

func readProbe(dsn string) (string, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return "", err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	var value string
	if err := db.QueryRow("SELECT value FROM beresta_sqlcipher_probe WHERE id = 1").Scan(&value); err != nil {
		return "", err
	}
	rows, err := db.Query("PRAGMA cipher_integrity_check")
	if err != nil {
		return "", fmt.Errorf("run cipher integrity check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var issue string
		if err := rows.Scan(&issue); err != nil {
			return "", fmt.Errorf("read cipher integrity result: %w", err)
		}
		if issue != "" {
			return "", fmt.Errorf("cipher integrity check failed: %s", issue)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("complete cipher integrity check: %w", err)
	}
	return value, nil
}

func containsPlaintext(path string, marker []byte) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()

	buffer := make([]byte, 64*1024+len(marker)-1)
	retained := 0
	for {
		previouslyRetained := retained
		read, readErr := file.Read(buffer[retained:])
		if bytes.Contains(buffer[:retained+read], marker) {
			return true, nil
		}
		if readErr == io.EOF {
			return false, nil
		}
		if readErr != nil {
			return false, readErr
		}
		available := previouslyRetained + read
		retained = min(len(marker)-1, available)
		copy(buffer[:retained], buffer[available-retained:available])
	}
}
