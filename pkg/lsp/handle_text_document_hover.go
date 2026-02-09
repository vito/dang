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

	// Skip hover for literal nodes — the word under cursor is just content, not a symbol.
	switch node.(type) {
	case *dang.String, *dang.Int, *dang.Float, *dang.Boolean, *dang.Null:
		return nil, nil
	}

	// Check if we're hovering over a field access (Select node)
	if selectNode, ok := node.(*dang.Select); ok {
		receiverType := selectNode.Receiver.GetInferredType()
		if receiverType != nil {
			if nn, ok := receiverType.(hm.NonNullType); ok {
				receiverType = nn.Type
			}
			if env, ok := receiverType.(dang.Env); ok {
				var docString string
				if doc, found := env.GetDocString(selectNode.Field); found {
					docString = doc
				}
				if scheme, found := env.LocalSchemeOf(selectNode.Field); found {
					fieldType, _ := scheme.Type()
					codeBlock := fmt.Sprintf("%s: %s", symbolName, fieldType)
					return h.hoverResultWithDoc(docString, codeBlock)
				}
			}
		}
	}

	// Try the node's inferred type
	if inferredType := node.GetInferredType(); inferredType != nil {
		codeBlock := fmt.Sprintf("%s: %s", symbolName, inferredType)
		return h.hoverResult(f, params.Position, symbolName, codeBlock)
	}

	// Try to format the declaring node's signature from the symbol table
	if f.Symbols != nil {
		if def, ok := f.Symbols.Definitions[symbolName]; ok {
			if sig := formatDeclSignature(def.Node); sig != "" {
				return h.hoverResult(f, params.Position, symbolName, sig)
			}
		}
	}

	return nil, nil
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

// hoverResult builds a hover response, looking up doc strings from the environment.
func (h *langHandler) hoverResult(f *File, pos Position, symbolName string, codeBlock string) (any, error) {
	var docString string

	// Try the file's type environment
	if f.TypeEnv != nil {
		if doc, ok := f.TypeEnv.GetDocString(symbolName); ok {
			docString = doc
		}
	}

	// If not found at file level, try to find in lexical scopes
	if docString == "" && f.AST != nil {
		environments := findEnclosingEnvironments(f.AST, pos)
		for i := len(environments) - 1; i >= 0; i-- {
			if doc, ok := environments[i].GetDocString(symbolName); ok {
				docString = doc
				break
			}
		}
	}

	return h.hoverResultWithDoc(docString, codeBlock)
}

// hoverResultWithDoc builds a hover response with an explicit doc string.
func (h *langHandler) hoverResultWithDoc(docString string, codeBlock string) (any, error) {
	var content string
	if docString != "" {
		content = fmt.Sprintf("%s\n\n```dang\n%s\n```", docString, codeBlock)
	} else {
		content = fmt.Sprintf("```dang\n%s\n```", codeBlock)
	}

	return &Hover{
		Contents: MarkupContent{
			Kind:  Markdown,
			Value: content,
		},
	}, nil
}

// formatDeclSignature formats a declaring node's signature without the body.
func formatDeclSignature(node dang.Node) string {
	switch n := node.(type) {
	case *dang.SlotDecl:
		// Temporarily strip the doc string, value, and body to format just the signature.
		savedDoc := n.DocString
		savedValue := n.Value
		n.DocString = ""

		if funDecl, ok := n.Value.(*dang.FunDecl); ok {
			// For functions, keep the FunDecl but strip its body.
			savedBody := funDecl.FunctionBase.Body
			funDecl.FunctionBase.Body = nil
			sig := dang.Format(n)
			funDecl.FunctionBase.Body = savedBody
			n.DocString = savedDoc
			return sig
		}

		// For non-function slots, strip the value so we get just the type annotation.
		n.Value = nil
		sig := dang.Format(n)
		n.Value = savedValue
		n.DocString = savedDoc
		return sig

	case *dang.ClassDecl:
		return fmt.Sprintf("type %s", n.Name.Name)

	default:
		return ""
	}
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


