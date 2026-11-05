package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"leafmark/internal/db"
	"leafmark/internal/poll"
)

// resendPending re-publishes the ntfy notification for an open pending
// match. Returns db.ErrNotFound if the match is missing or no longer open.
func (s *Server) resendPending(ctx context.Context, pm *db.PendingMatch) error {
	title, author := pm.DocHash, ""
	if doc, err := db.GetDocument(ctx, s.db, pm.DocHash); err == nil {
		title, author = doc.KoreaderTitle, doc.KoreaderAuthor.String
	}
	n, err := poll.BuildPendingNotification(s.baseURL, title, author, int(pm.ProgressPct), pm)
	if err != nil {
		return err
	}
	return s.notifier.Publish(ctx, n)
}

// handleResend implements POST /pending/{id}/resend.
func (s *Server) handleResend(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid pending match id", http.StatusBadRequest)
		return
	}
	pm, err := db.GetPendingMatch(r.Context(), s.db, id)
	if errors.Is(err, db.ErrNotFound) || (err == nil && pm.Status != db.PendingStatusOpen) {
		http.Redirect(w, r, "/pending", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.resendPending(r.Context(), pm); err != nil {
		log.Printf("web: resend notification for pending %d: %v", id, err)
		http.Error(w, "failed to publish notification: "+err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/pending?resent=1", http.StatusSeeOther)
}

// handleResendAll implements POST /pending/resend-all: re-publishes every
// open pending match's notification. Failures are logged and counted; the
// rest still go out.
func (s *Server) handleResendAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	matches, err := db.ListOpenPendingMatches(ctx, s.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	failed := 0
	for i := range matches {
		if err := s.resendPending(ctx, &matches[i]); err != nil {
			log.Printf("web: resend notification for pending %d: %v", matches[i].ID, err)
			failed++
		}
	}
	http.Redirect(w, r, "/pending?resent="+strconv.Itoa(len(matches)-failed)+"&failed="+strconv.Itoa(failed), http.StatusSeeOther)
}

// handleIgnore implements POST /pending/{id}/ignore: dismisses the pending
// match and marks its document as not-a-book so the poller never raises
// another match or notification for it. Both writes share one transaction.
func (s *Server) handleIgnore(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid pending match id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	pm, err := db.GetPendingMatch(ctx, s.db, id)
	if errors.Is(err, db.ErrNotFound) {
		http.Redirect(w, r, "/pending", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	// Only an open match gets dismissed; the ignore itself applies either
	// way, so a stale tab still does what the user asked.
	if _, err := db.DismissPendingMatch(ctx, tx, id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := db.IgnoreDocument(ctx, tx, pm.DocHash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/pending", http.StatusSeeOther)
}

// handleRestore implements POST /ignored/{hash}/restore: un-ignores a
// document so the next poll treats it normally again.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if err := db.UnignoreDocument(r.Context(), s.db, r.PathValue("hash")); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/pending", http.StatusSeeOther)
}
