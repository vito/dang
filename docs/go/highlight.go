package dangdocs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/vito/dang/v2/pkg/dang/tswasm"
)

// Every code surface on the site is highlighted into the same tok-* CSS
// classes (the names docs/js/playground.js assigns in the browser editors),
// which syntax.css colors from base16 variables. Dang is highlighted by
// tree-sitter, with the grammar and query Neovim, Zed, the playground and
// the REPL use (run as WebAssembly, like the REPL), so static blocks
// highlight identically everywhere. Other languages (shell, TOML, Go, Ruby,
// YAML) only ever appear as static snippets, so chroma lexes them, its token
// types mapped onto the same classes.

// languageAliases maps fence language tags (and injected-string language tags)
// onto the languages highlighted. Unlisted languages render as plain code.
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

// classifyCode returns the token CSS class for each byte of source (empty for
// unstyled bytes), or nil when the language isn't highlighted or its
// highlighter is unavailable — callers render plain code then.
func classifyCode(language, source string) []string {
	name, ok := languageAliases[strings.ToLower(language)]
	if !ok {
		return nil
	}
	if name == "dang" {
		return classifyDang(source)
	}
	lexer := lexers.Get(name)
	if lexer == nil {
		return nil
	}
	classify := func(source string) []string { return classifyChroma(name, lexer, source) }
	if name == "bash" {
		if classes, ok := classifyTranscript(classify, source); ok {
			return classes
		}
	}
	return classify(source)
}

// classifyTranscript handles ```sh fences that are terminal transcripts
// rather than scripts: when any line starts with the conventional "$ "
// prompt, each prompted command is highlighted as bash on its own and every
// other line — program output — stays plain, instead of garbling the whole
// transcript as one script. Reports false when no line is prompted, so plain
// scripts take the normal whole-source path.
func classifyTranscript(classify func(string) []string, source string) ([]string, bool) {
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
			copy(classes[offset+len(prompt):], classify(command))
		}
		offset += len(line)
	}
	return classes, true
}

// classifyChroma assigns each byte the class of the chroma token it is in,
// refined for the language as refineBash and refineTOML do, or returns nil
// if lexing fails.
func classifyChroma(name string, lexer chroma.Lexer, source string) []string {
	it, err := lexer.Tokenise(nil, source)
	if err != nil {
		return nil
	}
	var tokens []chroma.Token
	offset := 0
	for token := it(); token != chroma.EOF; token = it() {
		end := offset + len(token.Value)
		// Some lexers add a final newline; anything past the source, or
		// that isn't the source, means the tokens can't be trusted.
		if end > len(source) || source[offset:end] != token.Value {
			if offset == len(source) && strings.TrimSpace(token.Value) == "" {
				break
			}
			return nil
		}
		tokens = append(tokens, token)
		offset = end
	}

	tokenClasses := make([]string, len(tokens))
	for i, token := range tokens {
		tokenClasses[i] = chromaClass(token)
	}
	switch name {
	case "bash":
		refineBash(tokens, tokenClasses)
	case "toml":
		refineTOML(tokens, tokenClasses)
	}

	classes := make([]string, len(source))
	offset = 0
	for i, token := range tokens {
		for b := offset; b < offset+len(token.Value); b++ {
			classes[b] = tokenClasses[i]
		}
		offset += len(token.Value)
	}
	return classes
}

