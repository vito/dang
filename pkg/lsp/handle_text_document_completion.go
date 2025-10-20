package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

func (h *langHandler) handleTextDocumentCompletion(ctx context.Context, conn *jsonrpc2.Conn, req *jsonrpc2.Request) (result any, err error) {
	if req.Params == nil {
		return nil, &jsonrpc2.Error{Code: jsonrpc2.CodeInvalidParams}
	}

	var params CompletionParams
	if err := json.Unmarshal(*req.Params, &params); err != nil {
		return nil, err
	}

	f, ok := h.files[params.TextDocument.URI]
	if !ok {
		return []CompletionItem{}, nil
	}

	// Check if we're completing after a "." (member access)
	if h.isAfterDot(f, params.Position) {
		// Find the receiver expression before the dot
		if f.AST != nil {
			receiver := FindReceiverAt(f.AST, params.Position.Line, params.Position.Character)
			if receiver != nil {
				// Get the inferred type of the receiver
				receiverType := receiver.GetInferredType()
				if receiverType != nil {
					// Offer completions for this type's members
					items := h.getMemberCompletions(receiverType)
					if len(items) > 0 {
						return items, nil
					}
				}
			}
		}
	}

	var items []CompletionItem

	// Add all defined symbols from the current file
	for name, info := range f.Symbols.Definitions {
		items = append(items, CompletionItem{
			Label: name,
			Kind:  info.Kind,
		})
	}

	// Add lexical bindings (function parameters, etc.) that are in scope at the cursor position
	if f.LexicalAnalyzer != nil {
		// Convert LSP position to Dang SourceLocation for scope checking
		cursorLoc := &dang.SourceLocation{
			Filename: string(params.TextDocument.URI),
			Line:     params.Position.Line + 1, // LSP is 0-based, Dang is 1-based
			Column:   params.Position.Character + 1, // LSP is 0-based, Dang is 1-based
		}

		// Find bindings that are in scope at the cursor position
		bindings := f.LexicalAnalyzer.FindBindingsAt(cursorLoc)
		for _, binding := range bindings {
			items = append(items, CompletionItem{
				Label:  binding.Symbol,
				Kind:   binding.Kind,
				Detail: "lexical binding",
			})
		}
	}

	// Add global functions from GraphQL schema (Query type fields)
	if h.schema != nil && h.schema.QueryType.Name != "" {
		items = append(items, h.getSchemaCompletions()...)
	}

	return items, nil
}

// getSchemaCompletions returns completion items for global functions from the GraphQL schema
func (h *langHandler) getSchemaCompletions() []CompletionItem {
	var items []CompletionItem

	// Find the Query type in the schema
	for _, t := range h.schema.Types {
		if t.Name == h.schema.QueryType.Name {
			// Found the Query type - add all its fields as global functions
			for _, field := range t.Fields {
				// Skip internal/deprecated fields if needed
				if strings.HasPrefix(field.Name, "__") {
					continue
				}

				items = append(items, CompletionItem{
					Label:  field.Name,
					Kind:   FunctionCompletion,
					Detail: "global function",
				})
			}
			break
		}
	}

	return items
}

// isAfterDot checks if the cursor is immediately after a "."
func (h *langHandler) isAfterDot(f *File, pos Position) bool {
	lines := strings.Split(f.Text, "\n")
	if pos.Line >= len(lines) {
		return false
	}

	line := lines[pos.Line]
	if pos.Character == 0 {
		return false
	}

	// Check if the previous character is a "."
	return pos.Character > 0 && pos.Character <= len(line) && line[pos.Character-1] == '.'
}

// getMemberCompletions returns completion items for a type's members
func (h *langHandler) getMemberCompletions(t hm.Type) []CompletionItem {
	var items []CompletionItem

	// Unwrap NonNullType if needed
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}

	// Check if the type is a Module
	module, ok := t.(*dang.Module)
	if !ok {
		return items
	}

	// Iterate over all public members of the type
	for name, scheme := range module.Bindings(dang.PublicVisibility) {
		memberType, _ := scheme.Type()

		// Determine completion kind based on member type
		kind := VariableCompletion
		if _, isFn := memberType.(*hm.FunctionType); isFn {
			kind = MethodCompletion
		}

		items = append(items, CompletionItem{
			Label:  name,
			Kind:   kind,
			Detail: memberType.String(),
		})
	}

	return items
}
