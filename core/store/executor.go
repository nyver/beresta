package store

import (
	"context"
	"database/sql"
)

// Executor is satisfied by both *sql.DB and *sql.Tx. Repository functions
// accept it so a caller can run one operation standalone or compose several
// into one atomic transaction (see core/store's package documentation).
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	_ Executor = (*sql.DB)(nil)
	_ Executor = (*sql.Tx)(nil)
)
