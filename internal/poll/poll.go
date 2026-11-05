// Package poll implements LeafMark's core sync cycle: poll KOReader, diff
// against last-known state, resolve or fuzzy-match the Goodreads identity,
// and push progress.
package poll

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"

	"leafmark/internal/db"
	"leafmark/internal/goodreads"
	"leafmark/internal/koreader"
	"leafmark/internal/match"
	"leafmark/internal/ntfy"
)

// Deps are RunOnce's dependencies, all interfaces so the whole cycle is
// testable against mocks without touching any live service.
type Deps struct {
	DB             db.Queryer
	KOReader       koreader.KOReader
	Goodreads      goodreads.Goodreads
	Notifier       ntfy.Publisher
	MatchThreshold float64
	BaseURL        string // for building ntfy action button URLs

	// Relogin re-authenticates the Goodreads client (a closure over
	// goodreads.Client.Login + credentials, built in main.go) so this
	// package never needs to see GOODREADS_PASSWORD directly.
	Relogin func(ctx context.Context) error
}

// Poller runs repeated sync cycles, holding just enough state across calls
// to throttle the "Goodreads session expired" ntfy alert to once per
// expiry event rather than once per poll interval.
type Poller struct {
	Deps

	mu               sync.Mutex
	sessionAlertSent bool
}

func NewPoller(deps Deps) *Poller {
	return &Poller{Deps: deps}
}

// RunOnce executes one full poll cycle: poll -> diff -> resolve/match ->
// push. Returns an error only for problems with the cycle itself (e.g.
// KOReader unreachable); a failed Goodreads push is recorded into
// sync_state and does not fail the cycle.
func (p *Poller) RunOnce(ctx context.Context) error {
	doc, ok, err := p.KOReader.LastSyncedDocument(ctx)
	if err != nil {
		return fmt.Errorf("poll: fetch last synced document: %w", err)
	}
	if !ok {
		return nil // nothing synced yet
	}

	if err := db.UpsertDocument(ctx, p.DB, doc.DocHash, doc.Title, doc.Author); err != nil {
		return fmt.Errorf("poll: upsert document: %w", err)
	}

	ignored, err := db.IsIgnored(ctx, p.DB, doc.DocHash)
	if err != nil {
		return fmt.Errorf("poll: check ignored: %w", err)
	}
	if ignored {
		return nil // marked "not a book" in the WebUI
	}

	if !p.progressChanged(ctx, doc) {
		return nil
	}

	mapping, err := db.GetMapping(ctx, p.DB, doc.DocHash)
	if err == nil {
		p.pushAndRecord(ctx, doc.DocHash, mapping.GoodreadsBookID, doc.Percent)
		return nil
	}
	if !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("poll: get mapping: %w", err)
	}

	return p.resolveAndProceed(ctx, doc)
}

// progressChanged reports whether doc's progress is worth acting on: no
// prior sync_state row, a changed percentage, or a previous attempt that
// errored (worth retrying).
func (p *Poller) progressChanged(ctx context.Context, doc koreader.Document) bool {
	state, err := db.GetSyncState(ctx, p.DB, doc.DocHash)
	if errors.Is(err, db.ErrNotFound) {
		return true
	}
	if err != nil {
		// Treat an unexpected lookup failure as "needs action" rather than
		// silently skipping a real sync.
		return true
	}
	if state.LastSyncStatus == db.SyncStatusError {
		return true
	}
	return int(state.LastPercent) != doc.Percent
}

// resolveAndProceed runs the fuzzy-match step for a document with no
// confirmed mapping yet: shelf-scoped fuzzy match, auto-confirm above
// threshold, or create a pending match + ntfy notification below it.
func (p *Poller) resolveAndProceed(ctx context.Context, doc koreader.Document) error {
	shelf, err := p.fetchCombinedShelf(ctx)
	if err != nil {
		return fmt.Errorf("poll: fetch shelves: %w", err)
	}

	best, ok, topN := match.FindBestMatches(doc.Title, doc.Author, shelf, p.MatchThreshold)
	logMatchCandidates(doc, shelf, topN, best, ok, p.MatchThreshold)
	candidates := toDBCandidates(topN)

	if ok {
		confidence := best.Score
		if err := db.UpsertMapping(ctx, p.DB, db.UpsertMappingParams{
			DocHash: doc.DocHash, GoodreadsBookID: best.GoodreadsBookID,
			GoodreadsTitle: best.Title, GoodreadsAuthor: best.Author,
			MatchConfidence: &confidence, MatchedVia: db.MatchedViaAuto,
		}); err != nil {
			return fmt.Errorf("poll: upsert auto-matched mapping: %w", err)
		}
		p.pushAndRecord(ctx, doc.DocHash, best.GoodreadsBookID, doc.Percent)
		return nil
	}

	pm, err := db.CreatePendingMatch(ctx, p.DB, doc.DocHash, float64(doc.Percent), candidates)
	if errors.Is(err, db.ErrAlreadyPending) {
		log.Printf("poll: doc %s already has an open pending match, skipping (no new notification)", doc.DocHash)
		return nil
	}
	if err != nil {
		return fmt.Errorf("poll: create pending match: %w", err)
	}

	if err := p.notifyPendingMatch(ctx, doc, pm); err != nil {
		return fmt.Errorf("poll: publish ntfy notification: %w", err)
	}
	return nil
}

