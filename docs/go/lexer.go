//go:build cgo

package dangdocs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/vito/dang/v2/pkg/dang/danglang"
)

func init() {
	lexers.Register(dangLexer)
}

// dangLexer is a chroma.Lexer backed by the Dang tree-sitter grammar and the
// same highlight query the editors and the playground use, so static code
// blocks on the site highlight identically to Neovim, Zed, and the in-browser
// editor. Chroma stays on as the HTML/CSS formatting layer (and the lexer for
// the few non-Dang fences); only the tokenization is tree-sitter's.
var dangLexer = &treeSitterLexer{
	config: &chroma.Config{
		Name:      "Dang",
		Aliases:   []string{"dang"},
		Filenames: []string{"*.dang"},
		MimeTypes: []string{"text/x-dang"},
	},
}

type treeSitterLexer struct {
	config *chroma.Config

	once     sync.Once
	query    *tree_sitter.Query
	captures []string
	loadErr  error
}

func (l *treeSitterLexer) Config() *chroma.Config { return l.config }

func (l *treeSitterLexer) SetRegistry(*chroma.LexerRegistry) chroma.Lexer { return l }

func (l *treeSitterLexer) SetAnalyser(func(string) float32) chroma.Lexer { return l }

func (l *treeSitterLexer) AnalyseText(string) float32 { return 0 }

// loadHighlightQuery finds highlights.scm the same way build-highlight-assets.sh
// does: the in-repo copy (a symlink into the editors/zed submodule) when it's
// checked out, otherwise the copy the build script fetched into docs/js/.
// DANG_HIGHLIGHT_QUERY overrides both.
func loadHighlightQuery() (string, error) {
	var candidates []string
	if p := os.Getenv("DANG_HIGHLIGHT_QUERY"); p != "" {
		candidates = append(candidates, p)
	}
	if _, src, _, ok := runtime.Caller(0); ok {
		root := filepath.Dir(filepath.Dir(filepath.Dir(src)))
		candidates = append(candidates,
			filepath.Join(root, "treesitter", "queries", "highlights.scm"),
			filepath.Join(root, "docs", "js", "dang-highlights.scm"),
		)
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("highlights.scm not found (tried %s)", strings.Join(candidates, ", "))
}

func (l *treeSitterLexer) compile() {
	source, err := loadHighlightQuery()
	if err != nil {
		l.loadErr = err
		return
	}
	query, qerr := tree_sitter.NewQuery(danglang.Language(), source)
	if qerr != nil {
		l.loadErr = fmt.Errorf("compile highlights.scm: %w", qerr)
		return
	}
	l.query = query
	l.captures = query.CaptureNames()
}

// signaturePrefix wraps declaration fragments for parsing. Bare field
// declarations like `withExec(args: [String!]!): Container!` — common in
// prose — only parse inside an interface body.
const (
	signaturePrefix = "interface _ {\n"
	signatureSuffix = "\n}"
)

func (l *treeSitterLexer) Tokenise(_ *chroma.TokeniseOptions, text string) (chroma.Iterator, error) {
	l.once.Do(l.compile)
	if l.loadErr != nil {
		return nil, l.loadErr
	}
	if text == "" {
		return chroma.Literator(), nil
	}

	spans, errBytes := l.capture(text, 0)
	if errBytes > 0 {
		// The source didn't fully parse. Snippets on the site are often
		// declaration fragments (stdlib signatures); retry inside a synthetic
		// interface body and keep whichever parse recovered more.
		wrapped, wrappedErrBytes := l.capture(signaturePrefix+text+signatureSuffix, len(signaturePrefix))
		if wrappedErrBytes < errBytes {
			spans = wrapped
		}
	}

	// Paint a token type per byte: wider captures first, narrower (and later)
	// captures override, mirroring how the editors resolve the same query.
	types := make([]chroma.TokenType, len(text))
	for i := range types {
		types[i] = chroma.Text
	}
	for _, s := range spans {
		tt, ok := captureTokenType(s.capture, text[s.start:s.end])
		if !ok {
			continue
		}
		for b := s.start; b < s.end; b++ {
			types[b] = tt
		}
	}

	var tokens []chroma.Token
	for i := 0; i < len(text); {
		j := i + 1
		for j < len(text) && types[j] == types[i] {
			j++
		}
		tokens = append(tokens, chroma.Token{Type: types[i], Value: text[i:j]})
		i = j
	}
	return chroma.Literator(tokens...), nil
}

type captureSpan struct {
	start, end int
	capture    string
}

// capture parses source and returns highlight spans translated back by
// offset, along with the number of bytes covered by ERROR/MISSING nodes
// within the [offset, len(source)-offset') snippet region.
func (l *treeSitterLexer) capture(source string, offset int) ([]captureSpan, int) {
	src := []byte(source)
	snippetLen := len(src) - offset
	if offset > 0 {
		snippetLen -= len(signatureSuffix)
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(danglang.Language()); err != nil {
		return nil, snippetLen
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, snippetLen
	}
	defer tree.Close()

	root := tree.RootNode()

	var spans []captureSpan
	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	captures := cursor.Captures(l.query, root, src)
	for {
		match, idx := captures.Next()
		if match == nil {
			break
		}
		c := match.Captures[idx]
		start := int(c.Node.StartByte()) - offset
		end := int(c.Node.EndByte()) - offset
		if end <= 0 || start >= snippetLen {
			continue
		}
		spans = append(spans, captureSpan{
			start:   max(start, 0),
			end:     min(end, snippetLen),
			capture: l.captures[c.Index],
		})
	}

	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end-spans[i].start > spans[j].end-spans[j].start
	})

	return spans, errorBytes(root, offset, offset+snippetLen)
}

