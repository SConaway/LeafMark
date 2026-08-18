package db

import (
	"errors"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ErrNotFound is returned by single-row lookups when no row matches.
var ErrNotFound = errors.New("db: not found")

// ErrAlreadyPending is returned by CreatePendingMatch when doc_hash already
// has an open pending match (the partial unique index rejected the insert).
// Callers should treat this as "there's already a match waiting on this
// doc," not as a failure worth crashing or re-notifying over.
var ErrAlreadyPending = errors.New("db: doc_hash already has an open pending match")

// isUniqueConstraint reports whether err is a SQLite UNIQUE constraint
// violation (primary or extended result code).
func isUniqueConstraint(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code&0xff == sqlite3.SQLITE_CONSTRAINT
}
