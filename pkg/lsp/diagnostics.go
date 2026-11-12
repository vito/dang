package lsp

import (
	"path/filepath"

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

// analysisPos converts a 0-based LSP position to a 1-based analysis one.
func analysisPos(p Position) analysis.Pos {
	return analysis.Pos{Line: p.Line + 1, Column: p.Character + 1}
}

// lspLocation converts a location in the analysis of the file at uri, whose
// Source is rooted at the file's directory (see analyzeDirectory), to an LSP
// location.
func lspLocation(uri DocumentURI, loc analysis.Location) (Location, bool) {
	fp, err := fromURI(uri)
	if err != nil {
		return Location{}, false
	}
	path := filepath.Join(filepath.Dir(fp), filepath.FromSlash(loc.Path))
	return Location{URI: toURI(path), Range: lspRange(loc.Range)}, true
}

// inspect returns what the name at pos in f names (see analysis.Inspect),
// or nil.
func inspect(f *File, pos Position) *analysis.Symbol {
	if f == nil || f.Analysis == nil {
		return nil
	}
	sym, err := f.Analysis.Inspect(f.Path, analysisPos(pos))
	if err != nil {
		return nil
	}
	return sym
}

// lspDiagnostic converts an analysis diagnostic to the LSP's. Deprecations
// are tagged so editors strike them through. A pitfall's hint follows the
// message, and its ID and docs link are the code and code description.
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
	if d.Pitfall != "" {
		diag.Code = stringPtr(d.Pitfall)
		if d.Hint != "" {
			diag.Message += "\nhint: " + d.Hint
		}
		if d.Docs != "" {
			diag.CodeDescription = &CodeDescription{Href: d.Docs}
		}
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
