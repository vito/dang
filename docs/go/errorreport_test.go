package dangdocs

import (
	"strings"
	"testing"

	"github.com/vito/dang/v2/pkg/dang"
)

// renderErrorReport bakes the annotated-snippet HTML that playground.js's
// errorReportHtml rebuilds on replay; this pins the DOM contract the two
// share: section/title/field/snippet classes, the filename-less arrow, the
// terminal's gutter geometry, and the caret underline.
func TestRenderErrorReport(t *testing.T) {
	rep := &dang.ErrorReport{
		Sections: []dang.ErrorReportSection{
			{
				Role:    dang.ReportPrimary,
				Message: "uncaught DeployError: deploy failed",
				Fields:  []dang.ErrorReportField{{Name: "stage", Value: `"push"`}},
				Location: &dang.SourceLocation{
					Filename: "literate", Line: 2, Column: 3, Length: 5,
				},
				Snippet: &dang.ErrorSnippet{
					StartLine: 1,
					Lines:     []string{"first line", "  raise it", "third line"},
				},
			},
			{
				Role:    dang.ReportCause,
				Message: "error: connection refused",
				// No location at all: an explicit `cause` field. Renders as
				// just the labeled header.
			},
			{
				Role:     dang.ReportSibling,
				Message:  "error: second",
				Location: &dang.SourceLocation{Line: 3, Column: 25, Length: 1},
				// Location but no snippet: degrades to the bare arrow.
			},
		},
	}

	html := renderErrorReport(rep, "Runtime error").String()

	for _, want := range []string{
		`<span class="dang-error-label">Runtime error:</span> uncaught DeployError: deploy failed`,
		`<span class="dang-error-label">caused by:</span> error: connection refused`,
		`<span class="dang-error-label">also failed:</span> error: second`,
		// Fields keep the terminal's two-space indent.
		`<div class="dang-error-field">  stage: `,
		// The arrow drops the synthetic filename.
		`<span class="dang-error-arrow">  --&gt; 2:3</span>`,
		`<span class="dang-error-arrow">  --&gt; 3:25</span>`,
		// Gutter geometry mirrors formatSourceAnnotation: " %3d | ".
		`<span class="dang-error-gutter">   1 | </span>`,
		`<span class="dang-error-gutter is-hl">   2 | </span>`,
		// Underline: 1+3+3 = 7 chars of lead, then column-1, then ^ x length.
		"\n" + strings.Repeat(" ", 7+2) + `<span class="dang-error-underline">^^^^^</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered report missing %q:\n%s", want, html)
		}
	}

	// Context lines are dimmed; the error line is not.
	if !strings.Contains(html, `<span class="dang-error-dim">`) {
		t.Errorf("rendered report has no dimmed context lines:\n%s", html)
	}
	if strings.Contains(html, `is-hl">   2 | </span><span class="dang-error-dim">`) {
		t.Errorf("the error line must not be dimmed:\n%s", html)
	}

	// The cause section has no location, so exactly two snippets render.
	if got := strings.Count(html, `<pre class="dang-error-snippet">`); got != 2 {
		t.Errorf("got %d snippets, want 2 (cause has no location):\n%s", got, html)
	}
}

// Snippet lines are highlighted as one fragment: a template string spanning
// lines must not reset per line. Guarded by cgo builds only in effect —
// without a grammar highlightSnippetLines degrades to escaped plain text,
// and the per-line split alone is still exercised.
func TestHighlightSnippetLinesSplit(t *testing.T) {
	lines := highlightSnippetLines([]string{"let a = 1", "let b = 2"})
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
	for i, l := range lines {
		if strings.Contains(l, "\n") {
			t.Errorf("line %d contains a newline: %q", i, l)
		}
	}
	if !strings.Contains(lines[0], "let a = 1") && !strings.Contains(lines[0], "let") {
		t.Errorf("line 0 lost its text: %q", lines[0])
	}
}
