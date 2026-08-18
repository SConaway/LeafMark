package db

import (
	"context"
	"database/sql"
)

// Sync status values for sync_state.last_sync_status.
const (
	SyncStatusOK    = "ok"
	SyncStatusError = "error"
)

// SyncState mirrors the sync_state table.
type SyncState struct {
	DocHash        string
	LastPercent    float64
	LastSyncedAt   string
	LastSyncStatus string
	LastError      sql.NullString
}

// GetSyncState looks up the last known sync outcome for a doc_hash. Returns
// ErrNotFound if we've never recorded a sync attempt for it.
func GetSyncState(ctx context.Context, q Queryer, docHash string) (*SyncState, error) {
	var s SyncState
	err := q.QueryRowContext(ctx, `
		SELECT doc_hash, last_percent, last_synced_at, last_sync_status, last_error
		FROM sync_state WHERE doc_hash = ?
	`, docHash).Scan(&s.DocHash, &s.LastPercent, &s.LastSyncedAt, &s.LastSyncStatus, &s.LastError)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// UpsertSyncState records the outcome of a push attempt for a doc_hash.
func UpsertSyncState(ctx context.Context, q Queryer, docHash string, percent float64, status string, syncErr string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO sync_state (doc_hash, last_percent, last_sync_status, last_error)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (doc_hash) DO UPDATE SET
			last_percent     = excluded.last_percent,
			last_synced_at   = datetime('now'),
			last_sync_status = excluded.last_sync_status,
			last_error       = excluded.last_error
	`, docHash, percent, status, nullableString(syncErr))
	return err
}
