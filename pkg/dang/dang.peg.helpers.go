package dang

import (
	"bytes"
	"regexp"
	"strconv"
)

// ParseWithComments parses source and returns the AST along with a map of
// line-number to comment text collected during parsing.
func ParseWithComments(filename string, source []byte) (any, map[int]string, error) {
	comments := make(map[int]string)
	result, err := Parse(filename, source, GlobalStore("comments", comments))
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
		Length:   len(string(c.text[:textEnd])),
	}
	lineCount := bytes.Count(c.text, []byte("\n"))
	var endLine = start.Line + lineCount
	var endCol int
	if lineCount == 0 {
		// Single line: end column is start column + length of text
		endCol = start.Column + len(c.text)
	} else {
		// Multi-line: end column is the length after the last newline + 1 (1-indexed)
		lastNewline := bytes.LastIndexByte(c.text, '\n')
		if lastNewline != -1 {
			endCol = len(c.text) - lastNewline // Already 1-indexed since we're counting from after the newline
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
