package lsp

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/vito/dang/pkg/dang"
)

func (h *langHandler) handleTextDocumentRename(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	var params RenameParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "rename request", "uri", params.TextDocument.URI, "position", params.Position, "newName", params.NewName)

	f, ok := h.files[params.TextDocument.URI]
	if !ok {
		slog.WarnContext(ctx, "file not found for rename", "uri", params.TextDocument.URI)
		return nil, nil
	}

	if f.AST == nil {
		slog.WarnContext(ctx, "AST is nil for file", "uri", params.TextDocument.URI)
		return nil, nil
	}

	slog.InfoContext(ctx, "file info", "hasAST", f.AST != nil, "textLength", len(f.Text))

	// Find the symbol at the cursor position
	symbolName := h.symbolAtPosition(f, params.Position)
	if symbolName == "" {
		slog.WarnContext(ctx, "no symbol found at position", "position", params.Position)
		return nil, nil
	}

	slog.InfoContext(ctx, "renaming symbol", "symbol", symbolName, "newName", params.NewName)

	// Collect all text edits for renaming this symbol
	var edits []TextEdit

	defer func() {
		slog.InfoContext(ctx, "DEFERRED returning workspace edit", "totalEdits", len(edits))
	}()

	// Use precise reference finding with Symbol nodes
	slog.InfoContext(ctx, "about to find references")
	references := h.findPreciseReferences(f, symbolName)
	slog.InfoContext(ctx, "found references", "count", len(references), "references", references)
	for _, refRange := range references {
		edits = append(edits, TextEdit{
			Range:   refRange,
			NewText: params.NewName,
		})
	}

	// Also find and rename declarations
	declarations := h.findDeclarations(f.AST, symbolName)
	slog.InfoContext(ctx, "found declarations", "count", len(declarations), "declarations", declarations)
	for _, declRange := range declarations {
		edits = append(edits, TextEdit{
			Range:   declRange,
			NewText: params.NewName,
		})
	}

	slog.InfoContext(ctx, "returning workspace edit", "totalEdits", len(edits), "edits", edits)

	// Return WorkspaceEdit with changes for this file
	changes := map[string][]TextEdit{
		string(params.TextDocument.URI): edits,
	}

	return &WorkspaceEdit{
		Changes: changes,
	}, nil
}

// findDeclarations finds all declaration nodes for a symbol
func (h *langHandler) findDeclarations(node dang.Node, symbolName string) []Range {
	var declarations []Range

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
					declarations = append(declarations, Range{
						Start: Position{Line: loc.Line - 1, Character: loc.Column - 1},
						End:   Position{Line: loc.Line - 1, Character: loc.Column - 1 + len(symbolName)},
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
func (h *langHandler) findPreciseReferences(f *File, symbolName string) []Range {
	var references []Range

	if f.AST == nil {
		return references
	}

	// Find all symbol nodes that match the symbol name
	symbols := h.findSymbolNodes(f.AST, symbolName)

	for _, sym := range symbols {
		loc := sym.GetSourceLocation()
		if loc != nil {
			references = append(references, Range{
				Start: Position{Line: loc.Line - 1, Character: loc.Column - 1},
				End:   Position{Line: loc.Line - 1, Character: loc.Column - 1 + len(symbolName)},
			})
		}
	}

	return references
}
