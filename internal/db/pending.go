package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Pending match status values for pending_matches.status.
const (
	PendingStatusOpen      = "open"
	PendingStatusResolved  = "resolved"
	PendingStatusDismissed = "dismissed"
)

// Candidate is one fuzzy near-miss recorded in pending_matches.candidates_json,
// ordered by score descending. Decoupled from the match package's internal
// scoring types so this package has no dependency on it.
type Candidate struct {
	GoodreadsBookID string  `json:"goodreads_book_id"`
	Title           string  `json:"title"`
	Author          string  `json:"author"`
	Score           float64 `json:"score"`
}

// PendingMatch mirrors the pending_matches table.
type PendingMatch struct {
	ID          int64
	DocHash     string
	ProgressPct float64
	Candidates  []Candidate
	Status      string
	CreatedAt   string
	ResolvedAt  sql.NullString
}

// CreatePendingMatch records a document awaiting human match confirmation.
// Returns ErrAlreadyPending (not an error worth crashing or re-notifying
// over) if doc_hash already has an open pending match — the partial unique
// index on pending_matches enforces that invariant at the DB layer.
func CreatePendingMatch(ctx context.Context, q Queryer, docHash string, progressPct float64, candidates []Candidate) (*PendingMatch, error) {
	candidatesJSON, err := json.Marshal(candidates)
	if err != nil {
		return nil, err
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO pending_matches (doc_hash, progress_pct, candidates_json)
		VALUES (?, ?, ?)
	`, docHash, progressPct, string(candidatesJSON))
	if isUniqueConstraint(err) {
		return nil, ErrAlreadyPending
	}
	if err != nil {
		return nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	return GetPendingMatch(ctx, q, id)
}

// GetPendingMatch looks up a pending match by id, regardless of status.
// Returns ErrNotFound if absent.
func GetPendingMatch(ctx context.Context, q Queryer, id int64) (*PendingMatch, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, doc_hash, progress_pct, candidates_json, status, created_at, resolved_at
		FROM pending_matches WHERE id = ?
	`, id)
	return scanPendingMatch(row)
}

// GetOpenPendingMatchByDocHash looks up the (at most one) open pending match
// for a doc_hash. Returns ErrNotFound if there isn't one.
func GetOpenPendingMatchByDocHash(ctx context.Context, q Queryer, docHash string) (*PendingMatch, error) {
	row := q.QueryRowContext(ctx, `
		SELECT id, doc_hash, progress_pct, candidates_json, status, created_at, resolved_at
		FROM pending_matches WHERE doc_hash = ? AND status = ?
	`, docHash, PendingStatusOpen)
	return scanPendingMatch(row)
}

// ListOpenPendingMatches lists all open pending matches, most recent first,
// for the WebUI's cold-open index page.
func ListOpenPendingMatches(ctx context.Context, q Queryer) ([]PendingMatch, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, doc_hash, progress_pct, candidates_json, status, created_at, resolved_at
		FROM pending_matches WHERE status = ? ORDER BY created_at DESC
	`, PendingStatusOpen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PendingMatch
	for rows.Next() {
		pm, err := scanPendingMatchRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pm)
	}
	return out, rows.Err()
}

// ResolvePendingMatch marks a pending match resolved. Scoped to status='open'
// so a double-tap race resolves the row exactly once; check RowsAffected to
// distinguish "resolved just now" from "was already resolved/dismissed."
func ResolvePendingMatch(ctx context.Context, q Queryer, id int64) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE pending_matches
		SET status = ?, resolved_at = datetime('now')
		WHERE id = ? AND status = ?
	`, PendingStatusResolved, id, PendingStatusOpen)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanPendingMatch(row *sql.Row) (*PendingMatch, error) {
	pm, err := scanPendingMatchRow(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return pm, err
}

func scanPendingMatchRow(s scannable) (*PendingMatch, error) {
	var pm PendingMatch
	var candidatesJSON sql.NullString
	if err := s.Scan(&pm.ID, &pm.DocHash, &pm.ProgressPct, &candidatesJSON, &pm.Status, &pm.CreatedAt, &pm.ResolvedAt); err != nil {
		return nil, err
	}
	if candidatesJSON.Valid && candidatesJSON.String != "" {
		if err := json.Unmarshal([]byte(candidatesJSON.String), &pm.Candidates); err != nil {
			return nil, err
		}
	}
	return &pm, nil
}
