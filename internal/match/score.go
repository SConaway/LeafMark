package match

import "strings"

// tokenSetDice computes the Sorensen-Dice coefficient over two strings'
// *word* token sets (2*|A∩B| / (|A|+|B|)), not the character n-grams
// github.com/adrg/strutil's metrics operate on. The spec calls for a
// token-based measure specifically to handle word reordering and subtitle
// noise better than raw edit distance — a whole-string n-gram metric only
// partially gives that, so this is a small hand-rolled set computation
// instead of forcing strutil's API to do something it isn't built for
// (documented here and in the README per the spec's ask to record the
// library/approach choice).
func tokenSetDice(a, b string) float64 {
	setA := tokenSet(a)
	setB := tokenSet(b)
	if len(setA) == 0 && len(setB) == 0 {
		return 1
	}
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}

	intersection := 0
	for tok := range setA {
		if setB[tok] {
			intersection++
		}
	}
	return 2 * float64(intersection) / float64(len(setA)+len(setB))
}

func tokenSet(s string) map[string]bool {
	tokens := strings.Fields(s)
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	return set
}

// authorBoostThreshold and authorBoostWeight tune how much a matching
// author nudges the score up. Author metadata from KOReader is often
// messier than title (multiple authors, "et al.", translator credits) so
// this is deliberately a soft nudge, not a requirement — per spec, author
// is a confidence booster, never a hard filter.
const (
	authorBoostThreshold = 0.5
	authorBoostWeight    = 0.15
)

// TitleAuthorScore scores a candidate against a target title/author,
// primarily on title similarity, with a capped upward nudge when the
// author also looks like a reasonable match. Never lets an author mismatch
// pull the score down — only a match can help it.
func TitleAuthorScore(targetTitle, targetAuthor, candidateTitle, candidateAuthor string) float64 {
	score := tokenSetDice(Normalize(targetTitle), Normalize(candidateTitle))

	if targetAuthor != "" && candidateAuthor != "" {
		authorScore := tokenSetDice(Normalize(targetAuthor), Normalize(candidateAuthor))
		if authorScore > authorBoostThreshold {
			score += (1 - score) * authorBoostWeight * authorScore
		}
	}

	if score > 1 {
		score = 1
	}
	return score
}
