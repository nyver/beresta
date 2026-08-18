package sqlcipherdb

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Open establishes the single-writer encrypted SQLite connection used by the
// client store: WAL journaling, a five-second busy timeout, and enforced
// foreign keys. It does not run migrations or an integrity check; those are
// the caller's responsibility (see core/store.Open for the production
// connection lifecycle).
func Open(path string, key []byte) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("SQLCipher database path is required")
	}
	if strings.ContainsAny(path, "?#") {
		return nil, errors.New("SQLCipher database path must not contain '?' or '#'")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("SQLCipher key must be 32 bytes, got %d", len(key))
	}

	db, err := sql.Open(driverName, dataSourceName(path, key))
	if err != nil {
		return nil, fmt.Errorf("open encrypted database: %w", err)
	}
	// SQLCipher keys and WAL-mode locking are per-connection state; a single
	// pooled connection keeps every statement on the same encrypted session
	// and matches the design's one-serialized-writer client model.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open encrypted database: %w", err)
	}
	return db, nil
}
