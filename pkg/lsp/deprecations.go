package lsp

import (
	"fmt"
	"sync"

	"github.com/vito/dang/v2/pkg/dang"
)

// deprecatedBuiltin captures what the LSP needs to flag a deprecated builtin
// call: the human-facing reason and, when available, the structured replacement
// callee used for the quick-fix rewrite.
type deprecatedBuiltin struct {
	reason      string
	replacement string
}

var (
	deprecatedBuiltinsOnce   sync.Once
	deprecatedBuiltinsByName map[string]deprecatedBuiltin
)

// deprecatedBuiltins returns the top-level builtin functions marked deprecated,
// keyed by name. The builtin registry is populated in dang's package init, so
// it is safe to read once and cache.
func deprecatedBuiltins() map[string]deprecatedBuiltin {
	deprecatedBuiltinsOnce.Do(func() {
		deprecatedBuiltinsByName = map[string]deprecatedBuiltin{}
		dang.ForEachFunction(func(d dang.BuiltinDef) {
			if d.Deprecated != "" {
				deprecatedBuiltinsByName[d.Name] = deprecatedBuiltin{
					reason:      d.Deprecated,
					replacement: d.Replacement,
				}
			}
		})
	})
	return deprecatedBuiltinsByName
}

// deprecatedCall is a call site whose callee names a deprecated builtin.
type deprecatedCall struct {
	name string
	info deprecatedBuiltin
	// sym is the callee identifier, used for both the diagnostic range and the
	// quick-fix rewrite (only the callee token is replaced; arguments stay put).
	sym *dang.Symbol
}

// findDeprecatedCalls walks the AST and returns every call whose callee names a
// deprecated builtin. It matches by callee name: a local binding that shadows a
// builtin of the same name would be a false positive, but shadowing the
// deprecated conversion builtins is vanishingly rare, and the eval-time warning
// is the source of truth.
func findDeprecatedCalls(root dang.Node) []deprecatedCall {
	if root == nil {
		return nil
	}
	deprecated := deprecatedBuiltins()
	var calls []deprecatedCall
	root.Walk(func(n dang.Node) bool {
		if n == nil {
			return false
		}
		call, ok := n.(*dang.FunCall)
		if !ok {
			return true
		}
		sym, ok := call.Fun.(*dang.Symbol)
		if !ok {
			return true
		}
		if info, ok := deprecated[sym.Name]; ok {
			calls = append(calls, deprecatedCall{name: sym.Name, info: info, sym: sym})
		}
		return true
	})
	return calls
}

// symbolRange returns the LSP range covering a callee identifier. Dang source
// locations are 1-based; LSP positions are 0-based.
func symbolRange(sym *dang.Symbol) (Range, bool) {
	loc := sym.GetSourceLocation()
	if loc == nil {
		return Range{}, false
	}
	startLine := loc.Line - 1
	startCol := loc.Column - 1
	return Range{
		Start: Position{Line: startLine, Character: startCol},
		End:   Position{Line: startLine, Character: startCol + len(sym.Name)},
	}, true
}

// deprecationMessage is the warning shown for a deprecated call; it matches the
// eval-time warning wording so the two surfaces agree.
func deprecationMessage(call deprecatedCall) string {
	return fmt.Sprintf("%s is deprecated: %s", call.name, call.info.reason)
}

// deprecationDiagnostics produces a strike-through warning for each deprecated
// builtin call in the AST.
func deprecationDiagnostics(root dang.Node) []Diagnostic {
	var diags []Diagnostic
	for _, call := range findDeprecatedCalls(root) {
		rng, ok := symbolRange(call.sym)
		if !ok {
			continue
		}
		diags = append(diags, Diagnostic{
			Range:    rng,
			Severity: int(SeverityWarning),
			Source:   stringPtr("dang"),
			Message:  deprecationMessage(call),
			Tags:     []DiagnosticTag{DiagnosticTagDeprecated},
		})
	}
	return diags
}
