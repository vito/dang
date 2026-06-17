package lsp_test

import (
	"context"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/lsp"
)

func (LSPSuite) TestDeprecationDiagnosticFlagsDeprecatedBuiltin(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, "let s = toJSON([1, 2, 3])\n")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Len(t, diagnostics, 1)
	d := diagnostics[0]
	require.Equal(t, "toJSON is deprecated: use JSON.encode instead", d.Message)
	require.Equal(t, int(lsp.SeverityWarning), d.Severity)
	require.Contains(t, d.Tags, lsp.DiagnosticTagDeprecated)

	// The range must cover the `toJSON` callee token: line 0, after "let s = ".
	require.Equal(t, 0, d.Range.Start.Line)
	require.Equal(t, len("let s = "), d.Range.Start.Character)
	require.Equal(t, len("let s = ")+len("toJSON"), d.Range.End.Character)
}

func (LSPSuite) TestDeprecationDiagnosticCleanWhenUsingReplacement(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, "let s = JSON.encode([1, 2, 3])\n")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Empty(t, diagnostics, "the non-deprecated JSON.encode should not be flagged")
}

// codeActionResult mirrors the wire shape of a returned CodeAction with the
// Changes map typed concretely, since lsp.WorkspaceEdit.Changes is `any`.
type codeActionResult struct {
	Title       string             `json:"title"`
	Kind        string             `json:"kind"`
	IsPreferred bool               `json:"isPreferred"`
	Diagnostics []lsp.Diagnostic   `json:"diagnostics"`
	Edit        codeActionEditWire `json:"edit"`
}

type codeActionEditWire struct {
	Changes map[string][]lsp.TextEdit `json:"changes"`
}

func (LSPSuite) TestDeprecationCodeActionReplacesCallee(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, "let s = toJSON([1, 2, 3])\n")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	_ = h.waitForDiagnostics(ctx, t, uri)

	// Request actions for a cursor sitting inside the toJSON token.
	cursor := lsp.Position{Line: 0, Character: len("let s = ") + 2}
	var actions []codeActionResult
	require.NoError(t, h.client.CallResult(ctx, "textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range:        lsp.Range{Start: cursor, End: cursor},
		Context:      lsp.CodeActionContext{Only: []lsp.CodeActionKind{lsp.QuickFix}},
	}, &actions))

	require.Len(t, actions, 1)
	action := actions[0]
	require.Equal(t, "Replace toJSON with JSON.encode", action.Title)
	require.Equal(t, string(lsp.QuickFix), action.Kind)
	require.True(t, action.IsPreferred)

	edits := action.Edit.Changes[string(uri)]
	require.Len(t, edits, 1)
	require.Equal(t, "JSON.encode", edits[0].NewText)
	// The edit replaces only the callee token, leaving the argument list intact.
	require.Equal(t, len("let s = "), edits[0].Range.Start.Character)
	require.Equal(t, len("let s = ")+len("toJSON"), edits[0].Range.End.Character)
}

func (LSPSuite) TestDeprecationCodeActionSkipsUnrelatedRange(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	// Two lines: the deprecated call is on line 0; ask for actions on line 1.
	writeDangFile(t, mainPath, "let s = toJSON([1, 2, 3])\nlet t = 1\n")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	_ = h.waitForDiagnostics(ctx, t, uri)

	pos := lsp.Position{Line: 1, Character: 0}
	var actions []codeActionResult
	require.NoError(t, h.client.CallResult(ctx, "textDocument/codeAction", lsp.CodeActionParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Range:        lsp.Range{Start: pos, End: pos},
		Context:      lsp.CodeActionContext{},
	}, &actions))

	require.Empty(t, actions, "no deprecated call overlaps the requested range")
}
