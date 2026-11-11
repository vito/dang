package dang

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseWithComments parses source and returns the AST along with a map of
// line-number to comment text collected during parsing.
func ParseWithComments(filename string, source []byte, opts ...Option) (any, map[int]string, error) {
	comments := make(map[int]string)
	result, err := Parse(filename, source, append(opts, GlobalStore("comments", comments))...)
	if err != nil {
		return nil, nil, err
	}
	return result, comments, nil
}

func (e errList) Unwrap() []error {
	return e
}

func (p *parserError) ParseErrorLocation() *SourceLocation {
	return &SourceLocation{
		Line:   p.pos.line,
		Column: p.pos.col + 1,
	}
}

// Unwrap exposes the error an action returned or panicked with, so errors.As
// finds a rejectedSyntax through the parser's error list.
func (p *parserError) Unwrap() error {
	return p.Inner
}

// rejectedSyntax is source the grammar matched but that is still invalid,
// like an integer too large for an Int or a second block parameter.
type rejectedSyntax struct {
	msg string
	loc *SourceLocation
}

func (e *rejectedSyntax) Error() string { return e.msg }

// reject aborts the parse with a rejectedSyntax at loc.
//
// It panics, which pigeon turns into a parse error, rather than returning the
// error from the action: pigeon rolls its error list back when a left-recursive
// rule enclosing the action backtracks, so a returned error can be lost and
// the parse "succeed" with a bogus value (and returning a nil node instead
// crashes the rule that uses it). Call it only for text no valid program
// contains, so stopping is always right.
func (c *current) reject(loc *SourceLocation, msg string) {
	panic(&rejectedSyntax{msg: msg, loc: loc})
}

// rejectSecondBlockParam rejects args declaring more than one block
// parameter, at the second; what names the declaration, as in "function can
// only have one block parameter".
func (c *current) rejectSecondBlockParam(args []*FieldDecl, what string) {
	seen := false
	for _, arg := range args {
		if !arg.IsBlockParam {
			continue
		}
		if seen {
			c.reject(arg.Loc, what+" can only have one block parameter")
		}
		seen = true
	}
}

// rejectedSourceError renders a rejectedSyntax from a parse error as a
// SourceError with its snippet. It takes precedence over tree-sitter's
// diagnosis, which sees nothing wrong with such source.
func rejectedSourceError(pegErr error, filename string, source []byte) (*SourceError, bool) {
	var rejected *rejectedSyntax
	if !errors.As(pegErr, &rejected) {
		return nil, false
	}
	loc := *rejected.loc
	if loc.Filename == "" {
		loc.Filename = filename
	}
	return NewSourceError(fmt.Errorf("syntax error: %s", rejected.msg), &loc, string(source)), true
}

// unquoteString decodes a double-quoted string literal, quotes included (see
// unescapeString).
func unquoteString(text []byte) (string, error) {
	if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
		return "", strconv.ErrSyntax
	}
	return unescapeString(string(text[1 : len(text)-1]))
}

