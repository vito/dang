package lsp

import (
	"strings"

	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
)

func (h *langHandler) handleTextDocumentDefinition(ctx *common.LSPContext, params *lsp.DefinitionParams) (any, error) {
	f, ok := h.files[params.TextDocument.URI]
	if !ok {
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

func (h *langHandler) symbolAtPosition(f *File, pos lsp.Position) string {
	lines := strings.Split(f.Text, "\n")
	if int(pos.Line) >= len(lines) {
		return ""
	}

	line := lines[pos.Line]
	if int(pos.Character) >= len(line) {
		return ""
	}

	// Find word boundaries around the cursor
	start := int(pos.Character)
	for start > 0 && isIdentifierChar(rune(line[start-1])) {
		start--
	}

	end := int(pos.Character)
	for end < len(line) && isIdentifierChar(rune(line[end])) {
		end++
	}

	if start == end {
		return ""
	}

	return line[start:end]
}
