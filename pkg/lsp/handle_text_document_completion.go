package lsp

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

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

	env := h.buildCompletionTypeScope(f, params.Position)
	if env != nil {
		// Completion re-parses fragments of the buffer (e.g. the receiver
		// before a `.`), which have no path of their own: parse them in the
		// Dang version of the file they came from.
		if path, err := fromURI(params.TextDocument.URI); err == nil {
			ctx = dang.ContextWithVersion(ctx, versionForFile(ctx, path))
		}
		result := analysis.Complete(ctx, env, f.Text, analysis.Pos{
			Line:   params.Position.Line + 1,
			Column: params.Position.Character + 1,
		})
		if len(result.Items) > 0 {
			return completionsToItems(result.Items), nil
		}
	}

	// Fallback: lexical completions from all enclosing scopes.
	if f.AST != nil {
		return h.getLexicalCompletions(ctx, f.AST, params.Position, f.TypeScope), nil
	}

	return nil, nil
}

// buildCompletionTypeScope creates a type environment that includes the file-level
// env plus all enclosing scope bindings at the given position. This allows
// resolving local variables (e.g. "ctr") that are not in the top-level env.
func (h *langHandler) buildCompletionTypeScope(f *File, pos Position) dang.TypeScope {
	if f.Analysis != nil {
		return f.Analysis.ScopeAt(f.Path, analysisPos(pos))
	}

	if f.TypeScope == nil {
		return nil
	}

	if f.AST == nil {
		return f.TypeScope
	}

	// Collect enclosing scope environments from the Dang AST
	enclosing := findEnclosingEnvironments(f.AST, pos)
	if len(enclosing) == 0 {
		return f.TypeScope
	}

	// Build a layered env: start from the file-level env (cloned),
	// then merge all enclosing scope bindings into it.
	env := f.TypeScope.Clone().(dang.TypeScope)
	for _, enclosingScope := range enclosing {
		for name, scheme := range enclosingScope.Bindings(dang.PrivateVisibility) {
			env.Add(name, scheme)
		}
	}

	return env
}

// getLexicalCompletions returns completion items for symbols in enclosing lexical scopes
func (h *langHandler) getLexicalCompletions(ctx context.Context, root dang.Node, pos Position, fileScope dang.TypeScope) []CompletionItem {
	var environments []dang.TypeScope

	// First add the file-level environment if available
	if fileScope != nil {
		environments = append(environments, fileScope)
	}

	// Collect all enclosing environments
	environments = append(environments, findEnclosingEnvironments(root, pos)...)

	// Collect all unique symbols from all environments
	seen := make(map[string]bool)
	var items []CompletionItem

	// Search environments from innermost to outermost (reverse order)
	for i := len(environments) - 1; i >= 0; i-- {
		// Everything in scope, private bindings included, but not types that
		// can only be named in type position.
		for _, m := range analysis.ScopeMembers(environments[i]) {
			if seen[m.Name] || m.TypeOnly {
				continue
			}
			seen[m.Name] = true

			kind := VariableCompletion
			if m.Kind == analysis.KindFunction {
				kind = FunctionCompletion
			}

			items = append(items, CompletionItem{
				Label:         m.Name,
				Kind:          kind,
				Detail:        m.Detail(),
				Documentation: m.Doc,
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
