package lsp

import (
	"context"
	"strings"

	"github.com/creachadair/jrpc2"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/hm"
)

// getLineUpToCursor returns the text of the given line up to the cursor column.
func getLineUpToCursor(text string, line, col int) string {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	l := lines[line]
	if col > len(l) {
		col = len(l)
	}
	return strings.TrimLeft(l[:col], " \t")
}

func (h *langHandler) handleTextDocumentCompletion(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params CompletionParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	f := h.waitForFile(params.TextDocument.URI)
	if f == nil {
		return []CompletionItem{}, nil
	}

	// Try text-based completion first. This handles argument completion
	// (inside parens) and member/method completion on expressions that the
	// AST path can't resolve (e.g. "hello".sp where the cursor is past the
	// end of the Select node, or builtin type methods on strings/lists).
	if f.TypeEnv != nil {
		lineText := getLineUpToCursor(f.Text, params.Position.Line, params.Position.Character)
		if lineText != "" {
			completions := dang.CompleteInput(ctx, f.TypeEnv, lineText, len(lineText))
			if len(completions) > 0 {
				return completionsToItems(completions), nil
			}
		}
	}

	if f.AST != nil {
		// Check if we're completing a member access (e.g., "container.fr<TAB>")
		receiver := FindReceiverAt(f.AST, params.Position.Line, params.Position.Character)
		if receiver != nil {
			receiverType := receiver.GetInferredType()
			if receiverType != nil {
				completions := dang.MembersOf(receiverType, "")
				items := completionsToItems(completions)
				if len(items) > 0 {
					return items, nil
				}
			}
		}

		// Add lexical bindings from enclosing scopes
		return h.getLexicalCompletions(ctx, f.AST, params.Position, f.TypeEnv), nil
	}

	return nil, nil
}

// getLexicalCompletions returns completion items for symbols in enclosing lexical scopes
func (h *langHandler) getLexicalCompletions(ctx context.Context, root dang.Node, pos Position, fileEnv dang.Env) []CompletionItem {
	var environments []dang.Env

	// First add the file-level environment if available
	if fileEnv != nil {
		environments = append(environments, fileEnv)
	}

	// Collect all enclosing environments
	environments = append(environments, findEnclosingEnvironments(root, pos)...)

	// Collect all unique symbols from all environments
	seen := make(map[string]bool)
	var items []CompletionItem

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
			kind := VariableCompletion
			if _, isFn := memberType.(*hm.FunctionType); isFn {
				kind = FunctionCompletion
			}

			// Get documentation for this symbol
			var documentation string
			if doc, found := env.GetDocString(name); found {
				documentation = doc
			}

			items = append(items, CompletionItem{
				Label:         name,
				Kind:          kind,
				Detail:        memberType.String(),
				Documentation: documentation,
			})
		}
	}

	return items
}

// completionsToItems converts shared dang.Completion values to LSP CompletionItems.
func completionsToItems(completions []dang.Completion) []CompletionItem {
	items := make([]CompletionItem, len(completions))
	for i, c := range completions {
		kind := VariableCompletion
		if c.IsFunction {
			kind = MethodCompletion
		}
		if c.IsArg {
			kind = FieldCompletion
		}
		items[i] = CompletionItem{
			Label:         c.Label,
			Kind:          kind,
			Detail:        c.Detail,
			Documentation: c.Documentation,
		}
	}
	return items
}