// unescapeString decodes the escapes in raw, the text of a double-quoted
// string as written. It accepts every Go escape, plus `\/` for `/` (as JSON
// does) and `\$` for `$` (to write a literal `${`). Unescaped quotes and
// newlines are kept as they are: triple-quoted strings may hold them.
func unescapeString(raw string) (string, error) {
	if !strings.Contains(raw, `\`) {
		return raw, nil
	}
	var b strings.Builder
	for raw != "" {
		if raw[0] != '\\' {
			i := strings.IndexByte(raw, '\\')
			if i < 0 {
				i = len(raw)
			}
			b.WriteString(raw[:i])
			raw = raw[i:]
			continue
		}
		if len(raw) >= 2 && (raw[1] == '/' || raw[1] == '$') {
			b.WriteByte(raw[1])
			raw = raw[2:]
			continue
		}
		r, multibyte, tail, err := strconv.UnquoteChar(raw, '"')
		if err != nil {
			return "", err
		}
		if r < utf8.RuneSelf || !multibyte {
			b.WriteByte(byte(r))
		} else {
			b.WriteRune(r)
		}
		raw = tail
	}
	return b.String(), nil
}

// quotedString builds a double-quoted string literal (from Dang 2.5) from its
// parts, whose literal text is still as written: a multi-line string is
// dedented before its escapes are decoded, so that an escape like `\t` is
// text rather than indentation. It is a String unless it interpolates.
func (c *current) quotedString(rawParts any, quote byte, triple bool, lang any) (any, error) {
	parts := sliceOf[TemplatePart](rawParts)
	l, _ := lang.(string)
	fence := 1
	if triple {
		fence = 3
		if l == "" {
			c.rejectOpeningLineText(parts, `"""`)
		}
		parts = normalizeTemplateParts(parts)
	}
	parts = coalesceTemplateParts(parts)
	interpolates := false
	for i, p := range parts {
		if p.Expr != nil {
			interpolates = true
			continue
		}
		lit, err := unescapeString(p.Lit)
		if err != nil {
			// The grammar admits escapes that encode no character, like \777.
			c.reject(c.Loc(), "invalid escape sequence in string literal")
		}
		parts[i].Lit = lit
	}
	if !interpolates {
		var value strings.Builder
		for _, p := range parts {
			value.WriteString(p.Lit)
		}
		return &String{
			Value:        value.String(),
			TripleQuoted: triple,
			Quote:        quote,
			Lang:         l,
			Loc:          c.Loc(),
		}, nil
	}
	return &Template{
		Parts: parts,
		Fence: fence,
		Lang:  l,
		Quote: quote,
		Loc:   c.Loc(),
	}, nil
}

// verbatimTripleString builds a `'''...'''` string from its content as
// written, dedented.
func (c *current) verbatimTripleString(content, lang any) (any, error) {
	raw := ""
	if content != nil {
		raw = string(content.([]byte))
	}
	l, _ := lang.(string)
	if l == "" {
		c.rejectOpeningLineText([]TemplatePart{{Lit: raw}}, "'''")
	}
	return &String{
		Value:        normalizeTripleQuoteString([]byte(raw)),
		TripleQuoted: true,
		Quote:        '\'',
		Verbatim:     true,
		Lang:         l,
		Loc:          c.Loc(),
	}, nil
}

// rejectOpeningLineText rejects a multi-line string with text on its opening
// line, given the parts that follow its opening delimiter (written delim)
// when no language tag does. From Dang 2.5 a multi-line string's text starts
// on the next line, so that a word there can't be mistaken for a language tag
// or the other way around. A string that ends on its opening line is inline,
// and fine.
func (c *current) rejectOpeningLineText(parts []TemplatePart, delim string) {
	text := false
	for _, p := range parts {
		if p.Expr != nil {
			text = true
			continue
		}
		i := strings.IndexByte(p.Lit, '\n')
		if i < 0 {
			text = text || strings.TrimSpace(p.Lit) != ""
			continue
		}
		if text || strings.TrimSpace(p.Lit[:i]) != "" {
			c.reject(c.Loc(), fmt.Sprintf("text on the opening line of a multi-line string: start it on the next line (only a language tag may follow the opening %s, like %ssh)", delim, delim))
		}
		return
	}
}

func (c current) Loc() *SourceLocation {
	fn, _ := c.globalStore["filePath"].(string)
	textEnd := len(c.text)
	if ln := bytes.IndexByte(c.text, '\n'); ln != -1 {
		textEnd = ln
	}
	start := SourceLocation{
		Filename: fn,
		Line:     c.pos.line,
		Column:   c.pos.col,
		Length:   utf8.RuneCount(c.text[:textEnd]),
	}
	lineCount := bytes.Count(c.text, []byte("\n"))
	var endLine = start.Line + lineCount
	var endCol int
	if lineCount == 0 {
		// Match the parser's rune-based start column.
		endCol = start.Column + utf8.RuneCount(c.text)
	} else {
		// Multi-line: end column is the length after the last newline + 1 (1-indexed)
		lastNewline := bytes.LastIndexByte(c.text, '\n')
		if lastNewline != -1 {
			endCol = utf8.RuneCount(c.text[lastNewline+1:]) + 1
		} else {
			// Shouldn't happen since lineCount > 0, but fallback
			endCol = 1
		}
	}
	start.End = &SourcePosition{
		Line:   endLine,
		Column: endCol,
	}
	return &start
}

// TODO: unit test all this
func normalizeTripleQuoteString(content []byte) string {
	// Trim leading newlines only (preserve leading spaces for indentation detection)
	content = bytes.TrimLeft(content, "\n\r")

	// Trim trailing newlines and whitespace
	content = bytes.TrimRight(content, "\n\r \t")

	// Handle empty content
	if len(content) == 0 {
		return ""
	}

	lines := bytes.Split(content, []byte{'\n'})

	// Single line - trim it and return
	if len(lines) == 1 {
		return string(bytes.TrimSpace(content))
	}

	// Find MINIMUM indentation level from all non-empty lines
	minIndent := -1
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue // Skip empty lines when calculating indent
		}
		indent := 0
		for i, b := range line {
			if b != ' ' && b != '\t' {
				indent = i
				break
			}
		}
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent == -1 {
		minIndent = 0
	}

	// Trim minimum indentation from all lines
	var trimmedLines [][]byte
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			// Preserve empty lines as empty
			trimmedLines = append(trimmedLines, []byte{})
		} else if len(line) >= minIndent {
			// Strip minimum indent, then trim trailing whitespace
			dedented := line[minIndent:]
			trimmedLines = append(trimmedLines, bytes.TrimRight(dedented, " \t"))
		} else {
			// Line has less indentation than minimum - just trim trailing
			trimmedLines = append(trimmedLines, bytes.TrimRight(line, " \t"))
		}
	}

	return string(bytes.Join(trimmedLines, []byte{'\n'}))
}

func templateFenceKey(depth int) string {
	return "fence_" + strconv.Itoa(depth)
}

func makeTemplate(rawParts any, fence int, lang string, loc *SourceLocation) *Template {
	parts := sliceOf[TemplatePart](rawParts)
	if fence >= 3 {
		parts = normalizeTemplateParts(parts)
	}
	parts = coalesceTemplateParts(parts)
	return &Template{
		Parts: parts,
		Fence: fence,
		Lang:  lang,
		Loc:   loc,
	}
}

var templateMarkerRe = regexp.MustCompile("\x00(\\d+)\x00")

// normalizeTemplateParts applies the same dedent/trim rules as triple-quoted
// strings to template content, treating expression parts as opaque markers
// so their positions are preserved across the rewrite.
func normalizeTemplateParts(parts []TemplatePart) []TemplatePart {
	if len(parts) == 0 {
		return parts
	}
	var buf bytes.Buffer
	exprs := make([]Node, 0, len(parts))
	for _, p := range parts {
		if p.Expr != nil {
			buf.WriteByte(0)
			buf.WriteString(strconv.Itoa(len(exprs)))
			buf.WriteByte(0)
			exprs = append(exprs, p.Expr)
		} else {
			buf.WriteString(p.Lit)
		}
	}
	normalized := normalizeTripleQuoteString(buf.Bytes())
	var out []TemplatePart
	last := 0
	for _, m := range templateMarkerRe.FindAllStringSubmatchIndex(normalized, -1) {
		if m[0] > last {
			out = append(out, TemplatePart{Lit: normalized[last:m[0]]})
		}
		idx, _ := strconv.Atoi(normalized[m[2]:m[3]])
		out = append(out, TemplatePart{Expr: exprs[idx]})
		last = m[1]
	}
	if last < len(normalized) {
		out = append(out, TemplatePart{Lit: normalized[last:]})
	}
	return out
}

// coalesceTemplateParts merges runs of adjacent literal parts into a single
// part so downstream consumers (formatter, eval) see one chunk per literal
// region instead of per-character.
func coalesceTemplateParts(parts []TemplatePart) []TemplatePart {
	var out []TemplatePart
	for _, p := range parts {
		if p.Expr == nil && len(out) > 0 && out[len(out)-1].Expr == nil {
			out[len(out)-1].Lit += p.Lit
			continue
		}
		out = append(out, p)
	}
	return out
}
