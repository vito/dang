package dang

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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

// unquoteString decodes a double-quoted string literal. It accepts every Go
// escape plus `\/` for `/` (as JSON does), which strconv.Unquote rejects.
func unquoteString(text []byte) (string, error) {
	if !bytes.Contains(text, []byte(`\/`)) {
		return strconv.Unquote(string(text))
	}
	// Rewrite only escapes that are `\/`: in `\\/` the backslash is escaped.
	buf := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) {
			i++
			if text[i] != '/' {
				buf = append(buf, '\\')
			}
		}
		buf = append(buf, text[i])
	}
	return strconv.Unquote(string(buf))
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
