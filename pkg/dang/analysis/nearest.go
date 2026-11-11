package analysis

import (
	"sort"
	"strings"
	"unicode"
)

// nearest ranks members by how close their names are to name, a name that
// was not found, and returns at most limit of the closest: names that differ
// only in case or by a few edits (`lenght` for length), then names that one
// is a prefix of the other (`findFirst` for find). Only when neither kind
// matches does it fall back to names sharing a camelCase word with name
// (`sortStrings` for sortBy), which are weaker guesses. Nil when nothing is
// close.
func nearest(name string, members []Member, limit int) []Member {
	if name == "" || limit <= 0 {
		return nil
	}
	lower := strings.ToLower(name)
	words := camelWords(name)

	type candidate struct {
		member Member
		score  int // lower is closer
		dist   int
	}
	var strong, weak []candidate
	seen := map[string]bool{}
	for _, m := range members {
		if m.Name == name || seen[m.Name] {
			continue
		}
		cand := strings.ToLower(m.Name)
		dist := levenshtein(lower, cand)
		switch {
		case cand == lower:
			strong = append(strong, candidate{m, 0, dist})
		case dist <= maxEdits(len(lower)):
			strong = append(strong, candidate{m, dist, dist})
		case isPrefixMatch(lower, cand):
			strong = append(strong, candidate{m, 4, dist})
		default:
			if shared := sharedWords(words, camelWords(m.Name)); shared > 0 {
				// More words in common is closer.
				weak = append(weak, candidate{m, 10 - shared, dist})
			} else {
				continue
			}
		}
		seen[m.Name] = true
	}
	found := strong
	if len(found) == 0 {
		found = weak
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.score != b.score {
			return a.score < b.score
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		return a.member.Name < b.member.Name
	})
	if len(found) > limit {
		found = found[:limit]
	}
	if len(found) == 0 {
		return nil
	}
	near := make([]Member, len(found))
	for i, c := range found {
		near[i] = c.member
	}
	return near
}

// maxEdits is how many edits a name of length n may be from another and
// still count as a misspelling of it.
func maxEdits(n int) int {
	switch {
	case n <= 2:
		return 0
	case n <= 5:
		return 1
	case n <= 8:
		return 2
	default:
		return 3
	}
}

// isPrefixMatch reports whether one of two names is a prefix of the other,
// the shorter being long enough to mean something.
func isPrefixMatch(a, b string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	return len(a) >= 3 && strings.HasPrefix(b, a)
}

// sharedWords counts the words of a that b has too, ignoring words too
// short to mean anything on their own (`to`, `is`).
func sharedWords(a, b []string) int {
	shared := 0
	for _, w := range a {
		if len(w) < 3 {
			continue
		}
		for _, o := range b {
			if w == o {
				shared++
				break
			}
		}
	}
	return shared
}

// camelWords splits an identifier into its lowercased words, at case
// changes, digits and underscores: "sortStrings" is [sort strings],
// "toJSON" is [to json], "HTMLEscape" is [html escape].
func camelWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || unicode.IsSpace(r):
			flush()
			continue
		case unicode.IsUpper(r):
			// A new word starts at an upper-case letter after a lower-case
			// one or a digit ("sortStrings"), or at the last capital of a run
			// that a lower-case letter follows ("HTMLEscape").
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					flush()
				}
			}
		case unicode.IsDigit(r):
			if i > 0 && !unicode.IsDigit(runes[i-1]) {
				flush()
			}
		default:
			if i > 0 && unicode.IsDigit(runes[i-1]) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// levenshtein is the edit distance between a and b, counting an adjacent
// transposition as one edit (`lenght` is one edit from length).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	// Three rows: two back for transpositions.
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}
