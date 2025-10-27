package lsp

import (
	"log/slog"

	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
	"github.com/vito/dang/pkg/dang"
)

func (h *langHandler) handleTextDocumentRename(ctx *common.LSPContext, params *lsp.RenameParams) (*lsp.WorkspaceEdit, error) {
	slog.InfoContext(ctx.Context, "rename request", "uri", params.TextDocument.URI, "position", params.Position, "newName", params.NewName)

	f, ok := h.files[params.TextDocument.URI]
	if !ok {
		slog.WarnContext(ctx.Context, "file not found for rename", "uri", params.TextDocument.URI)
		return nil, nil
	}

	if f.AST == nil {
		slog.WarnContext(ctx.Context, "AST is nil for file", "uri", params.TextDocument.URI)
		return nil, nil
	}

	slog.InfoContext(ctx.Context, "file info", "hasAST", f.AST != nil, "textLength", len(f.Text))

	// Find the symbol at the cursor position
	symbolName := h.symbolAtPosition(f, params.Position)
	if symbolName == "" {
		slog.WarnContext(ctx.Context, "no symbol found at position", "position", params.Position)
		return nil, nil
	}

	slog.InfoContext(ctx.Context, "renaming symbol", "symbol", symbolName, "newName", params.NewName)

	// Collect all text edits for renaming this symbol
	var edits []lsp.TextEdit

	defer func() {
		slog.InfoContext(ctx.Context, "DEFERRED returning workspace edit", "totalEdits", len(edits))
	}()

	// Use precise reference finding with Symbol nodes
	slog.InfoContext(ctx.Context, "about to find references")
	references := h.findPreciseReferences(f, symbolName)
	slog.InfoContext(ctx.Context, "found references", "count", len(references), "references", references)
	for _, refRange := range references {
		edits = append(edits, lsp.TextEdit{
			Range:   &refRange,
			NewText: params.NewName,
		})
	}

	// Also find and rename declarations
	declarations := h.findDeclarations(f.AST, symbolName)
	slog.InfoContext(ctx.Context, "found declarations", "count", len(declarations), "declarations", declarations)
	for _, declRange := range declarations {
		edits = append(edits, lsp.TextEdit{
			Range:   &declRange,
			NewText: params.NewName,
		})
	}

	slog.InfoContext(ctx.Context, "returning workspace edit", "totalEdits", len(edits), "edits", edits)

	// Return WorkspaceEdit with changes for this file
	changes := map[lsp.DocumentURI][]lsp.TextEdit{
		params.TextDocument.URI: edits,
	}

	return &lsp.WorkspaceEdit{
		Changes: changes,
	}, nil
}

// findDeclarations finds all declaration nodes for a symbol
func (h *langHandler) findDeclarations(node dang.Node, symbolName string) []lsp.Range {
	var declarations []lsp.Range

	if node == nil {
		return declarations
	}

	// Walk the tree and collect all declarations
	node.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}

		// Check if this node declares the symbol
		declaredSyms := n.DeclaredSymbols()
		for _, declSym := range declaredSyms {
			if declSym == symbolName {
				var loc *dang.SourceLocation
				// Special handling for SlotDecl: use the Name symbol's location
				if slotDecl, ok := n.(*dang.SlotDecl); ok {
					loc = slotDecl.Name.GetSourceLocation()
				} else {
					loc = n.GetSourceLocation()
				}
				
				if loc != nil {
					declarations = append(declarations, lsp.Range{
						Start: lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1)},
						End:   lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1 + len(symbolName))},
					})
				}
			}
		}

		return true // Continue walking children
	})

	return declarations
}

// Helper to find symbol nodes in the AST
func (h *langHandler) findSymbolNodes(node dang.Node, symbolName string) []*dang.Symbol {
	var symbols []*dang.Symbol

	if node == nil {
		return symbols
	}

	// Walk the tree and collect all matching symbols
	node.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}

		// Check if this node is a symbol matching our name
		if sym, ok := n.(*dang.Symbol); ok {
			if sym.Name == symbolName {
				symbols = append(symbols, sym)
			}
		}

		return true // Continue walking children
	})

	return symbols
}

// More precise reference finding using symbol nodes
func (h *langHandler) findPreciseReferences(f *File, symbolName string) []lsp.Range {
	var references []lsp.Range

	if f.AST == nil {
		return references
	}

	// Find all symbol nodes that match the symbol name
	symbols := h.findSymbolNodes(f.AST, symbolName)

	for _, sym := range symbols {
		loc := sym.GetSourceLocation()
		if loc != nil {
			references = append(references, lsp.Range{
				Start: lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1)},
				End:   lsp.Position{Line: lsp.UInteger(loc.Line - 1), Character: lsp.UInteger(loc.Column - 1 + len(symbolName))},
			})
		}
	}

	return references
}