// chromaClass maps a chroma token onto the site's token CSS class, the way
// captureClass maps tree-sitter captures: constants (true, nil) share the
// number color, keys and attributes are properties, functions are
// functions. Plain text and names chroma doesn't distinguish return "" to
// stay unstyled.
func chromaClass(token chroma.Token) string {
	t := token.Type
	switch t {
	case chroma.KeywordConstant, chroma.NameConstant:
		return "tok-number"
	case chroma.KeywordType, chroma.NameClass, chroma.NameException, chroma.NameNamespace:
		return "tok-type"
	case chroma.NameBuiltinPseudo:
		return "tok-self"
	case chroma.NameBuiltin, chroma.NameFunction, chroma.NameFunctionMagic:
		return "tok-function"
	case chroma.NameAttribute, chroma.NameTag, chroma.NameProperty:
		return "tok-property"
	case chroma.NameLabel:
		return "tok-label"
	case chroma.NameDecorator:
		return "tok-directive"
	case chroma.LiteralStringEscape:
		return "tok-escape"
	case chroma.LiteralStringInterpol:
		return "tok-punct"
	case chroma.LiteralDate:
		return "tok-string"
	case chroma.Punctuation:
		if token.Value == "=" {
			return "tok-operator"
		}
		return "tok-punct"
	}
	switch {
	case t.InCategory(chroma.Keyword):
		return "tok-keyword"
	case t.InSubCategory(chroma.NameVariable):
		return "tok-variable"
	case t.InSubCategory(chroma.LiteralString):
		return "tok-string"
	case t.InSubCategory(chroma.LiteralNumber):
		return "tok-number"
	case t.InCategory(chroma.Comment):
		return "tok-comment"
	case t.InCategory(chroma.Operator):
		return "tok-operator"
	}
	return ""
}

// refineTOML colors bare keys and table names as properties, which chroma
// leaves as plain names.
func refineTOML(tokens []chroma.Token, classes []string) {
	for i, token := range tokens {
		if token.Type == chroma.NameOther {
			classes[i] = "tok-property"
		}
	}
}

// refineBash colors shell the way the tree-sitter bash query the site used
// to highlight with did, where chroma's lexer has no opinion: the word in
// command position is a function (chroma leaves it plain text), a word
// starting with "-" is a flag (number color), variable names are properties,
// export and unset are keywords, and a command substitution inside a string
// stays string-colored but for its command.
func refineBash(tokens []chroma.Token, classes []string) {
	command := true    // the next word is in command position
	inString := false  // within a double-quoted string
	flag := false      // within a word that started with "-"
	forVar := false    // the next word is a for loop's variable
	forIn := false     // the next word may be a for loop's "in"
	assigning := false // within a NAME=value before a command
	for i, token := range tokens {
		v := token.Value
		if v == "" {
			continue // an empty group of a lexer rule
		}
		if strings.TrimSpace(v) == "" && token.Type != chroma.LiteralStringDouble {
			if inString {
				classes[i] = "tok-string"
			} else if strings.Contains(v, "\n") {
				command = true
			}
			flag = false
			assigning = false
			continue
		}
		if inString {
			// All string but the variables and a substitution's command.
			switch {
			case token.Type == chroma.LiteralStringDouble && v == `"`:
				inString = false
			case token.Type.InSubCategory(chroma.NameVariable):
				classes[i] = "tok-property"
			case v == "$(" || v == "`":
				classes[i] = "tok-string"
				command = true
			case command && !token.Type.InSubCategory(chroma.LiteralString):
				classes[i] = "tok-function"
				command = false
			default:
				classes[i] = "tok-string"
			}
			continue
		}
		switch {
		case token.Type.InCategory(chroma.Comment):
			continue
		case token.Type == chroma.LiteralStringEscape && v == "\\\n":
			// a line continuation: not an escape, and the command goes on
			classes[i] = ""
			continue
		case token.Type == chroma.LiteralStringDouble && v == `"`:
			inString = true
			command = command && assigning
			continue
		case token.Type.InSubCategory(chroma.LiteralString):
			command = command && assigning
			continue
		case token.Type.InSubCategory(chroma.NameVariable):
			classes[i] = "tok-property"
			// A leading NAME=value leaves the command to come.
			if command && nextValue(tokens, i) == "=" {
				assigning = true
			} else {
				command = false
			}
			continue
		case token.Type == chroma.NameBuiltin && (v == "export" || v == "unset"):
			classes[i] = "tok-keyword"
			continue
		case v == "$(" || v == "`":
			classes[i] = ""
			command = true
			continue
		case token.Type.InCategory(chroma.Keyword) && strings.Trim(v, "${}()") == "":
			// the rest of an expansion or substitution's delimiters
			classes[i] = ""
			continue
		case token.Type.InCategory(chroma.Keyword) && command:
			// if, then, do, done, ...: the next word is a command again,
			// but for a for loop's variable
			forVar = v == "for"
			command = !forVar
			continue
		case v == "&" && i > 0 && strings.HasSuffix(tokens[i-1].Value, ">"):
			// a redirection's >&, as in 2>&1
			classes[i] = ""
			continue
		case token.Type == chroma.Punctuation || v == "&&" || v == "||":
			switch v {
			case "|", "&", "&&", "||":
				classes[i] = "tok-operator"
			default:
				classes[i] = ""
			}
			command = true
			continue
		case v == "<" || v == ">" || v == ">>" || v == "<<":
			classes[i] = "tok-operator"
			continue
		case v == "=":
			classes[i] = ""
			if flag {
				classes[i] = "tok-number"
			}
			continue
		}

		// A word (or a keyword out of place, like echo's done).
		afterForVar := forIn
		forIn = false
		switch {
		case assigning:
			classes[i] = ""
		case forVar:
			classes[i] = "tok-property"
			forVar, forIn = false, true
		case afterForVar && v == "in":
			classes[i] = "tok-keyword"
		case command && (v == "[" || v == "[["):
			// a test, not a command named [
			classes[i] = ""
			command = false
		case command:
			classes[i] = "tok-function"
			command = false
		case strings.HasPrefix(v, "-") || flag:
			classes[i] = "tok-number"
			flag = true
		default:
			classes[i] = ""
		}
	}
}

