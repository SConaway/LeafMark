package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"leafmark/internal/db"
)

type confirmRequest struct {
	PendingMatchID  int64  `json:"pending_match_id"`
	GoodreadsBookID string `json:"goodreads_book_id"`
	GoodreadsTitle  string `json:"goodreads_title"`
	GoodreadsAuthor string `json:"goodreads_author"`
	Source          string `json:"source"`
}

// handleConfirm implements POST /confirm. Per spec, a not-found or
// already-resolved/dismissed pending match is a 200 no-op, not an error —
// this is deliberately the same response as a fresh success, so a
// double-tapped ntfy action button (or a re-submitted WebUI form) doesn't
// surface as a scary error to the user.
//
// Accepts either a JSON body (ntfy's http actions send JSON) or a plain
// HTML form post (the WebUI's forms, so the manual "paste a book ID" and
// candidate-picker forms need no JS at all) — detected by Content-Type.
// On a form submission the response is a redirect back to /pending rather
// than raw JSON, since that's what a browser navigation expects.
func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	req, isForm, err := parseConfirmRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.GoodreadsTitle == "" {
		req.GoodreadsTitle = "Goodreads book " + req.GoodreadsBookID
	}
	matchedVia := db.MatchedViaNtfyAction
	if req.Source == "webui" {
		matchedVia = db.MatchedViaWebUI
	}

	ctx := r.Context()
	resolved, docHash, progressPct, err := s.resolveAndMap(ctx, req, matchedVia)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if resolved {
		// Push immediately per spec, outside the resolve transaction so we
		// don't hold SQLite's writer lock across a slow external HTTP call.
		s.pushAndRecordOutcome(ctx, docHash, req.GoodreadsBookID, progressPct)
	}

	if isForm {
		http.Redirect(w, r, "/pending", http.StatusSeeOther)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func parseConfirmRequest(r *http.Request) (confirmRequest, bool, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var req confirmRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return confirmRequest{}, false, fmt.Errorf("invalid JSON body: %w", err)
		}
		return req, false, nil
	}

	if err := r.ParseForm(); err != nil {
		return confirmRequest{}, false, fmt.Errorf("invalid form body: %w", err)
	}
	id, err := strconv.ParseInt(r.FormValue("pending_match_id"), 10, 64)
	if err != nil {
		return confirmRequest{}, false, fmt.Errorf("invalid pending_match_id: %w", err)
	}
	return confirmRequest{
		PendingMatchID:  id,
		GoodreadsBookID: strings.TrimSpace(r.FormValue("goodreads_book_id")),
		GoodreadsTitle:  r.FormValue("goodreads_title"),
		GoodreadsAuthor: r.FormValue("goodreads_author"),
		Source:          r.FormValue("source"),
	}, true, nil
}

// resolveAndMap resolves the pending match and writes book_mappings inside
// one short transaction. Returns resolved=false (not an error) for the
// not-found/already-resolved/dismissed no-op cases.
func (s *Server) resolveAndMap(ctx context.Context, req confirmRequest, matchedVia string) (resolved bool, docHash string, progressPct float64, err error) {
	pm, err := db.GetPendingMatch(ctx, s.db, req.PendingMatchID)
	if errors.Is(err, db.ErrNotFound) {
		return false, "", 0, nil
	}
	if err != nil {
		return false, "", 0, err
	}
	if pm.Status != db.PendingStatusOpen {
		return false, "", 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, "", 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op if already committed

	ok, err := db.ResolvePendingMatch(ctx, tx, req.PendingMatchID)
	if err != nil {
		return false, "", 0, err
	}
	if !ok {
		// Lost a race with a concurrent confirm for the same pending match.
		return false, "", 0, nil
	}

	if err := db.UpsertMapping(ctx, tx, db.UpsertMappingParams{
		DocHash:         pm.DocHash,
		GoodreadsBookID: req.GoodreadsBookID,
		GoodreadsTitle:  req.GoodreadsTitle,
		GoodreadsAuthor: req.GoodreadsAuthor,
		MatchedVia:      matchedVia,
	}); err != nil {
		return false, "", 0, err
	}

	if err := tx.Commit(); err != nil {
		return false, "", 0, err
	}

	return true, pm.DocHash, pm.ProgressPct, nil
}

// pushAndRecordOutcome pushes progress to Goodreads and records the result
// in sync_state in its own short transaction. Errors (including a session
// gone invalid) are recorded, not returned — a failure here shouldn't
// surface as a 500 to whoever tapped "confirm"; the next successful
// poll-triggered re-login will recover it.
func (s *Server) pushAndRecordOutcome(ctx context.Context, docHash, bookID string, percent float64) {
	status := db.SyncStatusOK
	errText := ""
	if err := s.goodreads.UpdateProgress(ctx, bookID, int(percent)); err != nil {
		status = db.SyncStatusError
		errText = err.Error()
	}
	_ = db.UpsertSyncState(ctx, s.db, docHash, percent, status, errText)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
