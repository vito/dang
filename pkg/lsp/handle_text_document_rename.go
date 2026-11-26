package lsp

import (
	"context"
	"log/slog"

	"github.com/creachadair/jrpc2"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func (h *langHandler) handleTextDocumentRename(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params RenameParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "rename request", "uri", params.TextDocument.URI, "position", params.Position, "newName", params.NewName)

	f := h.waitForFile(params.TextDocument.URI)
	if f == nil {
		slog.WarnContext(ctx, "file not found for rename", "uri", params.TextDocument.URI)
		return nil, nil
	}

	if f.AST == nil {
		slog.WarnContext(ctx, "AST is nil for file", "uri", params.TextDocument.URI)
		return nil, nil
	}

	slog.InfoContext(ctx, "file info", "hasAST", f.AST != nil, "textLength", len(f.Text))

	// Rename the declaration the name resolves to, and the references that
	// resolve to it, in every file of the directory: not other bindings of
	// the same name.
	if changes := h.referenceEdits(ctx, f, params.TextDocument.URI, params.Position, params.NewName); changes != nil {
		return &WorkspaceEdit{Changes: changes}, nil
	}

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

// referenceEdits returns the edits renaming what the name at pos names to
// newName, by URI, or nil if it isn't declared in the directory.
func (h *langHandler) referenceEdits(ctx context.Context, f *File, uri DocumentURI, pos Position, newName string) map[string][]TextEdit {
	locs := h.directoryReferences(ctx, f, uri, pos)
	if len(locs) == 0 {
		return nil
	}
	changes := map[string][]TextEdit{}
	for _, loc := range locs {
		changes[string(loc.URI)] = append(changes[string(loc.URI)], TextEdit{
			Range:   loc.Range,
			NewText: newName,
		})
	}
	return changes
}

// directoryReferences is like references, but resolves them against a full
// analysis of f's directory. f's own analysis infers only f's bodies, so it
// misses references that need inference to resolve in sibling files, such as
// a method called through an instance in another file's function body.
func (h *langHandler) directoryReferences(ctx context.Context, f *File, uri DocumentURI, pos Position) []Location {
	if f == nil || f.Analysis == nil {
		return nil
	}
	fp, err := fromURI(uri)
	if err != nil {
		return references(f.Analysis, f.Path, uri, pos)
	}
	full, err := h.analyze(ctx, fp, "")
	if err != nil {
		slog.WarnContext(ctx, "full directory analysis failed; using the file's own", "error", err)
		return references(f.Analysis, f.Path, uri, pos)
	}
	return references(full, f.Path, uri, pos)
}

// references returns the LSP locations of the declaration of what the name
// at pos in the file at path names and of its references, according to a
// (see analysis.References).
func references(a *analysis.Analysis, path string, uri DocumentURI, pos Position) []Location {
	if a == nil {
		return nil
	}
	var locs []Location
	for _, ref := range a.References(path, analysisPos(pos)) {
		if loc, ok := lspLocation(uri, ref); ok {
			locs = append(locs, loc)
		}
	}
	return locs
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
				// Special handling for FieldDecl: use the Name symbol's location
				if fieldDecl, ok := n.(*dang.FieldDecl); ok {
					loc = fieldDecl.Name.GetSourceLocation()
				} else {
					loc = n.GetSourceLocation()
				}

				if loc != nil {
					declarations = append(declarations, nameRange(loc, symbolName))
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
			references = append(references, nameRange(loc, symbolName))
		}
	}

	return references
}