// nextValue returns the text of the token after tokens[i] that has any,
// skipping empty groups.
func nextValue(tokens []chroma.Token, i int) string {
	for _, t := range tokens[i+1:] {
		if t.Value != "" {
			return t.Value
		}
	}
	return ""
}

// dangHighlighter holds the Dang highlight and injection queries, compiled
// on first use.
var dangHighlighter struct {
	once       sync.Once
	query      *tswasm.Query
	injections *tswasm.Query // nil without one
	err        error
}

// signaturePrefix wraps declaration fragments for parsing. Bare field
// declarations like `withExec(args: [String!]!): Container!` — common in
// prose — only parse inside an interface body.
const (
	signaturePrefix = "interface _ {\n"
	signatureSuffix = "\n}"
)

func compileDangQueries() {
	h := &dangHighlighter
	source, err := loadDangQuery("highlights.scm", "DANG_HIGHLIGHT_QUERY", "dang-highlights.scm")
	if err != nil {
		h.err = err
		return
	}
	if h.query, err = tswasm.NewQuery(source); err != nil {
		h.err = fmt.Errorf("compile highlight query: %w", err)
		return
	}
	// The injection query is best-effort: a missing or unparseable one just
	// skips injections, so a language-tagged template stays highlighted as
	// a plain Dang string.
	if source, err := loadDangQuery("injections.scm", "DANG_INJECTION_QUERY", "dang-injections.scm"); err == nil {
		if q, err := tswasm.NewQuery(source); err == nil {
			h.injections = q
		}
	}
}

