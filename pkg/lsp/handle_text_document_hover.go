package lsp

import (
	"fmt"
	"log/slog"

	"github.com/newstack-cloud/ls-builder/common"
	lsp "github.com/newstack-cloud/ls-builder/lsp_3_17"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

func (h *langHandler) handleTextDocumentHover(ctx *common.LSPContext, params *lsp.HoverParams) (*lsp.Hover, error) {
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

	slog.InfoContext(ctx.Context, "hover request", "uri", params.TextDocument.URI, "position", params.Position, "symbol", symbolName)

	// Find the node at this position to get its inferred type
	node := h.findNodeAtPosition(f.AST, params.Position)
	if node == nil {
		slog.InfoContext(ctx.Context, "no node found at position")
		return nil, nil
	}

	// Check if we're hovering over a field access (Select node)
	var docString string
	var typeInfo string

	if selectNode, ok := node.(*dang.Select); ok {
		// Get the receiver's type
		receiverType := selectNode.Receiver.GetInferredType()
		if receiverType != nil {
			// Unwrap NonNullType if needed
			if nn, ok := receiverType.(hm.NonNullType); ok {
				receiverType = nn.Type
			}

			// Cast as an Env to look up the field's doc
			if env, ok := receiverType.(dang.Env); ok {
				if doc, found := env.GetDocString(selectNode.Field); found {
					docString = doc
				}

				// Get the field's type
				if scheme, found := env.LocalSchemeOf(selectNode.Field); found {
					fieldType, _ := scheme.Type()
					typeInfo = fmt.Sprintf("%v", fieldType)
				}
			}
		}
	}

	// Get the inferred type from the node if we don't have it yet
	if typeInfo == "" {
		inferredType := node.GetInferredType()
		if inferredType != nil {
			typeInfo = fmt.Sprintf("%v", inferredType)
		}
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

	slog.InfoContext(ctx.Context, "hover result", "symbol", symbolName, "type", typeInfo)

	// Try to get documentation from the type environment (if we don't have it yet)
	if docString == "" {
		// First try the file's type environment
		if f.TypeEnv != nil {
			if doc, ok := f.TypeEnv.GetDocString(symbolName); ok {
				docString = doc
			}
		}

		// If not found at file level, try to find in lexical scopes
		if docString == "" {
			docString = h.findDocInLexicalScope(ctx, f.AST, params.Position, symbolName)
		}
	}

	// Build hover content
	var contents string
	contents = fmt.Sprintf("```dang\n%s\n```", typeInfo)
	if docString != "" {
		contents += fmt.Sprintf("\n\n%s", docString)
	}

	return &lsp.Hover{
		Contents: lsp.MarkupContent{
			Kind:  lsp.MarkupKindMarkdown,
			Value: contents,
		},
	}, nil
}

func (h *langHandler) findNodeAtPosition(root dang.Node, pos lsp.Position) dang.Node {
	var result dang.Node

	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}

		// Check if position is within this node's range
		if positionWithinNode(n, pos) {
			// This node contains the position, keep it as a candidate
			// Continue walking to find more specific children
			result = n
			return true
		}

		return true
	})

	return result
}

// findDocInLexicalScope searches for documentation in any enclosing lexical scope
func (h *langHandler) findDocInLexicalScope(ctx *common.LSPContext, root dang.Node, pos lsp.Position, symbolName string) string {
	// Collect all enclosing environments
	environments := findEnclosingEnvironments(root, pos)

	// Search environments from innermost to outermost
	// (reverse order since we collected from outermost to innermost)
	for i := len(environments) - 1; i >= 0; i-- {
		if doc, ok := environments[i].GetDocString(symbolName); ok {
			return doc
		}
	}

	return ""
}
