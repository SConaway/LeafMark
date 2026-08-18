package db

import (
	"context"
	"database/sql"
)

// Queryer is satisfied by both *sql.DB and *sql.Tx, so CRUD helpers work
// unmodified inside a transaction or directly against the pool.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
