package dang

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A Pitfall is a common mistake Dang recognizes, usually a habit carried over
// from another language: `//` comments, `&&`, `for` loops, truthiness. When an
// error is one of these, it carries the Pitfall, whose Hint and Docs are shown
// beside the diagnostic (see SourceError.FormatWithHighlighting and
// analysis.Diagnostic).
//
// A pitfall only ever adds guidance. On a parse error it replaces the
// parser's own message, which can't say what was meant; on a type error the
// message stays and the hint is added.
//
// Detection must be precise: a hint on the wrong code misleads, which is
// worse than no hint. Every pitfall has positive and negative cases in
// pitfalls_test.go (pkg/dang/analysis).
type Pitfall struct {
	// ID names the pitfall, e.g. "line-comment". Diagnostics carry it as
	// their Pitfall (and the LSP as their code).
	ID string

	// Hint is one line of guidance: what to write instead.
	Hint string

	// Docs links the section of the docs that covers it.
	Docs string

	// syntax detects the pitfall at a parse failure: src is the whole file,
	// parsed as Dang v, and at the byte offset the parser failed at. It
	// returns the message that replaces the parser's and the span of src to
	// report.
	syntax func(src []byte, at int, v Version) (msg string, start, end int, ok bool)

	// typeErr detects the pitfall in a type error: err is the error an
	// InferError wraps, node the node it is on.
	typeErr func(err error, node SourceLocatable) bool
}

// docsURL is the URL of a section of the docs site: tag is the {#tag} of a
// heading in docs/lit, on the page tagged page.
func docsURL(page, tag string) string {
	return "https://danglang.org/" + page + "#" + tag
}

var (
	// PitfallLineComment is `//` written as a comment.
	PitfallLineComment = &Pitfall{
		ID:     "line-comment",
		Hint:   "comments start with `#`",
		Docs:   docsURL("syntax", "comments"),
		syntax: detectLineComment,
	}

	// PitfallLogicalOperators is `&&` or `||` for `and` or `or`.
	PitfallLogicalOperators = &Pitfall{
		ID:      "logical-operators",
		Hint:    "the logical operators are the keywords `and` and `or` (and `!` to negate)",
		Docs:    docsURL("operators", "logical"),
		syntax:  detectLogicalOperator,
		typeErr: isDoubledFunctionRef,
	}

	// PitfallLoopKeywords is a `for` or `while` loop.
	PitfallLoopKeywords = &Pitfall{
		ID:      "loop-keywords",
		Hint:    "iterate with `xs.each { x => ... }`, or repeat with `loop { ... }` and `break`",
		Docs:    docsURL("control-flow", "loop"),
		syntax:  detectLoopKeyword,
		typeErr: isLoopKeywordNotFound,
	}

	// PitfallSingleBraceRecord is a record literal written with single
	// braces, which make a block.
	PitfallSingleBraceRecord = &Pitfall{
		ID:     "single-brace-record",
		Hint:   "record literals use double braces: `{{ name: value }}`; `{ ... }` is a block",
		Docs:   docsURL("syntax", "record-literals"),
		syntax: detectSingleBraceRecord,
	}

	// PitfallTruthiness is a non-Boolean used as a condition or a logical
	// operand.
	PitfallTruthiness = &Pitfall{
		ID:      "truthiness",
		Hint:    "there is no truthiness: test explicitly, e.g. `x != null` or `!xs.isEmpty`",
		Docs:    docsURL("control-flow", "if-else"),
		typeErr: isTruthiness,
	}
)

// Pitfalls are all the pitfalls Dang recognizes. A parse failure is checked
// against each in order, and the first to match wins.
var Pitfalls = []*Pitfall{
	PitfallLineComment,
	PitfallLogicalOperators,
	PitfallLoopKeywords,
	PitfallSingleBraceRecord,
	PitfallTruthiness,
}

// PitfallOf returns the pitfall an error is, or nil.
func PitfallOf(err error) *Pitfall {
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) && sourceErr.Pitfall != nil {
		return sourceErr.Pitfall
	}
	var inferErr *InferError
	if errors.As(err, &inferErr) {
		return inferErr.Pitfall
	}
	return nil
}

// typePitfall returns the pitfall a type error is, or nil.
func typePitfall(err error, node SourceLocatable) *Pitfall {
	for _, p := range Pitfalls {
		if p.typeErr != nil && p.typeErr(err, node) {
			return p
		}
	}
	return nil
}

