package lsp_test

import (
	"context"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/lsp"
)

func (LSPSuite) TestRescueLazinessWarningDiagnostic(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, "let g = \"hello\" rescue \"?\"\n")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Len(t, diagnostics, 1)
	d := diagnostics[0]
	require.Equal(t, int(lsp.SeverityWarning), d.Severity)
	require.Contains(t, d.Message, "this rescue can never fire")
	require.Equal(t, 0, d.Range.Start.Line)
	require.Equal(t, len("let g = "), d.Range.Start.Character)
}

func (LSPSuite) TestRescueLazinessCleanForFallibleOperand(ctx context.Context, t *testctx.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.dang")
	writeDangFile(t, mainPath, "boom: String! { raise \"boom\" }\nlet g = boom rescue \"?\"\n")

	h := newLSPHarness(ctx, t, root)
	uri := h.open(ctx, t, mainPath)
	diagnostics := h.waitForDiagnostics(ctx, t, uri)

	require.Empty(t, diagnostics, "a fallible operand should not be flagged")
}
