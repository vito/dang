package lsp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/creachadair/jrpc2"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

func (h *langHandler) handleTextDocumentHover(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params HoverParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	f := h.waitForFile(params.TextDocument.URI)
	if f == nil {
		return nil, nil
	}

	if f.AST == nil {
		return nil, nil
	}

	// Find the node at this position to get its inferred type
	node := h.findNodeAtPosition(f.AST, params.Position)

	// Check if we're hovering over a directive application
	if directiveApp, ok := node.(*dang.DirectiveApplication); ok {
		return h.hoverDirectiveApplication(ctx, f, directiveApp, params.Position)
	}

	// Find the symbol at the cursor position
	symbolName := h.symbolAtPosition(f, params.Position)
	if symbolName == "" {
		return nil, nil
	}

	slog.InfoContext(ctx, "hover request", "uri", params.TextDocument.URI, "position", params.Position, "symbol", symbolName)

	if node == nil {
		slog.InfoContext(ctx, "no node found at position")
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

	slog.InfoContext(ctx, "hover result", "symbol", symbolName, "type", typeInfo)

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

// hoverDirectiveApplication returns hover info for a directive application by looking up its declaration.
func (h *langHandler) hoverDirectiveApplication(ctx context.Context, f *File, app *dang.DirectiveApplication, pos Position) (any, error) {
	// Find the directive declaration from enclosing environments
	var decl *dang.DirectiveDecl
	environments := findEnclosingEnvironments(f.AST, pos)
	for i := len(environments) - 1; i >= 0; i-- {
		if d, ok := environments[i].GetDirective(app.Name); ok {
			decl = d
			break
		}
	}
	// Also check the file-level type env
	if decl == nil && f.TypeEnv != nil {
		if d, ok := f.TypeEnv.GetDirective(app.Name); ok {
			decl = d
		}
	}

	if decl == nil {
		return nil, nil
	}

	// Format without the doc string so we can show it separately as markdown.
	savedDoc := decl.DocString
	decl.DocString = ""
	schema := dang.Format(decl)
	decl.DocString = savedDoc

	var content string
	if decl.DocString != "" {
		content = fmt.Sprintf("%s\n\n```dang\n%s\n```", decl.DocString, schema)
	} else {
		content = fmt.Sprintf("```dang\n%s\n```", schema)
	}

	return &Hover{
		Contents: MarkupContent{
			Kind:  Markdown,
			Value: content,
		},
	}, nil
}

// findDocInLexicalScope searches for documentation in any enclosing lexical scope
func (h *langHandler) findDocInLexicalScope(ctx context.Context, root dang.Node, pos Position, symbolName string) string {
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
