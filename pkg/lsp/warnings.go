package lsp

import (
	"github.com/vito/dang/v2/pkg/dang"
)

// warningDiagnostics converts collected inference warnings into
// Warning-severity diagnostics, filtered to the active file.
func warningDiagnostics(warnings []dang.InferWarning, path string) []Diagnostic {
	var diags []Diagnostic
	for _, w := range warnings {
		loc := w.Location
		if loc == nil || (loc.Filename != "" && !sameFile(loc.Filename, path)) {
			continue
		}

		startLine := loc.Line - 1
		startCol := loc.Column - 1
		endLine := startLine
		endCol := startCol + loc.Length
		if loc.Length == 0 {
			endCol = startCol + 1
		}
		if loc.End != nil {
			endLine = loc.End.Line - 1
			endCol = loc.End.Column - 1
		}

		diags = append(diags, Diagnostic{
			Range: Range{
				Start: Position{Line: startLine, Character: startCol},
				End:   Position{Line: endLine, Character: endCol},
			},
			Severity: int(SeverityWarning),
			Source:   stringPtr("dang"),
			Message:  w.Message,
		})
	}
	return diags
}