func (p *Poller) fetchCombinedShelf(ctx context.Context) ([]goodreads.ShelfBook, error) {
	var all []goodreads.ShelfBook
	for _, shelf := range []goodreads.ShelfName{goodreads.ShelfWantToRead, goodreads.ShelfCurrentlyReading} {
		books, err := p.Goodreads.FetchShelf(ctx, shelf)
		if errors.Is(err, goodreads.ErrSessionInvalid) && p.Relogin != nil {
			// The shelf pages need a logged-in session; log in lazily
			// (startup login may have failed) and retry once.
			log.Print("poll: shelf fetch came back session-invalid, triggering automated Goodreads re-login")
			if reloginErr := p.Relogin(ctx); reloginErr != nil {
				return nil, fmt.Errorf("session invalid, relogin also failed: %w", reloginErr)
			}
			books, err = p.Goodreads.FetchShelf(ctx, shelf)
		}
		if err != nil {
			return nil, err
		}
		log.Printf("poll: fetched %d book(s) from shelf %q", len(books), shelf)
		all = append(all, books...)
	}
	log.Printf("poll: %d book(s) total across want-to-read + currently-reading", len(all))
	return all, nil
}

// logMatchCandidates logs the full scored candidate pool for a match
// attempt — the target title/author as KOReader reported them and as
// Normalize sees them (a mismatch there is a common cause of an
// unexpectedly-low score), every candidate FindBestMatches considered up
// to maxCandidates, and the final auto-match/no-match decision. This is
// the only place that decision is visible; without it, a below-threshold
// score looks identical in the logs to "the book isn't on the shelf at
// all" (see FetchShelf's own per-shelf/total counts above).
func logMatchCandidates(doc koreader.Document, shelf []goodreads.ShelfBook, topN []match.Candidate, best *match.Candidate, ok bool, threshold float64) {
	log.Printf("poll: matching doc_hash=%s title=%q author=%q (normalized: title=%q author=%q) against %d shelf book(s), threshold=%.3f",
		doc.DocHash, doc.Title, doc.Author, match.Normalize(doc.Title), match.Normalize(doc.Author), len(shelf), threshold)

	if len(topN) == 0 {
		log.Printf("poll: no candidates at all for doc_hash=%s (empty shelf?) — creating/keeping a pending match", doc.DocHash)
		return
	}

	for i, c := range topN {
		log.Printf("poll:   candidate #%d: score=%.3f goodreads_book_id=%s title=%q author=%q",
			i+1, c.Score, c.GoodreadsBookID, c.Title, c.Author)
	}

	if ok {
		log.Printf("poll: auto-matched doc_hash=%s to goodreads_book_id=%s (score=%.3f >= threshold=%.3f)",
			doc.DocHash, best.GoodreadsBookID, best.Score, threshold)
	} else {
		log.Printf("poll: best candidate for doc_hash=%s scored %.3f, below threshold=%.3f — creating/keeping a pending match",
			doc.DocHash, topN[0].Score, threshold)
	}
}

func toDBCandidates(cs []match.Candidate) []db.Candidate {
	out := make([]db.Candidate, len(cs))
	for i, c := range cs {
		out[i] = db.Candidate{GoodreadsBookID: c.GoodreadsBookID, Title: c.Title, Author: c.Author, Score: c.Score}
	}
	return out
}

// pushAndRecord pushes progress to Goodreads, retrying once via Relogin on
// a session-invalid failure, and records the outcome in sync_state. Errors
// are swallowed (recorded, not returned) so one document's push failure
// never aborts the poll cycle; a persistent session failure surfaces via a
// throttled ntfy alert instead (see sessionAlertSent).
func (p *Poller) pushAndRecord(ctx context.Context, docHash, bookID string, percent int) {
	err := p.Goodreads.UpdateProgress(ctx, bookID, percent)
	// Captured before relogin/retry: a failed relogin wraps its own error,
	// which would no longer satisfy errors.Is(err, ErrSessionInvalid) —
	// that must not suppress the throttled alert below.
	sessionWasInvalid := errors.Is(err, goodreads.ErrSessionInvalid)

	if sessionWasInvalid {
		log.Printf("poll: push for doc_hash=%s came back session-invalid", docHash)
		if p.Relogin != nil {
			log.Print("poll: triggering automated Goodreads re-login")
			if reloginErr := p.Relogin(ctx); reloginErr == nil {
				log.Print("poll: re-login succeeded, retrying the push once")
				err = p.Goodreads.UpdateProgress(ctx, bookID, percent)
			} else {
				log.Printf("poll: re-login failed: %v", reloginErr)
				err = fmt.Errorf("session invalid, relogin also failed: %w", reloginErr)
			}
		} else {
			log.Print("poll: no Relogin configured (e.g. --dry-run) — not attempting one")
		}
	}

	status := db.SyncStatusOK
	errText := ""
	if err != nil {
		status = db.SyncStatusError
		errText = err.Error()
		log.Printf("poll: push for doc_hash=%s failed, recorded as sync error: %v", docHash, err)
		if sessionWasInvalid {
			p.alertSessionFailureOnce(ctx, err)
		}
	} else {
		log.Printf("poll: push for doc_hash=%s succeeded (%d%%)", docHash, percent)
		p.clearSessionAlert()
	}

	_ = db.UpsertSyncState(ctx, p.DB, docHash, float64(percent), status, errText)
}