// syntaxPitfallError returns the error to report for a parse failure of
// source, parsed as Dang v, that is a pitfall, if it is one.
func syntaxPitfallError(pegErr error, filename string, source []byte, v Version) (*SourceError, bool) {
	at, ok := parseFailureOffset(pegErr)
	if !ok || at > len(source) {
		return nil, false
	}
	for _, p := range Pitfalls {
		if p.syntax == nil {
			continue
		}
		msg, start, end, ok := p.syntax(source, at, v.orCurrent())
		if !ok {
			continue
		}
		loc := spanLocation(filename, source, start, end)
		err := NewSourceError(fmt.Errorf("syntax error: %s", msg), loc, string(source))
		err.Pitfall = p
		return err, true
	}
	return nil, false
}

// optionsVersion is the Dang version parser options select.
func optionsVersion(opts []Option) Version {
	return versionFromStore(newParser("", nil, opts...).cur.globalStore)
}

// spanLocation is the location of the bytes [start, end) of source, which
// must be on one line. Columns count runes, as the parser's do.
func spanLocation(filename string, source []byte, start, end int) *SourceLocation {
	lineStart := bytes.LastIndexByte(source[:start], '\n') + 1
	line := bytes.Count(source[:start], []byte("\n")) + 1
	col := utf8.RuneCount(source[lineStart:start]) + 1
	length := max(1, utf8.RuneCount(source[start:end]))
	return &SourceLocation{
		Filename: filename,
		Line:     line,
		Column:   col,
		Length:   length,
		End:      &SourcePosition{Line: line, Column: col + length},
	}
}

// codeMask reports, for each byte of src, whether it is code: not in a
// comment or the text of a string or template literal. The expressions a
// literal interpolates (`${...}`) are code. It's a lexer good enough to tell a
// `//` in code from one in a URL in a string, nothing more.
func codeMask(src []byte) []bool {
	m := &masker{src: src, code: make([]bool, len(src))}
	m.scanCode(0, false)
	return m.code
}

// masker is codeMask's lexer.
type masker struct {
	src  []byte
	code []bool
}

// scanCode marks code from i on, to the end of src or, in an interpolation,
// past the `}` that closes it. It returns where it stopped.
func (m *masker) scanCode(i int, interp bool) int {
	src := m.src
	depth := 0
	for i < len(src) {
		switch {
		case src[i] == '#':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case bytes.HasPrefix(src[i:], []byte(`"""`)):
			i = m.scanText(i+3, `"""`, true, true, false)
		case src[i] == '"':
			i = m.scanText(i+1, `"`, true, true, true)
		case bytes.HasPrefix(src[i:], []byte(`'''`)):
			i = m.scanText(i+3, `'''`, false, false, false)
		case src[i] == '\'':
			i = m.scanText(i+1, `'`, false, false, true)
		case src[i] == '`':
			fence := 1
			for i+fence < len(src) && src[i+fence] == '`' {
				fence++
			}
			if fence == 2 {
				// An empty single-line template.
				i += 2
				continue
			}
			i = m.scanTemplate(i+fence, fence)
		default:
			if interp {
				switch src[i] {
				case '{':
					depth++
				case '}':
					if depth == 0 {
						return i + 1
					}
					depth--
				}
			}
			m.code[i] = true
			i++
		}
	}
	return i
}

// scanText skips a quoted string's text from i to past its closing quote:
// escapes are backslashes, interpolations `${...}`, and a single-line string
// ends at a newline. It returns where the string ends.
func (m *masker) scanText(i int, closing string, escapes, interpolates, singleLine bool) int {
	src := m.src
	for i < len(src) {
		switch {
		case escapes && src[i] == '\\':
			i += 2
		case interpolates && bytes.HasPrefix(src[i:], []byte("${")):
			i = m.scanCode(i+2, true)
		case bytes.HasPrefix(src[i:], []byte(closing)):
			return i + len(closing)
		case singleLine && src[i] == '\n':
			return i
		default:
			i++
		}
	}
	return i
}

// scanTemplate skips a backtick template's text from i to past its closing
// fence: its only escape is `\${`, and a single-backtick template ends at a
// newline.
func (m *masker) scanTemplate(i, fence int) int {
	src := m.src
	closing := bytes.Repeat([]byte("`"), fence)
	for i < len(src) {
		switch {
		case bytes.HasPrefix(src[i:], []byte(`\${`)):
			i += 3
		case bytes.HasPrefix(src[i:], []byte("${")):
			i = m.scanCode(i+2, true)
		case bytes.HasPrefix(src[i:], closing):
			return i + fence
		case fence == 1 && src[i] == '\n':
			return i
		default:
			i++
		}
	}
	return i
}

// isCodeAt reports whether the bytes of src at i are code (see codeMask)
// and begin with s.
func isCodeAt(src []byte, code []bool, i int, s string) bool {
	return i >= 0 && i < len(code) && code[i] && bytes.HasPrefix(src[i:], []byte(s))
}

