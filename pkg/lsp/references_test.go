package lsp_test

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/lsp"
)

const shadowingSource = `let x = 1

double(x: Int!): Int! {
  x * 2
}

pub total = double(x)
`

// position returns the 0-based LSP position of the first occurrence of
// needle in text, offset by off characters into it.
func position(text, needle string, off int) lsp.Position {
	offset := strings.Index(text, needle) + off
	line := strings.Count(text[:offset], "\n")
	return lsp.Position{Line: line, Character: offset - strings.LastIndex(text[:offset], "\n") - 1}
}

func (LSPSuite) TestDefinitionResolvesShadowedLocals(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, shadowingSource)

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	require.Empty(t, h.waitForDiagnostics(ctx, t, uri))

	definition := func(pos lsp.Position) lsp.Location {
		var loc lsp.Location
		require.NoError(t, h.client.CallResult(ctx, "textDocument/definition", lsp.DocumentDefinitionParams{
			TextDocumentPositionParams: lsp.TextDocumentPositionParams{
				TextDocument: lsp.TextDocumentIdentifier{URI: uri},
				Position:     pos,
			},
		}, &loc))
		return loc
	}

	// x in double's body is its parameter, not the top-level x...
	loc := definition(position(shadowingSource, "x * 2", 0))
	require.Equal(t, uri, loc.URI)
	require.Equal(t, position(shadowingSource, "x: Int!", 0), loc.Range.Start)

	// ...while the argument in double(x) is the top-level x.
	loc = definition(position(shadowingSource, "double(x)", len("double(")))
	require.Equal(t, position(shadowingSource, "let x", len("let ")), loc.Range.Start)
}

func (LSPSuite) TestReferencesAndRenameResolveShadowedLocals(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, shadowingSource)

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	require.Empty(t, h.waitForDiagnostics(ctx, t, uri))

	var refs []lsp.Location
	require.NoError(t, h.client.CallResult(ctx, "textDocument/references", lsp.ReferenceParams{
		TextDocumentPositionParams: lsp.TextDocumentPositionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     position(shadowingSource, "let x", len("let ")),
		},
		Context: lsp.ReferenceContext{IncludeDeclaration: true},
	}, &refs))
	var starts []lsp.Position
	for _, ref := range refs {
		starts = append(starts, ref.Range.Start)
	}
	require.Equal(t, []lsp.Position{
		position(shadowingSource, "let x", len("let ")),
		position(shadowingSource, "double(x)", len("double(")),
	}, starts, "double's parameter x and its uses aren't references to the top-level x")

	var edit struct {
		Changes map[string][]lsp.TextEdit `json:"changes"`
	}
	require.NoError(t, h.client.CallResult(ctx, "textDocument/rename", lsp.RenameParams{
		TextDocument: lsp.TextDocumentIdentifier{URI: uri},
		Position:     position(shadowingSource, "x * 2", 0),
		NewName:      "n",
	}, &edit))
	var renamed []lsp.Position
	for _, e := range edit.Changes[string(uri)] {
		renamed = append(renamed, e.Range.Start)
	}
	require.Equal(t, []lsp.Position{
		position(shadowingSource, "x: Int!", 0),
		position(shadowingSource, "x * 2", 0),
	}, renamed)
}
