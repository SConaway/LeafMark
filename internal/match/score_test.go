package match

import "testing"

func TestTitleAuthorScoreSubtitleNoise(t *testing.T) {
	score := TitleAuthorScore("Project Hail Mary: A Novel", "Andy Weir", "Project Hail Mary", "Andy Weir")
	if score < 0.95 {
		t.Errorf("expected near-1.0 score for subtitle-noise-only difference, got %v", score)
	}
}

func TestTitleAuthorScoreSeriesAnnotation(t *testing.T) {
	score := TitleAuthorScore("The Fifth Season (The Broken Earth, #1)", "N.K. Jemisin", "The Fifth Season", "N.K. Jemisin")
	if score < 0.95 {
		t.Errorf("expected near-1.0 score for series-annotation-only difference, got %v", score)
	}
}

func TestTitleAuthorScoreWordReordering(t *testing.T) {
	// Token-set based, so word order shouldn't matter much.
	score := TitleAuthorScore("Mary Hail Project", "Andy Weir", "Project Hail Mary", "Andy Weir")
	if score < 0.9 {
		t.Errorf("expected high score despite word reordering, got %v", score)
	}
}

func TestTitleAuthorScoreMessyAuthorStillMatchesOnTitle(t *testing.T) {
	// Messy/absent author metadata shouldn't tank an otherwise-exact title
	// match — author is a booster, never a hard filter.
	score := TitleAuthorScore("Project Hail Mary", "Andy Weir, et al.", "Project Hail Mary", "Andy Weir")
	if score < 0.99 {
		t.Errorf("expected near-exact score from title alone, got %v", score)
	}
}

func TestTitleAuthorScoreNearMissStaysBelowStrictThreshold(t *testing.T) {
	// Same author, meaningfully different title (a different book by the
	// same author) — should score noticeably lower than an exact match, low
	// enough to stay below a strict auto-confirm threshold.
	score := TitleAuthorScore("Artemis", "Andy Weir", "Project Hail Mary", "Andy Weir")
	if score >= 0.8 {
		t.Errorf("expected a near-miss score below a strict threshold, got %v", score)
	}
}

func TestTitleAuthorScoreClearNonMatch(t *testing.T) {
	score := TitleAuthorScore("Project Hail Mary", "Andy Weir", "The Great Gatsby", "F. Scott Fitzgerald")
	if score > 0.2 {
		t.Errorf("expected a very low score for an unrelated book, got %v", score)
	}
}

func TestTitleAuthorScoreAuthorBoostNeverDecreasesScore(t *testing.T) {
	withMismatchedAuthor := TitleAuthorScore("Project Hail Mary", "Someone Else", "Project Hail Mary", "Andy Weir")
	withMatchingAuthor := TitleAuthorScore("Project Hail Mary", "Andy Weir", "Project Hail Mary", "Andy Weir")
	if withMismatchedAuthor > withMatchingAuthor {
		t.Errorf("author mismatch should never score higher than author match: %v > %v", withMismatchedAuthor, withMatchingAuthor)
	}
}
