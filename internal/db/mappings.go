package db

import (
	"context"
	"database/sql"
)

// MatchedVia values for book_mappings.matched_via.
const (
	MatchedViaAuto       = "auto"
	MatchedViaNtfyAction = "ntfy_action"
	MatchedViaWebUI      = "webui"
)

// BookMapping mirrors the book_mappings table.
type BookMapping struct {
	DocHash         string
	GoodreadsBookID string
	GoodreadsTitle  string
	GoodreadsAuthor sql.NullString
	MatchConfidence sql.NullFloat64
	MatchedVia      string
	ConfirmedAt     string
}

// GetMapping looks up the confirmed Goodreads mapping for a doc_hash.
// Returns ErrNotFound if the document isn't mapped yet.
func GetMapping(ctx context.Context, q Queryer, docHash string) (*BookMapping, error) {
	var m BookMapping
	err := q.QueryRowContext(ctx, `
		SELECT doc_hash, goodreads_book_id, goodreads_title, goodreads_author,
		       match_confidence, matched_via, confirmed_at
		FROM book_mappings WHERE doc_hash = ?
	`, docHash).Scan(&m.DocHash, &m.GoodreadsBookID, &m.GoodreadsTitle, &m.GoodreadsAuthor,
		&m.MatchConfidence, &m.MatchedVia, &m.ConfirmedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// UpsertMapping creates or replaces the confirmed mapping for a doc_hash.
type UpsertMappingParams struct {
	DocHash         string
	GoodreadsBookID string
	GoodreadsTitle  string
	GoodreadsAuthor string
	MatchConfidence *float64 // nil for manual matches
	MatchedVia      string
}

func UpsertMapping(ctx context.Context, q Queryer, p UpsertMappingParams) error {
	var confidence sql.NullFloat64
	if p.MatchConfidence != nil {
		confidence = sql.NullFloat64{Float64: *p.MatchConfidence, Valid: true}
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO book_mappings (doc_hash, goodreads_book_id, goodreads_title, goodreads_author, match_confidence, matched_via)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (doc_hash) DO UPDATE SET
			goodreads_book_id = excluded.goodreads_book_id,
			goodreads_title   = excluded.goodreads_title,
			goodreads_author  = excluded.goodreads_author,
			match_confidence  = excluded.match_confidence,
			matched_via       = excluded.matched_via,
			confirmed_at      = datetime('now')
	`, p.DocHash, p.GoodreadsBookID, p.GoodreadsTitle, nullableString(p.GoodreadsAuthor), confidence, p.MatchedVia)
	return err
}
