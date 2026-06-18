//go:build cgo

package dang

import (
	_ "embed"
	"log/slog"
	"sort"
	"sync"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang/danglang"
)

// highlights.scm is the same tree-sitter highlight query the editors and the
// docs site use (it lives in the editors/zed submodule, symlinked into
// treesitter/queries). It is embedded here so the binary highlights without
// needing the repo or submodule at runtime; ./hack/generate refreshes the
// copy from the symlink.
//
//go:embed highlights.scm
var highlightsQuery string

var (
	highlightQuery     *tree_sitter.Query
	highlightCaptures  []string
	highlightQueryOnce sync.Once
	highlightQueryErr  error
)

func compileHighlightQuery() {
	q, err := tree_sitter.NewQuery(danglang.Language(), highlightsQuery)
	if err != nil {
		highlightQueryErr = err
		return
	}
	highlightQuery = q
	highlightCaptures = q.CaptureNames()
}

// Highlight classifies source with tree-sitter and returns styling spans in
// rune offsets, coalesced into maximal runs of one class. Bytes covered by no
// capture (or by captures that map to no class) produce no span. It degrades
// gracefully: a parse/query failure yields no spans, so callers render plain.
func Highlight(source string) []HighlightSpan {
	highlightQueryOnce.Do(compileHighlightQuery)
	if highlightQueryErr != nil {
		slog.Debug("highlight query failed to compile", "error", highlightQueryErr)
		return nil
	}

	src := []byte(source)

	// tree-sitter parsers are not thread-safe; serialize access (shared with
	// completion's parser).
	tsMu.Lock()
	tree := tsParser.Parse(src, nil)
	tsMu.Unlock()
	if tree == nil {
		return nil
	}
	defer tree.Close()

	// Per-byte class, then narrower (and later) captures override wider ones,
	// mirroring how the editors and docs resolve the same query.
	type span struct {
		start, end int
		class      string
	}
	var spans []span

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	captures := cursor.Captures(highlightQuery, tree.RootNode(), src)
	for {
		match, idx := captures.Next()
		if match == nil {
			break
		}
		c := match.Captures[idx]
		class := captureClass(highlightCaptures[c.Index])
		if class == "" {
			continue
		}
		spans = append(spans, span{
			start: int(c.Node.StartByte()),
			end:   int(c.Node.EndByte()),
			class: class,
		})
	}

	// Wider captures first so narrower (and later) ones overwrite them.
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end-spans[i].start > spans[j].end-spans[j].start
	})

	byteClass := make([]string, len(src))
	for _, s := range spans {
		for b := s.start; b < s.end && b < len(byteClass); b++ {
			byteClass[b] = s.class
		}
	}

	return coalesceRuneSpans(source, byteClass)
}

// coalesceRuneSpans walks source rune by rune, reading each rune's class from
// the class of its first byte, and merges consecutive runes of the same
// non-empty class into HighlightSpans addressed in rune offsets.
func coalesceRuneSpans(source string, byteClass []string) []HighlightSpan {
	var out []HighlightSpan
	runeIdx := 0
	curClass := ""
	curStart := 0
	flush := func(end int) {
		if curClass != "" {
			out = append(out, HighlightSpan{Start: curStart, End: end, Class: curClass})
		}
	}
	for bytePos := range source { // range over string yields byte index of each rune
		class := ""
		if bytePos < len(byteClass) {
			class = byteClass[bytePos]
		}
		if class != curClass {
			flush(runeIdx)
			curClass = class
			curStart = runeIdx
		}
		runeIdx++
	}
	flush(runeIdx)
	return out
}
