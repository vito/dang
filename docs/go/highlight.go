//go:build cgo

package dangdocs

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unsafe"

	tree_sitter_toml "github.com/tree-sitter-grammars/tree-sitter-toml/bindings/go"
	tree_sitter_yaml "github.com/tree-sitter-grammars/tree-sitter-yaml/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_bash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	"github.com/vito/dang/v2/pkg/dang/danglang"
)

// Every code surface on the site is highlighted by tree-sitter: each language
// pairs a grammar with a highlight query, capture names map onto the tok-*
// CSS classes (the same names docs/js/playground.js assigns in the browser
// editors), and syntax.css colors the classes from base16 variables. Dang
// uses the same grammar and query as Neovim, Zed, and the playground, so
// static blocks highlight identically everywhere.

//go:embed queries/bash.scm
var bashHighlightsQuery string

//go:embed queries/toml.scm
var tomlHighlightsQuery string

//go:embed queries/go.scm
var goHighlightsQuery string

//go:embed queries/ruby.scm
var rubyHighlightsQuery string

//go:embed queries/yaml.scm
var yamlHighlightsQuery string

// highlighter pairs a tree-sitter grammar with its compiled highlight query.
type highlighter struct {
	language  func() *tree_sitter.Language
	loadQuery func() (string, error)

	// wrapPrefix/wrapSuffix, when set, give unparseable sources a second
	// chance inside a synthetic enclosing construct (Dang signature
	// fragments only parse inside an interface body).
	wrapPrefix, wrapSuffix string

	// loadInjections, when set (Dang only), is the injection query source: it
	// captures @injection.language + @injection.content on language-tagged
	// multi-line templates (```toml … ```) so their bodies are re-highlighted
	// with that language's grammar. Best-effort — a missing or bad query just
	// leaves the templates highlighted as plain Dang strings.
	loadInjections func() (string, error)

	once           sync.Once
	query          *tree_sitter.Query
	captures       []string
	injections     *tree_sitter.Query
	injectionNames []string
	loadErr        error
}

// signaturePrefix wraps declaration fragments for parsing. Bare field
// declarations like `withExec(args: [String!]!): Container!` — common in
// prose — only parse inside an interface body.
const (
	signaturePrefix = "interface _ {\n"
	signatureSuffix = "\n}"
)

var highlighters = map[string]*highlighter{
	"dang": {
		language:       danglang.Language,
		loadQuery:      loadDangHighlightQuery,
		loadInjections: loadDangInjectionQuery,
		wrapPrefix:     signaturePrefix,
		wrapSuffix:     signatureSuffix,
	},
	"bash": {
		language:  rawLanguage(tree_sitter_bash.Language),
		loadQuery: staticQuery(bashHighlightsQuery),
	},
	"toml": {
		language:  rawLanguage(tree_sitter_toml.Language),
		loadQuery: staticQuery(tomlHighlightsQuery),
	},
	"go": {
		language:  rawLanguage(tree_sitter_go.Language),
		loadQuery: staticQuery(goHighlightsQuery),
		// A bare Go snippet (no `package`) parses as an error soup where
		// keywords degrade to identifiers; retry inside a synthetic package so
		// injected ```go blocks highlight like real Go.
		wrapPrefix: "package p\n",
	},
	"ruby": {
		language:  rawLanguage(tree_sitter_ruby.Language),
		loadQuery: staticQuery(rubyHighlightsQuery),
	},
	"yaml": {
		language:  rawLanguage(tree_sitter_yaml.Language),
		loadQuery: staticQuery(yamlHighlightsQuery),
	},
}

// languageAliases maps fence language tags (and injected-string language tags)
// onto registered highlighters. Unlisted languages render as plain code.
var languageAliases = map[string]string{
	"dang":  "dang",
	"bash":  "bash",
	"sh":    "bash",
	"shell": "bash",
	"toml":  "toml",
	"go":    "go",
	"ruby":  "ruby",
	"rb":    "ruby",
	"yaml":  "yaml",
	"yml":   "yaml",
}

// rawLanguage adapts the unsafe.Pointer constructor the upstream grammar
// bindings export, deferring the cgo call until the language is first used.
func rawLanguage(raw func() unsafe.Pointer) func() *tree_sitter.Language {
	return func() *tree_sitter.Language { return tree_sitter.NewLanguage(raw()) }
}

