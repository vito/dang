package lsp

import (
	"context"
	"strings"
)

func (h *langHandler) handleTextDocumentDefinition(ctx context.Context, params DocumentDefinitionParams) (any, error) {
	f := h.waitForFile(params.TextDocument.URI)
	if f == nil {
		return nil, nil
	}

	// Find the symbol at the cursor position
	symbolName := h.symbolAtPosition(f, params.Position)
	if symbolName == "" {
		return nil, nil
	}

	// Look up the definition
	if def, ok := f.Symbols.Definitions[symbolName]; ok {
		return def.Location, nil
	}

	return nil, nil
}

func (h *langHandler) symbolAtPosition(f *File, pos Position) string {
	lines := strings.Split(f.Text, "\n")
	if pos.Line >= len(lines) {
		return ""
	}

	line := lines[pos.Line]
	if pos.Character >= len(line) {
		return ""
	}

	// Find word boundaries around the cursor
	start := pos.Character
	for start > 0 && isIdentifierChar(rune(line[start-1])) {
		start--
	}

	end := pos.Character
	for end < len(line) && isIdentifierChar(rune(line[end])) {
		end++
	}

	if start == end {
		return ""
	}

	return line[start:end]
}
