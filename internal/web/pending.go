package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"leafmark/internal/db"
	"leafmark/internal/goodreads"
	"leafmark/internal/match"
)

type pendingListItem struct {
	ID             int64
	DocHash        string
	KoreaderTitle  string
	KoreaderAuthor string
	ProgressPct    float64
	CreatedAt      string
}

type pendingListView struct {
	Items []pendingListItem
}

// handlePendingList implements GET /pending — a cold-open index of every
// open pending match, for when you didn't arrive via an ntfy link.
func (s *Server) handlePendingList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	matches, err := db.ListOpenPendingMatches(ctx, s.db)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	view := pendingListView{Items: make([]pendingListItem, 0, len(matches))}
	for _, pm := range matches {
		doc, err := db.GetDocument(ctx, s.db, pm.DocHash)
		title, author := pm.DocHash, ""
		if err == nil {
			title, author = doc.KoreaderTitle, doc.KoreaderAuthor.String
		}
		view.Items = append(view.Items, pendingListItem{
			ID: pm.ID, DocHash: pm.DocHash, KoreaderTitle: title, KoreaderAuthor: author,
			ProgressPct: pm.ProgressPct, CreatedAt: pm.CreatedAt,
		})
	}

	s.render(w, "pending_list.html", view)
}

type candidateView struct {
	GoodreadsBookID string
	Title           string
	Author          string
	Score           float64
}

type pendingDetailView struct {
	ID             int64
	DocHash        string
	KoreaderTitle  string
	KoreaderAuthor string
	ProgressPct    float64
	Candidates     []candidateView
	Query          string
	SearchResults  []candidateView
	NotOpen        bool
	Status         string
}

// handlePendingDetail implements GET /pending/{id} — the KOReader
// title/author/progress for that pending match, its cached near-miss
// candidates as a shortcut list, a search box that re-queries the shelves
// (looser/unranked, since a human is looking directly at the results), and
// a plain text input to paste a Goodreads book ID directly for cases with
// no decent candidate at all. Pure server-rendered HTML forms — no JS.
func (s *Server) handlePendingDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid pending match id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	pm, err := db.GetPendingMatch(ctx, s.db, id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	view := pendingDetailView{
		ID: pm.ID, DocHash: pm.DocHash, ProgressPct: pm.ProgressPct,
		Status: pm.Status, NotOpen: pm.Status != db.PendingStatusOpen,
	}
	if doc, err := db.GetDocument(ctx, s.db, pm.DocHash); err == nil {
		view.KoreaderTitle, view.KoreaderAuthor = doc.KoreaderTitle, doc.KoreaderAuthor.String
	}
	for _, c := range pm.Candidates {
		view.Candidates = append(view.Candidates, candidateView{c.GoodreadsBookID, c.Title, c.Author, c.Score})
	}

	if q := r.URL.Query().Get("q"); q != "" && !view.NotOpen {
		view.Query = q
		view.SearchResults = s.searchShelves(ctx, q, view.KoreaderAuthor)
	}

	s.render(w, "pending_detail.html", view)
}

// searchShelves re-scores both shelves against a free-text query, unranked
// (no threshold cutoff) — the human looking at this page is the filter,
// not an auto-confirm threshold.
func (s *Server) searchShelves(ctx context.Context, query, author string) []candidateView {
	var all []goodreads.ShelfBook
	for _, shelf := range []goodreads.ShelfName{goodreads.ShelfWantToRead, goodreads.ShelfCurrentlyReading} {
		books, err := s.goodreads.FetchShelf(ctx, shelf)
		if err != nil {
			continue
		}
		all = append(all, books...)
	}

	scored := match.ScoreShelf(query, author, all)
	results := make([]candidateView, len(scored))
	for i, c := range scored {
		results[i] = candidateView{c.GoodreadsBookID, c.Title, c.Author, c.Score}
	}
	return results
}
