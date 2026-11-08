package lsp

import (
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

// lspPosition converts a 1-based analysis position to a 0-based LSP one.
func lspPosition(p analysis.Pos) Position {
	return Position{Line: p.Line - 1, Character: p.Column - 1}
}

// lspRange converts a 1-based analysis range to a 0-based LSP one.
func lspRange(r analysis.Range) Range {
	return Range{Start: lspPosition(r.Start), End: lspPosition(r.End)}
}

// lspDiagnostic converts an analysis diagnostic to the LSP's. Deprecations
// are tagged so editors strike them through.
func lspDiagnostic(d analysis.Diagnostic) Diagnostic {
	diag := Diagnostic{
		Range:    lspRange(d.Range),
		Severity: int(SeverityError),
		Source:   stringPtr("dang"),
		Message:  d.Message,
	}
	if d.Severity == analysis.SeverityWarning {
		diag.Severity = int(SeverityWarning)
	}
	if d.Code == analysis.CodeDeprecated {
		diag.Tags = []DiagnosticTag{DiagnosticTagDeprecated}
	}
	return diag
}

// fileDiagnostics returns the LSP diagnostics of the file at path in a.
func fileDiagnostics(a *analysis.Analysis, path string) []Diagnostic {
	diags := []Diagnostic{}
	for _, d := range a.Diagnostics() {
		if d.Path == path {
			diags = append(diags, lspDiagnostic(d))
		}
	}
	return diags
}

func stringPtr(s string) *string {
	return &s
}
