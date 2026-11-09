package lsp

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleTextDocumentDefinition(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params DocumentDefinitionParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	f := h.waitForFile(params.TextDocument.URI)
	if f == nil {
		return nil, nil
	}

	// Resolve the name through the scopes around it, so a local shadowing an
	// outer binding goes to the local.
	if sym := inspect(f, params.Position); sym != nil {
		if sym.Definition != nil {
			if loc, ok := lspLocation(params.TextDocument.URI, *sym.Definition); ok {
				return loc, nil
			}
		} else if sym.Member != nil {
			// A builtin, an import, a schema field: nothing to go to.
			return nil, nil
		}
	}

	// Find the symbol at the cursor position
	symbolName := h.symbolAtPosition(f, params.Position)
	if symbolName == "" {
		return nil, nil
	}

	// Fall back to looking the name up, e.g. for a type named in a type
	// annotation, which isn't a reference analysis resolves.
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
