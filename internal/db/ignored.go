package db

import "context"

// IgnoredDocument is a document the user marked "not a real book".
type IgnoredDocument struct {
	DocHash string
	Title   string
	Author  string
}

// IgnoreDocument marks a document as not-a-book. Idempotent. The document
// row must already exist (it always does by the time a pending match does).
func IgnoreDocument(ctx context.Context, q Queryer, docHash string) error {
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO ignored_documents (doc_hash) VALUES (?)`, docHash)
	return err
}

// UnignoreDocument reverses IgnoreDocument. Idempotent.
func UnignoreDocument(ctx context.Context, q Queryer, docHash string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM ignored_documents WHERE doc_hash = ?`, docHash)
	return err
}

// IsIgnored reports whether docHash has been marked not-a-book.
func IsIgnored(ctx context.Context, q Queryer, docHash string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM ignored_documents WHERE doc_hash = ?`, docHash).Scan(&n)
	return n > 0, err
}

// ListIgnoredDocuments lists ignored documents, most recently ignored first.
func ListIgnoredDocuments(ctx context.Context, q Queryer) ([]IgnoredDocument, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT d.doc_hash, d.koreader_title, COALESCE(d.koreader_author, '')
		FROM ignored_documents i JOIN documents d ON d.doc_hash = i.doc_hash
		ORDER BY i.created_at DESC, d.doc_hash
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IgnoredDocument
	for rows.Next() {
		var d IgnoredDocument
		if err := rows.Scan(&d.DocHash, &d.Title, &d.Author); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DismissPendingMatch marks an open pending match dismissed (without
// mapping it). Returns false if it wasn't open.
func DismissPendingMatch(ctx context.Context, q Queryer, id int64) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE pending_matches
		SET status = ?, resolved_at = datetime('now')
		WHERE id = ? AND status = ?
	`, PendingStatusDismissed, id, PendingStatusOpen)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