func staticQuery(source string) func() (string, error) {
	return func() (string, error) { return source, nil }
}

// loadDangHighlightQuery finds highlights.scm the same way
// build-highlight-assets.sh does: the in-repo copy (a symlink into the
// editors/zed submodule) when it's checked out, otherwise the copy the build
// script fetched into docs/js/. DANG_HIGHLIGHT_QUERY overrides both.
func loadDangHighlightQuery() (string, error) {
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

// loadDangInjectionQuery finds injections.scm the same way
// loadDangHighlightQuery finds highlights.scm — the in-repo copy (a symlink
// into editors/zed) when checked out, otherwise the copy build-highlight-
// assets.sh fetched into docs/js/. DANG_INJECTION_QUERY overrides both.
func loadDangInjectionQuery() (string, error) {
	var candidates []string
	if p := os.Getenv("DANG_INJECTION_QUERY"); p != "" {
		candidates = append(candidates, p)
	}
	if _, src, _, ok := runtime.Caller(0); ok {
		root := filepath.Dir(filepath.Dir(filepath.Dir(src)))
		candidates = append(candidates,
			filepath.Join(root, "treesitter", "queries", "injections.scm"),
			filepath.Join(root, "docs", "js", "dang-injections.scm"),
		)
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("injections.scm not found (tried %s)", strings.Join(candidates, ", "))
}

// applyInjections overwrites, in place, the class of every byte inside a
// language-tagged multi-line template (```toml … ```) with that language's
// own highlighting. It runs the injection query, and for each match whose
// @injection.language names a registered grammar, re-classifies each
// @injection.content region and copies the result over the region — which the
// base pass had colored as one Dang string. Interpolation regions (${…}) are
// not captured as content, so they stay Dang.
func (l *highlighter) applyInjections(source string, classes []string) {
	src := []byte(source)

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(l.language()); err != nil {
		return
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return
	}
	defer tree.Close()

	cursor := tree_sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(l.injections, tree.RootNode(), src)
	for {
		match := matches.Next()
		if match == nil {
			break
		}

		var lang string
		var ranges, segments [][2]int
		for _, c := range match.Captures {
			switch l.injectionNames[c.Index] {
			case "injection.language":
				lang = c.Node.Utf8Text(src)
			case "injection.content":
				ranges = append(ranges, [2]int{int(c.Node.StartByte()), int(c.Node.EndByte())})
			}
		}
		if lang == "" {
			continue
		}
		if _, ok := languageAliases[strings.ToLower(lang)]; !ok {
			continue // no grammar for this tag; leave it as a Dang string
		}

		// The query captures content as many tiny parts (one per grammar token)
		// and omits ${…} interpolation. Merge contiguous parts into segments, so
		// each embedded run is parsed and highlighted as a whole rather than a
		// char at a time; the interpolation gaps between segments stay Dang.
		sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
		for _, r := range ranges {
			if n := len(segments); n > 0 && segments[n-1][1] == r[0] {
				segments[n-1][1] = r[1]
			} else {
				segments = append(segments, r)
			}
		}
		for _, s := range segments {
			start, end := s[0], s[1]
			if start < 0 || end > len(classes) || start >= end {
				continue
			}
			embedded := classifyCode(lang, source[start:end])
			if embedded == nil {
				continue
			}
			copy(classes[start:end], embedded)
		}
	}
}

func (l *highlighter) compile() {
	source, err := l.loadQuery()
	if err != nil {
		l.loadErr = err
		return
	}
	query, qerr := tree_sitter.NewQuery(l.language(), source)
	if qerr != nil {
		l.loadErr = fmt.Errorf("compile highlight query: %w", qerr)
		return
	}
	l.query = query
	l.captures = query.CaptureNames()

	// Compile the injection query too, when this grammar has one. Best-effort:
	// a missing or unparseable query just skips injections, so a language-tagged
	// template stays highlighted as a plain Dang string.
	if l.loadInjections != nil {
		if src, ierr := l.loadInjections(); ierr == nil {
			if iq, iqerr := tree_sitter.NewQuery(l.language(), src); iqerr == nil {
				l.injections = iq
				l.injectionNames = iq.CaptureNames()
			}
		}
	}
}

// classifyCode returns the token CSS class for each byte of source (empty for
// unstyled bytes), or nil when the language has no registered grammar or its
// highlight query is unavailable — callers render plain code then.
func classifyCode(language, source string) []string {
	name, ok := languageAliases[strings.ToLower(language)]
	if !ok {
		return nil
	}
	l := highlighters[name]
	l.once.Do(l.compile)
	if l.loadErr != nil {
		return nil
	}
	var classes []string
	if name == "bash" {
		if c, ok := classifyTranscript(l, source); ok {
			classes = c
		}
	}
	if classes == nil {
		classes = l.classify(source)
	}
	// Re-highlight any embedded language-tagged templates (```toml … ```) with
	// their own grammar. Only Dang carries an injection query.
	if l.injections != nil {
		l.applyInjections(source, classes)
	}
	return classes
}

// classifyTranscript handles ```sh fences that are terminal transcripts
// rather than scripts: when any line starts with the conventional "$ "
// prompt, each prompted command is highlighted as bash on its own and every
// other line — program output — stays plain, instead of garbling the whole
// transcript through one bash parse. Reports false when no line is prompted,
// so plain scripts take the normal whole-source path.
func classifyTranscript(l *highlighter, source string) ([]string, bool) {
	const prompt = "$ "
	lines := strings.SplitAfter(source, "\n")

	prompted := false
	for _, line := range lines {
		if strings.HasPrefix(line, prompt) {
			prompted = true
			break
		}
	}
	if !prompted {
		return nil, false
	}

	classes := make([]string, len(source))
	offset := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prompt) {
			// the prompt itself and the trailing newline stay unstyled
			command := strings.TrimSuffix(line[len(prompt):], "\n")
			copy(classes[offset+len(prompt):], l.classify(command))
		}
		offset += len(line)
	}
	return classes, true
}

