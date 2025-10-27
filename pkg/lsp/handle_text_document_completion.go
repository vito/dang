package lsp

import (
	"github.com/newstack-cloud/ls-builder/common"
	"github.com/newstack-cloud/ls-builder/lsp_3_17"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

func (h *langHandler) handleTextDocumentCompletion(ctx *common.LSPContext, params *lsp.CompletionParams) (any, error) {
	f, ok := h.files[params.TextDocument.URI]
	if !ok {
		return []*lsp.CompletionItem{}, nil
	}

	// Check if we're completing a member access (e.g., "container.fr<TAB>")
	// We do this by finding the node at the cursor and checking if it's a Select
	if f.AST != nil {
		receiver := FindReceiverAt(f.AST, int(params.Position.Line), int(params.Position.Character))
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

	// Add lexical bindings from enclosing scopes
	if f.AST != nil {
		return h.getLexicalCompletions(ctx, f.AST, params.Position, f.TypeEnv), nil
	}

	return nil, nil
}

// getLexicalCompletions returns completion items for symbols in enclosing lexical scopes
func (h *langHandler) getLexicalCompletions(ctx *common.LSPContext, root dang.Node, pos lsp.Position, fileEnv dang.Env) []*lsp.CompletionItem {
	var environments []dang.Env

	// First add the file-level environment if available
	if fileEnv != nil {
		environments = append(environments, fileEnv)
	}

	// Collect all enclosing environments
	environments = append(environments, findEnclosingEnvironments(root, pos)...)

	// Collect all unique symbols from all environments
	seen := make(map[string]bool)
	var items []*lsp.CompletionItem

	// Search environments from innermost to outermost (reverse order)
	for i := len(environments) - 1; i >= 0; i-- {
		env := environments[i]

		// Get all bindings from this environment (both public and private for completion)
		for name, scheme := range env.Bindings(dang.PrivateVisibility) {
			// Skip if we've already seen this symbol
			if seen[name] {
				continue
			}
			seen[name] = true

			// Determine type and kind
			memberType, _ := scheme.Type()
			kind := lsp.CompletionItemKindVariable
			if _, isFn := memberType.(*hm.FunctionType); isFn {
				kind = lsp.CompletionItemKindFunction
			}

			// Get documentation for this symbol
			var documentation any
			if doc, found := env.GetDocString(name); found {
				documentation = doc
			}

			detail := memberType.String()
			items = append(items, &lsp.CompletionItem{
				Label:         name,
				Kind:          &kind,
				Detail:        &detail,
				Documentation: documentation,
			})
		}
	}

	return items
}

// getMemberCompletions returns completion items for a type's members
func (h *langHandler) getMemberCompletions(t hm.Type) []*lsp.CompletionItem {
	var items []*lsp.CompletionItem

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
		kind := lsp.CompletionItemKindVariable
		if _, isFn := memberType.(*hm.FunctionType); isFn {
			kind = lsp.CompletionItemKindMethod
		}

		// Get documentation for this member
		var documentation any
		if doc, found := module.GetDocString(name); found {
			documentation = doc
		}

		detail := memberType.String()
		items = append(items, &lsp.CompletionItem{
			Label:         name,
			Kind:          &kind,
			Detail:        &detail,
			Documentation: documentation,
		})
	}

	return items
}
