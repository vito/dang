package lsp

import (
	"context"
	"fmt"
	"log/slog"
)

func (h *langHandler) handleTextDocumentCodeAction(ctx context.Context, params CodeActionParams) (any, error) {
	// Always return a (possibly empty) array rather than null, so clients don't
	// treat a no-op as an error.
	actions := []CodeAction{}

	// Honor an explicit `only` filter: if the client asked for kinds that don't
	// include quick fixes, we have nothing to offer.
	if !codeActionKindAllowed(params.Context.Only, QuickFix) {
		return actions, nil
	}

	f := h.waitForFile(params.TextDocument.URI)
	if f == nil || f.AST == nil {
		return actions, nil
	}

	for _, call := range findDeprecatedCalls(f.AST) {
		if call.info.replacement == "" {
			// No structured replacement to rewrite to; the diagnostic still
			// flags it, but there's no safe automatic fix.
			continue
		}
		rng, ok := symbolRange(call.sym)
		if !ok || !rangesOverlap(rng, params.Range) {
			continue
		}

		diag := Diagnostic{
			Range:    rng,
			Severity: int(SeverityWarning),
			Source:   stringPtr("dang"),
			Message:  deprecationMessage(call),
			Tags:     []DiagnosticTag{DiagnosticTagDeprecated},
		}

		// Replace only the callee identifier (e.g. toJSON -> JSON.encode); the
		// argument list is untouched.
		edit := TextEdit{Range: rng, NewText: call.info.replacement}
		actions = append(actions, CodeAction{
			Title:       fmt.Sprintf("Replace %s with %s", call.name, call.info.replacement),
			Kind:        QuickFix,
			Diagnostics: []Diagnostic{diag},
			IsPreferred: true,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					string(params.TextDocument.URI): {edit},
				},
			},
		})
	}

	slog.InfoContext(ctx, "code action request",
		"uri", params.TextDocument.URI, "range", params.Range, "actions", len(actions))

	return actions, nil
}

// codeActionKindAllowed reports whether a code action of the given kind should
// be offered for a request restricting kinds via context.only. An empty filter
// allows everything; otherwise the kind is allowed when it equals, or is nested
// under, one of the requested kinds.
func codeActionKindAllowed(only []CodeActionKind, kind CodeActionKind) bool {
	if len(only) == 0 {
		return true
	}
	for _, k := range only {
		if k == Empty || k == kind {
			return true
		}
		// A requested ancestor kind (e.g. "quickfix") permits descendants.
		if len(kind) > len(k) && kind[:len(k)] == k && kind[len(k)] == '.' {
			return true
		}
	}
	return false
}