// classify assigns a class per byte: wider captures first, narrower (and
// later) captures override, mirroring how the editors resolve the same query.
// The highlighter must be compiled (l.once) before calling.
func (l *highlighter) classify(source string) []string {
	spans, errBytes := l.capture(source, 0)
	if errBytes > 0 && l.wrapPrefix != "" {
		// The source didn't fully parse. Snippets on the site are often
		// declaration fragments (stdlib signatures); retry inside the
		// synthetic wrapper and keep whichever parse recovered more.
		wrapped, wrappedErrBytes := l.capture(l.wrapPrefix+source+l.wrapSuffix, len(l.wrapPrefix))
		if wrappedErrBytes < errBytes {
			spans = wrapped
		}
	}

	classes := make([]string, len(source))
	for _, s := range spans {
		cls := captureClass(s.capture)
		if cls == "" {
			continue
		}
		for b := s.start; b < s.end; b++ {
			classes[b] = cls
		}
	}
	return classes
}

type captureSpan struct {
	start, end int
	capture    string
}

// capture parses source and returns highlight spans translated back by
// offset, along with the number of bytes covered by ERROR/MISSING nodes
// within the snippet region (source stripped of the wrap affixes).
func (l *highlighter) capture(source string, offset int) ([]captureSpan, int) {
	src := []byte(source)
	snippetLen := len(src) - offset
	if offset > 0 {
		snippetLen -= len(l.wrapSuffix)
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(l.language()); err != nil {
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

// captureClass maps a highlight query capture name to the site's token CSS
// class — the same tok-* names docs/js/playground.js assigns in the browser
// editors (the two switches must stay in lockstep), themed by syntax.css.
// Unhandled captures (notably @error, since docs snippets may be fragments)
// return "" to stay unstyled.
func captureClass(name string) string {
	switch name {
	case "variable.special":
		return "tok-self"
	case "function.builtin":
		return "tok-builtin"
	case "function.macro":
		return "tok-directive"
	case "string.escape":
		return "tok-escape"
	case "property":
		return "tok-property"
	case "label":
		return "tok-label"
	case "type":
		// all capitalized type names highlight the same, like the editors
		return "tok-type"
	}
	switch strings.SplitN(name, ".", 2)[0] {
	case "keyword":
		return "tok-keyword"
	case "constant", "number":
		return "tok-number"
	case "string":
		return "tok-string"
	case "comment":
		return "tok-comment"
	case "operator":
		return "tok-operator"
	case "punctuation":
		return "tok-punct"
	case "function":
		return "tok-function"
	case "variable":
		return "tok-variable"
	}
	return ""
}