// lineBounds returns the start and end offsets of the line of src at i,
// without its newline.
func lineBounds(src []byte, i int) (int, int) {
	start := bytes.LastIndexByte(src[:i], '\n') + 1
	end := len(src)
	if nl := bytes.IndexByte(src[i:], '\n'); nl >= 0 {
		end = i + nl
	}
	return start, end
}

// detectLineComment matches a parse failure at a `//`: the parser fails on
// its first slash at the start of an expression, and on its second after an
// operand (on an earlier line, too). After an operand on its own line, a `//`
// followed by what reads as an expression may be Python's floor division
// instead (`n // 2`, `total // count`), which this leaves alone.
func detectLineComment(src []byte, at int, _ Version) (string, int, int, bool) {
	code := codeMask(src)
	for _, start := range []int{at, at - 1} {
		if !isCodeAt(src, code, start, "//") {
			continue
		}
		lineStart, end := lineBounds(src, start)
		end = start + len(bytes.TrimRight(src[start:end], " \t\r"))
		startsLine := len(bytes.TrimSpace(src[lineStart:start])) == 0
		if start == at-1 && !startsLine {
			rest := src[start+2 : end]
			// What follows a floor division can have a comment of its own.
			for i := start + 2; i < end; i++ {
				if src[i] == '#' && !code[i] {
					rest = src[start+2 : i]
					break
				}
			}
			if looksLikeExpression(rest, func(name string) bool {
				return namedElsewhere(src, code, name, lineStart, end)
			}) {
				return "", 0, 0, false
			}
		}
		return "`//` is not a comment", start, end, true
	}
	return "", 0, 0, false
}

