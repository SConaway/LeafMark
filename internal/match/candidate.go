package match

import (
	"sort"

	"leafmark/internal/goodreads"
)

// maxCandidates bounds how many near-misses get recorded in
// pending_matches.candidates_json (and offered as ntfy action buttons, per
// spec: "up to 3 http-type actions").
const maxCandidates = 3

// Candidate is one scored shelf book, ordered by Score descending.
type Candidate struct {
	GoodreadsBookID string
	Title           string
	Author          string
	Score           float64
}

// ScoreShelf scores every book on shelf against the target title/author,
// sorted by Score descending. Shared by FindBestMatches (capped to
// maxCandidates) and the WebUI's manual search (uncapped — a human
// directly reviewing results shouldn't be limited to 3).
func ScoreShelf(targetTitle, targetAuthor string, shelf []goodreads.ShelfBook) []Candidate {
	scored := make([]Candidate, len(shelf))
	for i, book := range shelf {
		scored[i] = Candidate{
			GoodreadsBookID: book.GoodreadsBookID,
			Title:           book.Title,
			Author:          book.Author,
			Score:           TitleAuthorScore(targetTitle, targetAuthor, book.Title, book.Author),
		}
	}
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})
	return scored
}

// FindBestMatches scores every book on shelf against the target
// title/author and returns:
//   - best, ok: the top candidate and whether its score clears threshold
//     (ok=false means "don't auto-confirm" — spec biases toward false
//     negatives over false positives, since a wrong auto-match silently
//     corrupts progress on the wrong Goodreads book).
//   - topN: the top (up to maxCandidates) candidates regardless of
//     threshold, for pending_matches.candidates_json / ntfy action buttons.
func FindBestMatches(targetTitle, targetAuthor string, shelf []goodreads.ShelfBook, threshold float64) (best *Candidate, ok bool, topN []Candidate) {
	if len(shelf) == 0 {
		return nil, false, nil
	}

	scored := ScoreShelf(targetTitle, targetAuthor, shelf)

	n := maxCandidates
	if n > len(scored) {
		n = len(scored)
	}
	topN = scored[:n]

	if topN[0].Score >= threshold {
		best = &topN[0]
		ok = true
	}
	return best, ok, topN
}