// loadDangQuery finds one of the Dang queries the same way
// build-highlight-assets.sh does: the in-repo copy under treesitter/queries
// (a symlink into the editors/zed submodule) when it's checked out,
// otherwise the copy the build script fetched into docs/js/ as jsName. The
// env variable overrides both.
func loadDangQuery(name, env, jsName string) (string, error) {
	var candidates []string
	if p := os.Getenv(env); p != "" {
		candidates = append(candidates, p)
	}
	if _, src, _, ok := runtime.Caller(0); ok {
		root := filepath.Dir(filepath.Dir(filepath.Dir(src)))
		candidates = append(candidates,
			filepath.Join(root, "treesitter", "queries", name),
			filepath.Join(root, "docs", "js", jsName),
		)
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("%s not found (tried %s)", name, strings.Join(candidates, ", "))
}

// classifyDang assigns a class per byte: wider captures first, narrower (and
// later) captures override, mirroring how the editors resolve the same query.
// Language-tagged templates (```toml … ```) are then re-highlighted as their
// language.
func classifyDang(source string) []string {
	h := &dangHighlighter
	h.once.Do(compileDangQueries)
	if h.err != nil {
		return nil
	}
	spans, errBytes := captureDang(source, "", "")
	if errBytes > 0 {
		// The source didn't fully parse. Snippets on the site are often
		// declaration fragments (stdlib signatures); retry inside a
		// synthetic interface body and keep whichever parse recovered more.
		wrapped, wrappedErrBytes := captureDang(source, signaturePrefix, signatureSuffix)
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
	if h.injections != nil {
		applyInjections(source, classes)
	}
	return classes
}

type captureSpan struct {
	start, end int
	capture    string
}

// captureDang highlights source wrapped in prefix and suffix, returning its
// highlight spans, translated back to and clipped to source, and the number
// of source bytes covered by ERROR/MISSING nodes. A parse that fails counts
// every byte as an error.
func captureDang(source, prefix, suffix string) ([]captureSpan, int) {
	h := &dangHighlighter
	src := []byte(prefix + source + suffix)
	offset := len(prefix)
	ctx := context.Background()

	captures, err := h.query.Captures(ctx, src)
	if err != nil {
		return nil, len(source)
	}
	root, err := tswasm.Parse(ctx, src, tswasm.PruneErrorFree)
	if err != nil {
		return nil, len(source)
	}

	names := h.query.CaptureNames()
	var spans []captureSpan
	for _, c := range captures {
		start := int(c.StartByte) - offset
		end := int(c.EndByte) - offset
		if end <= 0 || start >= len(source) {
			continue
		}
		spans = append(spans, captureSpan{
			start:   max(start, 0),
			end:     min(end, len(source)),
			capture: names[c.Index],
		})
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end-spans[i].start > spans[j].end-spans[j].start
	})
	return spans, errorBytes(root, offset, offset+len(source))
}

// errorBytes sums the bytes of ERROR/MISSING nodes intersecting [start, end).
func errorBytes(node *tswasm.Node, start, end int) int {
	if node.Error || node.Missing {
		s, e := int(node.StartByte), int(node.EndByte)
		if e < start || s > end {
			return 0
		}
		// A zero-width MISSING node still poisons the parse it appears in.
		return max(min(e, end)-max(s, start), 1)
	}
	total := 0
	for _, child := range node.Children() {
		total += errorBytes(child, start, end)
	}
	return total
}

// applyInjections overwrites, in place, the class of every byte inside a
// language-tagged multi-line template (```toml … ```) with that language's
// own highlighting. It runs the injection query, and for each match whose
// @injection.language names a highlighted language, re-classifies each
// @injection.content region and copies the result over the region — which the
// base pass had colored as one Dang string. Interpolation regions (${…}) are
// not captured as content, so they stay Dang.
func applyInjections(source string, classes []string) {
	q := dangHighlighter.injections
	matches, err := q.Matches(context.Background(), []byte(source))
	if err != nil {
		return
	}
	names := q.CaptureNames()
	for _, match := range matches {
		var lang string
		var ranges, segments [][2]int
		for _, c := range match.Captures {
			switch names[c.Index] {
			case "injection.language":
				lang = source[c.StartByte:c.EndByte]
			case "injection.content":
				ranges = append(ranges, [2]int{int(c.StartByte), int(c.EndByte)})
			}
		}
		if lang == "" {
			continue
		}
		if _, ok := languageAliases[strings.ToLower(lang)]; !ok {
			continue // not highlighted; leave it as a Dang string
		}

		// The query captures content as many tiny parts (one per grammar token)
		// and omits ${…} interpolation. Merge contiguous parts into segments, so
		// each embedded run is highlighted as a whole rather than a char at a
		// time; the interpolation gaps between segments stay Dang.
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