var (
	operandTokenRe  = regexp.MustCompile(`^(?:[0-9][0-9.]*|[A-Za-z_][A-Za-z0-9_.]*)`)
	operatorTokenRe = regexp.MustCompile(`^(?:==|!=|<=|>=|\*\*|//|[-+*/%<>()\[\],])`)
	wordRe          = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// looksLikeExpression reports whether text reads as the rest of an arithmetic
// expression rather than prose: a number, a name the source names elsewhere
// (named reports which), or operands and operators in turn (`len(xs) + 1`).
// A lone word that's nowhere else (`TODO`) reads as prose.
func looksLikeExpression(text []byte, named func(string) bool) bool {
	s := strings.TrimSpace(string(text))
	operands := 0
	var lone string
	lastWasOperand := false
	for s != "" {
		if m := operandTokenRe.FindString(s); m != "" {
			if lastWasOperand {
				// Two operands in a row: prose.
				return false
			}
			operands++
			lone = m
			lastWasOperand = true
			s = strings.TrimSpace(s[len(m):])
			continue
		}
		if m := operatorTokenRe.FindString(s); m != "" {
			lastWasOperand = m == ")" || m == "]"
			s = strings.TrimSpace(s[len(m):])
			continue
		}
		return false
	}
	if operands != 1 {
		return operands > 1
	}
	if !isIdentStart(lone[0]) {
		return true
	}
	root, _, _ := strings.Cut(lone, ".")
	return named(root)
}

// namedElsewhere reports whether name appears as a word in the code of src
// outside [from, to).
func namedElsewhere(src []byte, code []bool, name string, from, to int) bool {
	for _, m := range wordRe.FindAllIndex(src, -1) {
		if m[0] >= from && m[0] < to || !code[m[0]] {
			continue
		}
		if string(src[m[0]:m[1]]) == name {
			return true
		}
	}
	return false
}

// detectLogicalOperator matches a parse failure at `&&` or `||`.
func detectLogicalOperator(src []byte, at int, _ Version) (string, int, int, bool) {
	code := codeMask(src)
	for _, op := range []string{"&&", "||"} {
		if isCodeAt(src, code, at, op) {
			return fmt.Sprintf("`%s` is not an operator", op), at, at + 2, true
		}
	}
	return "", 0, 0, false
}

// isDoubledFunctionRef matches `&&x` where x is no function: `&&` that
// parsed, as a reference to a reference, because it starts a line (`a\n &&
// b`).
func isDoubledFunctionRef(err error, node SourceLocatable) bool {
	ref, ok := node.(*FunctionRef)
	if !ok {
		return false
	}
	_, doubled := ref.Expr.(*FunctionRef)
	var notFn *notAFunctionError
	return doubled && errors.As(err, &notFn)
}

var loopKeywordRe = regexp.MustCompile(`^[ \t]*(for|while)[ \t(]`)

// detectLoopKeyword matches a parse failure in the header of a `for` or
// `while` loop: on a line starting with the keyword, before its `{`. Before
// Dang 2.1, `for (cond) { ... }` is a loop, so it doesn't match there.
func detectLoopKeyword(src []byte, at int, v Version) (string, int, int, bool) {
	if at >= len(src) || v.forLoops() {
		return "", 0, 0, false
	}
	lineStart, lineEnd := lineBounds(src, at)
	m := loopKeywordRe.FindSubmatchIndex(src[lineStart:lineEnd])
	if m == nil {
		return "", 0, 0, false
	}
	start, end := lineStart+m[2], lineStart+m[3]
	code := codeMask(src)
	if !code[start] || at < end {
		return "", 0, 0, false
	}
	if brace := bytes.IndexByte(src[end:lineEnd], '{'); brace >= 0 && at >= end+brace {
		return "", 0, 0, false
	}
	return fmt.Sprintf("Dang has no `%s` loop", src[start:end]), start, end, true
}

// isLoopKeywordNotFound matches `for` or `while` that parsed as a call (a
// name followed by parentheses and a block) but names nothing.
func isLoopKeywordNotFound(err error, node SourceLocatable) bool {
	var notFound *nameNotFoundError
	return errors.As(err, &notFound) && (notFound.Name == "for" || notFound.Name == "while")
}

// detectSingleBraceRecord matches a parse failure at the value of a
// `{ name: value` that starts an expression: a record literal with single
// braces, which the parser reads as a block starting with a declaration.
func detectSingleBraceRecord(src []byte, at int, _ Version) (string, int, int, bool) {
	code := codeMask(src)
	// A value starting with a letter (`true`, `name`) fails a letter in,
	// past what parsed as a type variable. Unless it's followed by `=`:
	// then it's a declaration with a misspelled type (`count: int = 1`).
	at = wordStart(src, at)
	if at < len(src) && isIdentStart(src[at]) && declaresSlot(src[at:]) {
		return "", 0, 0, false
	}
	i := skipSpaceBack(src, at-1)
	if !isCodeAt(src, code, i, ":") {
		return "", 0, 0, false
	}
	nameEnd := skipSpaceBack(src, i-1) + 1
	nameStart := nameEnd
	for nameStart > 0 && isIdentByte(src[nameStart-1]) {
		nameStart--
	}
	if nameStart == nameEnd || !isIdentStart(src[nameStart]) || !code[nameStart] {
		return "", 0, 0, false
	}
	brace := skipSpaceBack(src, nameStart-1)
	if !isCodeAt(src, code, brace, "{") || (brace > 0 && src[brace-1] == '{') {
		return "", 0, 0, false
	}
	// Only where an expression starts: a block there can't begin with a
	// declaration whose type fails to parse, but a type body or function
	// body (after a name, `)` or a type) can.
	prev := skipSpaceBack(src, brace-1)
	if prev < 0 || !code[prev] || !strings.ContainsRune("=(,[:", rune(src[prev])) {
		return "", 0, 0, false
	}
	return "a record literal needs double braces", brace, brace + 1, true
}

// declaresSlotRe matches a type name and the `=` of a declaration after it.
var declaresSlotRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*[!?]*[ \t]*=($|[^=>])`)

// declaresSlot reports whether src starts with a word that reads as the type
// of a declaration: followed by `=`, not `==` or `=>`.
func declaresSlot(src []byte) bool {
	return declaresSlotRe.Match(src)
}

// skipSpaceBack returns the offset of the last non-whitespace byte of src at
// or before i, or -1.
func skipSpaceBack(src []byte, i int) int {
	for i >= 0 && i < len(src) && strings.ContainsRune(" \t\r\n", rune(src[i])) {
		i--
	}
	return i
}

// wordStart returns the offset of the start of the word src has at i, or i
// if it has none there.
func wordStart(src []byte, i int) int {
	if i >= len(src) || !isIdentByte(src[i]) {
		return i
	}
	for i > 0 && isIdentByte(src[i-1]) {
		i--
	}
	return i
}

func isIdentStart(b byte) bool {
	return b == '_' || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// isTruthiness matches a value of a type other than Boolean used as one. A
// nullable Boolean is a different mistake, with its own advice, and so is
// Decoded data, which needs a type.
func isTruthiness(err error, node SourceLocatable) bool {
	var notBool *notBooleanErr
	return errors.As(err, &notBool) && notBool.truthiness
}

// nameNotFoundError is the error for a name that is not in scope.
type nameNotFoundError struct {
	Name string
}

func (e *nameNotFoundError) Error() string {
	return fmt.Sprintf("%q not found", e.Name)
}
