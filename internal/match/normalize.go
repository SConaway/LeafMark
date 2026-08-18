// Package match fuzzy-matches KOReader's title/author metadata against a
// small shelf-scoped candidate pool (want-to-read + currently-reading only,
// per the project spec — not the full Goodreads catalog, which is what
// makes a strict threshold usable here).
package match

import (
	"regexp"
	"strings"
)

// noisePatterns strips subtitle/series/edition annotations that commonly
// differ between KOReader's embedded metadata and a book's Goodreads shelf
// title, but don't reflect a real title difference. Kept isolated in its
// own function since this is exactly the kind of thing that gets tuned
// repeatedly as real mismatches are observed.
var noisePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\s*:\s*a novel\s*$`),
	regexp.MustCompile(`(?i)\s*\(\s*book\s+\d+\s*\)\s*`),
	regexp.MustCompile(`(?i)\s*\([^)]*#\d+[^)]*\)\s*`), // series annotations like "(The Broken Earth, #1)"
	regexp.MustCompile(`(?i)\s*\(\s*(kindle|signed|special|annotated|illustrated|unabridged)[^)]*\s*edition\s*\)\s*`),
	regexp.MustCompile(`(?i)\s*\(\s*audiobook\s*\)\s*`),
}

var punctuationPattern = regexp.MustCompile(`[^\w\s]`)
var whitespacePattern = regexp.MustCompile(`\s+`)

// Normalize lowercases, strips punctuation, and drops common noise
// suffixes/prefixes so two titles that differ only in that noise compare
// as equal token sets.
func Normalize(s string) string {
	for _, p := range noisePatterns {
		s = p.ReplaceAllString(s, " ")
	}
	s = strings.ToLower(s)
	s = punctuationPattern.ReplaceAllString(s, " ")
	s = whitespacePattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
