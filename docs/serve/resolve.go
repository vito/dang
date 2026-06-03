package main

import (
	"os"
	"path/filepath"
	"strings"
)

// location is a resolved source position for a quoted excerpt.
type location struct {
	file string // path relative to the working directory, e.g. "lit/language/fields.md"
	line int    // 1-based line number where the match begins
	ok   bool
}

// resolver maps a rendered excerpt back to its markdown source line.
//
// Booklit does not preserve source positions in its rendered HTML, so the
// page and excerpt sent by the browser are matched against the markdown
// sources at submission time. Matching is fuzzy by design: the rendered text
// differs from markdown (inline links such as [#objects] render as their
// section title, emphasis markers are stripped, etc.), so we compare on
// normalized word tokens and find the densest run of matching words.
type resolver struct {
	root  string
	files []sourceFile
	// tag (html basename without extension) -> index into files, for the
	// page that an excerpt most likely came from.
	byTag map[string]int
}

type sourceFile struct {
	path   string  // path including root, e.g. "lit/language/fields.md"
	tokens []token // every word token in the file, in order
}

// token is a normalized word along with the source line it came from.
type token struct {
	word string
	line int
}

func newResolver(root string) *resolver {
	r := &resolver{root: root, byTag: map[string]int{}}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		idx := len(r.files)
		r.files = append(r.files, sourceFile{path: path, tokens: tokenize(text)})
		for _, tag := range pageTags(text, path) {
			if _, seen := r.byTag[tag]; !seen {
				r.byTag[tag] = idx
			}
		}
		return nil
	})
	return r
}

// resolve finds the best source location for excerpt, preferring the markdown
// file that the page was rendered from.
func (r *resolver) resolve(page, excerpt string) location {
	needle := normalizeWords(excerpt)
	if len(needle) == 0 {
		return location{}
	}

	// Prefer the source file matching the page; require a fairly strong match
	// there before falling back to a global search.
	if idx, ok := r.byTag[pageTag(page)]; ok {
		if line, score := r.files[idx].bestMatch(needle); score >= 0.6 {
			return location{file: r.files[idx].path, line: line, ok: true}
		}
	}

	best := location{}
	var bestScore float64
	for _, f := range r.files {
		line, score := f.bestMatch(needle)
		if score > bestScore {
			bestScore = score
			best = location{file: f.path, line: line, ok: true}
		}
	}
	if bestScore < 0.45 {
		return location{}
	}
	return best
}

// bestMatch slides a window the size of needle across the file's tokens and
// returns the source line of the densest overlap with needle, plus a score in
// [0,1] = (matched needle words) / (needle words).
func (f sourceFile) bestMatch(needle []string) (line int, score float64) {
	if len(f.tokens) == 0 {
		return 0, 0
	}
	want := map[string]int{}
	for _, w := range needle {
		want[w]++
	}

	win := min(len(needle), len(f.tokens))

	bestHits := 0
	bestLine := f.tokens[0].line
	for start := 0; start+1 <= len(f.tokens); start++ {
		end := min(start+win, len(f.tokens))
		seen := map[string]int{}
		hits := 0
		for i := start; i < end; i++ {
			w := f.tokens[i].word
			if seen[w] < want[w] {
				seen[w]++
				hits++
			}
		}
		if hits > bestHits {
			bestHits = hits
			bestLine = f.tokens[start].line
		}
		if end == len(f.tokens) {
			break
		}
	}
	return bestLine, float64(bestHits) / float64(len(needle))
}

// tokenize splits markdown into normalized word tokens carrying their line.
func tokenize(text string) []token {
	var toks []token
	for i, raw := range strings.Split(text, "\n") {
		for _, w := range normalizeWords(raw) {
			toks = append(toks, token{word: w, line: i + 1})
		}
	}
	return toks
}

// normalizeWords lowercases and splits on any non-alphanumeric character,
// yielding comparable word tokens from either rendered text or markdown.
func normalizeWords(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	return fields
}

// pageTag derives the section tag from a page path: "/fields.html" -> "fields".
func pageTag(page string) string {
	base := filepath.Base(page)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// pageTags returns the tags a markdown file may be rendered under: its explicit
// {#tag} anchor on the first heading, and its filename stem as a fallback.
func pageTags(text, path string) []string {
	tags := []string{strings.TrimSuffix(filepath.Base(path), ".md")}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		if open := strings.LastIndex(line, "{#"); open != -1 {
			if close := strings.Index(line[open:], "}"); close != -1 {
				tags = append(tags, line[open+2:open+close])
			}
		}
		break // only the first heading names the page
	}
	return tags
}
