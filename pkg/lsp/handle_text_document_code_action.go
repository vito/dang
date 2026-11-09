package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/creachadair/jrpc2"
)

func (h *langHandler) handleTextDocumentCodeAction(ctx context.Context, req *jrpc2.Request) (any, error) {
	if !req.HasParams() {
		return nil, jrpc2.Errorf(jrpc2.InvalidParams, "missing parameters")
	}

	var params CodeActionParams
	if err := req.UnmarshalParams(&params); err != nil {
		return nil, err
	}

	// Always return a (possibly empty) array rather than null, so clients don't
	// treat a no-op as an error.
	actions := []CodeAction{}

	// Honor an explicit `only` filter: if the client asked for kinds that don't
	// include quick fixes, we have nothing to offer.
	if !codeActionKindAllowed(params.Context.Only, QuickFix) {
		return actions, nil
	}

	f := h.waitForFile(params.TextDocument.URI)
	if f == nil || f.Analysis == nil || f.AST == nil {
		return actions, nil
	}

	for _, d := range f.Analysis.Diagnostics() {
		if d.Path != f.Path || d.Replacement == "" {
			// No structured replacement to rewrite to; the diagnostic still
			// flags it, but there's no safe automatic fix.
			continue
		}
		diag := lspDiagnostic(d)
		if !rangesOverlap(diag.Range, params.Range) {
			continue
		}

		// Replace only the flagged range (e.g. the callee toJSON ->
		// JSON.encode); the argument list is untouched.
		edit := TextEdit{Range: diag.Range, NewText: d.Replacement}
		actions = append(actions, CodeAction{
			Title:       fmt.Sprintf("Replace %s with %s", rangeText(f.Text, diag.Range), d.Replacement),
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

// rangeText returns the text a single-line range covers, or "" if it doesn't
// fit text.
func rangeText(text string, rng Range) string {
	lines := strings.Split(text, "\n")
	if rng.Start.Line != rng.End.Line || rng.Start.Line < 0 || rng.Start.Line >= len(lines) {
		return ""
	}
	line := []rune(lines[rng.Start.Line])
	if rng.Start.Character < 0 || rng.End.Character > len(line) || rng.Start.Character > rng.End.Character {
		return ""
	}
	return string(line[rng.Start.Character:rng.End.Character])
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