func (p *Poller) alertSessionFailureOnce(ctx context.Context, cause error) {
	p.mu.Lock()
	alreadySent := p.sessionAlertSent
	p.sessionAlertSent = true
	p.mu.Unlock()
	if alreadySent {
		log.Print("poll: session-expired ntfy alert already sent for this failure streak, suppressing duplicate")
		return
	}
	log.Print("poll: publishing a session-expired ntfy alert (throttled to once per failure streak)")
	_ = p.Notifier.Publish(ctx, ntfy.Notification{
		Title:   "LeafMark: Goodreads session expired",
		Message: "Automatic re-login failed: " + cause.Error(),
	})
}

func (p *Poller) clearSessionAlert() {
	p.mu.Lock()
	wasSet := p.sessionAlertSent
	p.sessionAlertSent = false
	p.mu.Unlock()
	if wasSet {
		log.Print("poll: Goodreads session recovered, resetting the session-expired alert throttle")
	}
}

// notifyPendingMatch publishes the ntfy notification for a fresh pending
// match.
func (p *Poller) notifyPendingMatch(ctx context.Context, doc koreader.Document, pm *db.PendingMatch) error {
	n, err := BuildPendingNotification(p.BaseURL, doc.Title, doc.Author, doc.Percent, pm)
	if err != nil {
		return err
	}
	return p.Notifier.Publish(ctx, n)
}

// BuildPendingNotification builds the ntfy notification for a pending
// match: up to 2 http actions (one per candidate, posting to /confirm with
// clear:true) plus one view action linking to the WebUI page. Exported so
// the WebUI can re-send it on demand.
func BuildPendingNotification(baseURL, title, author string, percent int, pm *db.PendingMatch) (ntfy.Notification, error) {
	// ntfy allows at most 3 actions per notification (a 4th gets an HTTP
	// 400), and one slot is reserved for "Choose manually" below. The web UI
	// still shows every candidate.
	const maxCandidateActions = 2
	candidates := pm.Candidates
	if len(candidates) > maxCandidateActions {
		candidates = candidates[:maxCandidateActions]
	}
	actions := make([]ntfy.Action, 0, len(candidates)+1)
	for _, c := range candidates {
		body, err := json.Marshal(map[string]any{
			"pending_match_id":  pm.ID,
			"goodreads_book_id": c.GoodreadsBookID,
			"goodreads_title":   c.Title,
			"goodreads_author":  c.Author,
			"source":            "ntfy_action",
		})
		if err != nil {
			return ntfy.Notification{}, err
		}
		actions = append(actions, ntfy.Action{
			Action:  "http",
			Label:   truncateLabel(c.Title),
			URL:     baseURL + "/confirm",
			Method:  "POST",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    string(body),
			Clear:   true,
		})
	}
	actions = append(actions, ntfy.Action{
		Action: "view",
		Label:  "Choose manually",
		URL:    fmt.Sprintf("%s/pending/%d", baseURL, pm.ID),
	})

	message := fmt.Sprintf("Currently at %d%%", percent)
	if author != "" {
		message = fmt.Sprintf("%s — %s", author, message)
	}

	return ntfy.Notification{
		Title:   "LeafMark: match needed — " + title,
		Message: message,
		Actions: actions,
	}, nil
}

func truncateLabel(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// ResendOpenPending re-publishes the ntfy notification for every open
// pending match. Called once at startup so a pending match whose original
// notification was lost (failed publish, crash, restart) isn't silently
// stranded — the poller never re-notifies an already-open match itself.
// Failures are logged and counted; the rest are still sent.
func (p *Poller) ResendOpenPending(ctx context.Context) (sent, failed int) {
	matches, err := db.ListOpenPendingMatches(ctx, p.DB)
	if err != nil {
		log.Printf("poll: list open pending matches for resend: %v", err)
		return 0, 0
	}
	for i := range matches {
		pm := &matches[i]
		title, author := pm.DocHash, ""
		if doc, err := db.GetDocument(ctx, p.DB, pm.DocHash); err == nil {
			title, author = doc.KoreaderTitle, doc.KoreaderAuthor.String
		}
		n, err := BuildPendingNotification(p.BaseURL, title, author, int(pm.ProgressPct), pm)
		if err == nil {
			err = p.Notifier.Publish(ctx, n)
		}
		if err != nil {
			log.Printf("poll: resend notification for pending %d: %v", pm.ID, err)
			failed++
			continue
		}
		sent++
	}
	return sent, failed
}
