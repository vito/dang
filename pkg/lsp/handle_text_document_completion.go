package lsp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sourcegraph/jsonrpc2"
	"github.com/vito/dang/pkg/dang"
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
			Column:   params.Position.Character,
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
