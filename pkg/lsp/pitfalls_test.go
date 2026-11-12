package lsp

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

func TestPitfallDiagnosticLinksItsDocs(t *testing.T) {
	d := lspDiagnostic(analysis.Diagnostic{
		Severity: analysis.SeverityError,
		Code:     analysis.CodeSyntax,
		Message:  "syntax error: `//` is not a comment",
		Pitfall:  "line-comment",
		Hint:     "comments start with `#`",
		Docs:     "https://danglang.org/syntax#comments",
	})
	require.Equal(t, "syntax error: `//` is not a comment\nhint: comments start with `#`", d.Message)
	require.Equal(t, "line-comment", *d.Code)
	require.Equal(t, &CodeDescription{Href: "https://danglang.org/syntax#comments"}, d.CodeDescription)

	plain := lspDiagnostic(analysis.Diagnostic{Severity: analysis.SeverityError, Message: "boom"})
	require.Equal(t, "boom", plain.Message)
	require.Nil(t, plain.Code)
	require.Nil(t, plain.CodeDescription)
}
