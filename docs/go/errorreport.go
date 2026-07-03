package dangdocs

import (
	"fmt"
	"html"
	"strings"

	"github.com/vito/booklit"
	"github.com/vito/dang/v2/pkg/dang"
)

// renderErrorReport renders a structured error report as the annotated HTML
// analogue of the terminal output: per section, a labeled header line, a
// "--> line:col" location arrow, and the quoted source window with a
// line-number gutter and a ^^^ underline — the same text shape
// formatSourceAnnotation (pkg/dang/errors.go) prints, minus ANSI and minus
// the filename (snippet filenames are synthetic — "literate" here,
// "playground" in the wasm replay — and the source sits right above the
// report anyway). Quoted source lines and field values are
// syntax-highlighted with the same tok-* spans as everything else.
//
// docs/js/playground.js's renderErrorReport builds the same DOM client-side
// so a replay shows exactly what the build baked; the two must stay in
// lockstep.
func renderErrorReport(rep *dang.ErrorReport, stageLabel string) booklit.Content {
	var b strings.Builder
	for _, sec := range rep.Sections {
		label := stageLabel + ":"
		switch sec.Role {
		case dang.ReportCause:
			label = "caused by:"
		case dang.ReportSibling:
			label = "also failed:"
		}

		b.WriteString(`<div class="dang-error-section">`)
		b.WriteString(`<div class="dang-error-title"><span class="dang-error-label">` +
			html.EscapeString(label) + `</span> ` + html.EscapeString(sec.Message) + `</div>`)

		for _, f := range sec.Fields {
			b.WriteString(`<div class="dang-error-field">  ` + html.EscapeString(f.Name) + `: ` +
				highlightResultHTML(f.Value) + `</div>`)
		}

		if sec.Location != nil {
			b.WriteString(`<pre class="dang-error-snippet">`)
			b.WriteString(errorSnippetHTML(sec.Location, sec.Snippet))
			b.WriteString(`</pre>`)
		}

		b.WriteString(`</div>`)
	}
	return booklit.Styled{Style: "raw-html", Content: booklit.String(b.String())}
}

// errorSnippetHTML renders the location arrow and quoted source window,
// line for line the text formatSourceAnnotation prints (sans filename).
// With no resolvable snippet it degrades to the bare arrow, like annotate
// in pkg/dang/uncaught.go.
func errorSnippetHTML(loc *dang.SourceLocation, snip *dang.ErrorSnippet) string {
	var b strings.Builder
	b.WriteString(`<span class="dang-error-arrow">` +
		html.EscapeString(fmt.Sprintf("  --> %d:%d", loc.Line, loc.Column)) + `</span>`)
	if snip == nil {
		return b.String()
	}

	gutter := func(lineNum string, hl bool) string {
		cls := "dang-error-gutter"
		if hl {
			cls += " is-hl"
		}
		return `<span class="` + cls + `">` + fmt.Sprintf(" %3s | ", lineNum) + `</span>`
	}
	pipe := `<span class="dang-error-gutter">` + " " + `    |` + `</span>`

	b.WriteString("\n" + pipe + "\n")
	highlighted := highlightSnippetLines(snip.Lines)
	for i, lineHTML := range highlighted {
		lineNum := snip.StartLine + i
		if lineNum == loc.Line {
			b.WriteString(gutter(fmt.Sprintf("%d", lineNum), true) + lineHTML + "\n")
			// Underline indent mirrors formatSourceAnnotation: 1 leading
			// space + 3 gutter + " | " + column-1.
			padding := strings.Repeat(" ", 1+3+3+loc.Column-1)
			carets := strings.Repeat("^", max(1, loc.Length))
			b.WriteString(padding + `<span class="dang-error-underline">` + carets + `</span>` + "\n")
		} else {
			b.WriteString(gutter(fmt.Sprintf("%d", lineNum), false) +
				`<span class="dang-error-dim">` + lineHTML + `</span>` + "\n")
		}
	}
	b.WriteString(pipe)
	return b.String()
}

// highlightSnippetLines syntax-highlights a snippet's lines as one Dang
// fragment (so multi-line tokens keep their context), returning per-line
// HTML. Without a grammar (or cgo) the lines come back as escaped plain
// text, matching highlightResult's degradation.
func highlightSnippetLines(lines []string) []string {
	joined := strings.Join(lines, "\n")
	classes := classifyCode("dang", joined)
	classAt := func(i int) string {
		if classes == nil {
			return ""
		}
		return classes[i]
	}

	out := make([]string, 0, len(lines))
	var b strings.Builder
	offset := 0
	for li, line := range lines {
		b.Reset()
		for i := offset; i < offset+len(line); {
			cls := classAt(i)
			j := i + 1
			for j < offset+len(line) && classAt(j) == cls {
				j++
			}
			text := html.EscapeString(joined[i:j])
			if cls != "" {
				b.WriteString(`<span class="` + cls + `">` + text + `</span>`)
			} else {
				b.WriteString(text)
			}
			i = j
		}
		out = append(out, b.String())
		offset += len(line)
		if li < len(lines)-1 {
			offset++ // the joining newline
		}
	}
	return out
}
