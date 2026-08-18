package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	corecrypto "github.com/beresta-app/beresta/core/crypto"
	"github.com/beresta-app/beresta/core/store/sqlcipherdb"
)

// ErrIntegrityCheckFailed reports that SQLCipher's authenticated page
// integrity check found a problem in an opened database.
var ErrIntegrityCheckFailed = errors.New("store: database integrity check failed")

// Open establishes the production encrypted client database connection: it
// opens the SQLCipher connection with WAL/foreign-key/busy-timeout pragmas,
// verifies authenticated page integrity before and after schema migration,
// and applies every pending migration inside its own transaction. It
// returns the database's resulting schema version.
//
// databaseKey must be the unwrapped per-device key; see
// LoadOrCreateDatabaseKey. The caller owns the returned *sql.DB and must
// close it.
func Open(ctx context.Context, path string, databaseKey *corecrypto.Secret) (*sql.DB, int, error) {
	if databaseKey == nil {
		return nil, 0, corecrypto.ErrSecretClosed
	}

	var db *sql.DB
	err := databaseKey.Use(func(key []byte) error {
		opened, openErr := sqlcipherdb.Open(path, key)
		if openErr != nil {
			return openErr
		}
		db = opened
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	if err := checkIntegrity(ctx, db); err != nil {
		db.Close()
		return nil, 0, err
	}

	version, err := Migrate(ctx, db)
	if err != nil {
		db.Close()
		return nil, 0, err
	}

	if err := checkIntegrity(ctx, db); err != nil {
		db.Close()
		return nil, 0, err
	}

	return db, version, nil
}

// checkIntegrity runs SQLCipher's authenticated integrity check, which
// verifies every page's HMAC in addition to SQLite's structural checks. A
// wrong key would already have failed page decryption before this point;
// this catches structural corruption in an otherwise correctly keyed file.
func checkIntegrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA cipher_integrity_check`)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIntegrityCheckFailed, err)
	}
	defer rows.Close()

	for rows.Next() {
		var issue string
		if err := rows.Scan(&issue); err != nil {
			return fmt.Errorf("%w: %v", ErrIntegrityCheckFailed, err)
		}
		if issue != "" {
			return fmt.Errorf("%w: %s", ErrIntegrityCheckFailed, issue)
		}
	}
	return rows.Err()
}
