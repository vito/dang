package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
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

// findDocInLexicalScope searches for documentation in any enclosing lexical scope
// by walking up the AST and checking nodes that store local environments
func (h *langHandler) findDocInLexicalScope(ctx context.Context, root dang.Node, pos Position, symbolName string) string {
	var environments []dang.Env

	// Walk the AST to find all enclosing scopes that might have stored environments
	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}

		loc := n.GetSourceLocation()
		if loc == nil {
			return true
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
			
			// Check if this node has a stored environment
			switch typed := n.(type) {
			case *dang.ClassDecl:
				if typed.Inferred != nil {
					environments = append(environments, typed.Inferred)
				}
			case *dang.Block:
				if typed.Env != nil {
					environments = append(environments, typed.Env)
				}
			case *dang.Object:
				if typed.Mod != nil {
					environments = append(environments, typed.Mod)
				}
			case *dang.ObjectSelection:
				if typed.Inferred != nil {
					environments = append(environments, typed.Inferred)
				}
			}
		}

		return true
	})

	// Search environments from innermost to outermost
	// (reverse order since we collected from outermost to innermost)
	for i := len(environments) - 1; i >= 0; i-- {
		if doc, ok := environments[i].GetDocString(symbolName); ok {
			return doc
		}
	}

	return ""
}
