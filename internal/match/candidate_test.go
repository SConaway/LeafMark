package match

import (
	"testing"

	"leafmark/internal/goodreads"
)

func testShelf() []goodreads.ShelfBook {
	return []goodreads.ShelfBook{
		{GoodreadsBookID: "1", Title: "Project Hail Mary", Author: "Andy Weir"},
		{GoodreadsBookID: "2", Title: "Project Hail Mary (Signed Edition)", Author: "Andy Weir"},
		{GoodreadsBookID: "3", Title: "The Fifth Season", Author: "N.K. Jemisin"},
		{GoodreadsBookID: "4", Title: "Artemis", Author: "Andy Weir"},
	}
}

func TestFindBestMatchesAutoConfirmsAboveThreshold(t *testing.T) {
	best, ok, topN := FindBestMatches("Project Hail Mary: A Novel", "Andy Weir", testShelf(), 0.8)
	if !ok {
		t.Fatalf("expected ok=true, got best=%+v", best)
	}
	if best.GoodreadsBookID != "1" && best.GoodreadsBookID != "2" {
		t.Errorf("expected one of the two Project Hail Mary entries to win, got %+v", best)
	}
	if len(topN) != 3 {
		t.Errorf("expected top 3 candidates, got %d", len(topN))
	}
	// Sorted descending.
	for i := 1; i < len(topN); i++ {
		if topN[i-1].Score < topN[i].Score {
			t.Errorf("topN not sorted descending: %+v", topN)
		}
	}
}

func TestFindBestMatchesNoMatchBelowThresholdStillReturnsCandidates(t *testing.T) {
	best, ok, topN := FindBestMatches("Some Completely Unrelated Title", "Some Rando", testShelf(), 0.8)
	if ok {
		t.Fatalf("expected ok=false for a title with no good match, got best=%+v", best)
	}
	if best != nil {
		t.Errorf("expected best=nil when ok=false, got %+v", best)
	}
	if len(topN) == 0 {
		t.Fatal("expected near-miss candidates to still be recorded even below threshold")
	}
}

func TestFindBestMatchesEmptyShelf(t *testing.T) {
	best, ok, topN := FindBestMatches("Anything", "Anyone", nil, 0.8)
	if ok || best != nil || topN != nil {
		t.Fatalf("expected zero values for an empty shelf, got best=%+v ok=%v topN=%v", best, ok, topN)
	}
}
