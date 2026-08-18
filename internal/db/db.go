// Package db owns the SQLite connection and embedded schema migration.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Open opens the SQLite database at path, enabling foreign key enforcement
// on every connection the pool opens (modernc.org/sqlite applies DSN query
// params per-connection, in newConn -> applyQueryParams, so this is not the
// one-time-PRAGMA trap that a bare "PRAGMA foreign_keys = ON" statement
// after Open would be).
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_foreign_keys=on", url.PathEscape(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite has a single writer; a busy pool just serializes on lock
	// contention instead of erroring, so keep this at one connection.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return db, nil
}

// Migrate applies the embedded schema. Every statement in schema.sql is
// idempotent (CREATE TABLE/INDEX IF NOT EXISTS), so this is safe to call on
// every startup.
func Migrate(db *sql.DB) error {
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("read embedded schema: %w", err)
	}

	if _, err := db.Exec(string(schema)); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}

	return nil
}
