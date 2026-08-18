package db

import (
	"context"
	"database/sql"
)

// Document mirrors the documents table.
type Document struct {
	DocHash        string
	KoreaderTitle  string
	KoreaderAuthor sql.NullString
	CreatedAt      string
	UpdatedAt      string
}

// UpsertDocument records the latest KOReader-reported title/author for a
// document, creating the row on first sight.
func UpsertDocument(ctx context.Context, q Queryer, docHash, title, author string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO documents (doc_hash, koreader_title, koreader_author)
		VALUES (?, ?, ?)
		ON CONFLICT (doc_hash) DO UPDATE SET
			koreader_title  = excluded.koreader_title,
			koreader_author = excluded.koreader_author,
			updated_at      = datetime('now')
	`, docHash, title, nullableString(author))
	return err
}

// GetDocument looks up a document by doc_hash. Returns ErrNotFound if absent.
func GetDocument(ctx context.Context, q Queryer, docHash string) (*Document, error) {
	var d Document
	err := q.QueryRowContext(ctx, `
		SELECT doc_hash, koreader_title, koreader_author, created_at, updated_at
		FROM documents WHERE doc_hash = ?
	`, docHash).Scan(&d.DocHash, &d.KoreaderTitle, &d.KoreaderAuthor, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
