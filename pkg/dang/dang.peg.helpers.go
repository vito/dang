package dang

import (
	"bytes"
)

func (c current) Loc() *SourceLocation {
	fn, _ := c.globalStore["filePath"].(string)
	textEnd := len(c.text)
	if ln := bytes.IndexByte(c.text, '\n'); ln != -1 {
		textEnd = ln
	}
	return &SourceLocation{
		Filename: fn,
		Line:     c.pos.line,
		Column:   c.pos.col,
		Length:   len(string(c.text[:textEnd])),
	}
}

func normalizeDocString(content []byte) (res string) {
	content = bytes.TrimSpace(content)
	lines := bytes.Split(content, []byte{'\n'})
	if len(lines) == 0 {
		return string(content)
	}

	// Determine indentation level from first non-empty line
	var indentLevel int
	for _, line := range lines {
		if len(line) > 0 {
			for i, b := range line {
				if b != ' ' && b != '\t' {
					indentLevel = i
					break
				}
			}
			break
		}
	}

	// Trim indentation from all lines
	var trimmedLines [][]byte
	for _, line := range lines {
		if len(line) >= indentLevel {
			trimmedLines = append(trimmedLines, line[indentLevel:])
		} else {
			trimmedLines = append(trimmedLines, line)
		}
	}

	// Un-word-wrap paragraphs
	var result [][]byte
	var currentParagraph []byte

	for _, line := range trimmedLines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			// Empty line ends current paragraph
			if len(currentParagraph) > 0 {
				result = append(result, currentParagraph)
				currentParagraph = nil
			}
			result = append(result, []byte{})
		} else {
			// Add to current paragraph
			if len(currentParagraph) > 0 {
				currentParagraph = append(currentParagraph, ' ')
			}
			currentParagraph = append(currentParagraph, trimmed...)
		}
	}

	// Add final paragraph if exists
	if len(currentParagraph) > 0 {
		result = append(result, currentParagraph)
	}

	return string(bytes.Join(result, []byte{'\n'}))
}

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
