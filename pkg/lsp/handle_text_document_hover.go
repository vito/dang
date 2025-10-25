package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/vito/dang/pkg/dang"
)

func (h *langHandler) handleTextDocumentHover(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	var params HoverParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	f, ok := h.files[params.TextDocument.URI]
	if !ok {
		return nil, nil
	}

	if f.AST == nil {
		return nil, nil
	}

	// Find the symbol at the cursor position
	symbolName := h.symbolAtPosition(f, params.Position)
	if symbolName == "" {
		return nil, nil
	}

	slog.InfoContext(ctx, "hover request", "uri", params.TextDocument.URI, "position", params.Position, "symbol", symbolName)

	// Find the node at this position to get its inferred type
	node := h.findNodeAtPosition(f.AST, params.Position)
	if node == nil {
		slog.InfoContext(ctx, "no node found at position")
		return nil, nil
	}

	// Get the inferred type from the node
	var typeInfo string
	inferredType := node.GetInferredType()
	if inferredType != nil {
		typeInfo = fmt.Sprintf("%v", inferredType)
	}

	// If we couldn't get type info from the node, try to find it in the symbol table
	if typeInfo == "" {
		if f.Symbols != nil {
			if def, ok := f.Symbols.Definitions[symbolName]; ok {
				// We have a definition but no type info yet
				typeInfo = fmt.Sprintf("(symbol: %s)", def.Name)
			}
		}
	}

	if typeInfo == "" {
		return nil, nil
	}

	slog.InfoContext(ctx, "hover result", "symbol", symbolName, "type", typeInfo)

	// Try to get documentation from the symbol definition
	var docString string
	if f.Symbols != nil {
		if def, ok := f.Symbols.Definitions[symbolName]; ok && def.Node != nil {
			// Check if the node has documentation
			if slotDecl, ok := def.Node.(*dang.SlotDecl); ok {
				docString = slotDecl.DocString
			} else if classDecl, ok := def.Node.(*dang.ClassDecl); ok {
				docString = classDecl.DocString
			} else if directiveDecl, ok := def.Node.(*dang.DirectiveDecl); ok {
				docString = directiveDecl.DocString
			}
		}
	}

	// Build the hover content
	var content string
	if docString != "" {
		content = fmt.Sprintf("%s\n\n```dang\n%s: %s\n```", docString, symbolName, typeInfo)
	} else {
		content = fmt.Sprintf("```dang\n%s: %s\n```", symbolName, typeInfo)
	}

	// Return hover information with the type
	return &Hover{
		Contents: MarkupContent{
			Kind:  Markdown,
			Value: content,
		},
	}, nil
}

// findNodeAtPosition finds the most specific AST node at the given position
func (h *langHandler) findNodeAtPosition(root dang.Node, pos Position) dang.Node {
	var result dang.Node

	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}

		loc := n.GetSourceLocation()
		if loc == nil {
			return true // Continue walking
		}

		// Convert to 0-based for comparison
		startLine := loc.Line - 1
		startCol := loc.Column - 1
		endLine := startLine
		endCol := startCol + loc.Length

		if loc.End != nil {
			endLine = loc.End.Line - 1
			endCol = loc.End.Column - 1
		}

		// Check if position is within this node's range
		if (pos.Line > startLine || (pos.Line == startLine && pos.Character >= startCol)) &&
			(pos.Line < endLine || (pos.Line == endLine && pos.Character <= endCol)) {
			// This node contains the position, keep it as a candidate
			// Continue walking to find more specific children
			result = n
			return true
		}

		return true
	})

	return result
}