// errorBytes sums the bytes of ERROR/MISSING nodes intersecting [start, end).
func errorBytes(node *tree_sitter.Node, start, end int) int {
	if node.IsError() || node.IsMissing() {
		s, e := int(node.StartByte()), int(node.EndByte())
		if e < start || s > end {
			return 0
		}
		// A zero-width MISSING node still poisons the parse it appears in.
		return max(min(e, end)-max(s, start), 1)
	}
	total := 0
	for i := uint(0); i < node.ChildCount(); i++ {
		total += errorBytes(node.Child(i), start, end)
	}
	return total
}

// captureTokenType maps a highlight query capture name to the chroma token
// type carrying the equivalent CSS class, keeping chroma.css and the
// light/dark palettes working unchanged. Unhandled captures (notably @error,
// since docs snippets may be fragments) report false to stay unstyled.
func captureTokenType(name, text string) (chroma.TokenType, bool) {
	switch name {
	case "error":
		return 0, false
	case "variable.special":
		return chroma.NameBuiltinPseudo, true
	case "function.builtin":
		return chroma.NameBuiltin, true
	case "function.macro":
		return chroma.NameDecorator, true
	case "string.escape":
		return chroma.LiteralStringEscape, true
	case "property":
		return chroma.NameProperty, true
	case "label":
		return chroma.NameTag, true
	case "type":
		if builtinTypes[text] {
			return chroma.KeywordType, true
		}
		return chroma.NameClass, true
	}
	switch strings.SplitN(name, ".", 2)[0] {
	case "keyword":
		return chroma.Keyword, true
	case "constant":
		if strings.HasPrefix(name, "constant.numeric") {
			return chroma.LiteralNumber, true
		}
		return chroma.KeywordConstant, true
	case "string":
		return chroma.LiteralString, true
	case "comment":
		return chroma.Comment, true
	case "operator":
		return chroma.Operator, true
	case "punctuation":
		return chroma.Punctuation, true
	case "function":
		return chroma.NameFunction, true
	case "variable":
		return chroma.Name, true
	}
	return 0, false
}
