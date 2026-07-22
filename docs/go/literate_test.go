package dangdocs

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vito/booklit"
)

// valueText returns the plain text of a literate block's Value partial,
// stripping the syntax-highlight markup the result is now rendered with
// (tags and HTML entities).
var htmlTagRE = regexp.MustCompile(`<[^>]*>`)

func valueText(c booklit.Content) string {
	if c == nil {
		return ""
	}
	return html.UnescapeString(htmlTagRE.ReplaceAllString(c.String(), ""))
}

// \literate-fences covers the section it's called in and its sub-sections —
// not the parent or siblings.
func TestLiterateFencesScope(t *testing.T) {
	root := &booklit.Section{Path: "scope-test.md"}
	marked := &booklit.Section{Parent: root}
	sibling := &booklit.Section{Parent: root}
	sub := &booklit.Section{Parent: marked}

	Plugin{section: marked}.LiterateFences()

	cases := []struct {
		name string
		sec  *booklit.Section
		want bool
	}{
		{"root", root, false},
		{"marked section", marked, true},
		{"sub-section of marked", sub, true},
		{"sibling", sibling, false},
	}
	for _, c := range cases {
		if got := literateFencesEnabled(c.sec); got != c.want {
			t.Errorf("%s: literateFencesEnabled = %v, want %v", c.name, got, c.want)
		}
	}
}

// In a \literate-fences scope a ```dang fence evaluates as a literate block,
// chaining state with earlier fences in the same file; ```dang-static stays a
// plain highlighted block, as does ```dang outside the scope.
func TestCodeBlockLiterateRouting(t *testing.T) {
	root := &booklit.Section{Path: "routing-test.md"}
	lit := &booklit.Section{Parent: root}
	plain := &booklit.Section{Parent: root}

	Plugin{section: lit}.LiterateFences()

	render := func(sec *booklit.Section, language, source string) booklit.Styled {
		t.Helper()
		content, err := Plugin{section: sec}.CodeBlock(language, booklit.Preformatted{booklit.String(source)})
		if err != nil {
			t.Fatalf("CodeBlock(%q, %q): %v", language, source, err)
		}
		styled, ok := content.(booklit.Styled)
		if !ok {
			t.Fatalf("CodeBlock(%q, %q) returned %T, want booklit.Styled", language, source, content)
		}
		return styled
	}

	first := render(lit, "dang", "let x = 6 * 7")
	if first.Style != "dang-literate" {
		t.Errorf("dang fence in literate scope: style %q, want dang-literate", first.Style)
	}

	second := render(lit, "dang", "x + 1")
	if got := second.Partials["Value"]; got == nil || valueText(got) != "43" {
		t.Errorf("second fence should see first fence's `x`: Value = %v, want 43", got)
	}

	static := render(lit, "dang-static", "definitely not dang ((")
	if static.Style != booklit.StyleCodeBlock {
		t.Errorf("dang-static fence: style %q, want %q", static.Style, booklit.StyleCodeBlock)
	}

	outside := render(plain, "dang", "undefinedNameNeverEvaluated")
	if outside.Style != booklit.StyleCodeBlock {
		t.Errorf("dang fence outside scope: style %q, want %q", outside.Style, booklit.StyleCodeBlock)
	}
}

// A literate block's result is echoed as a quoted Dang literal (via Repr) and
// highlighted under the same grammar as the input, so the baked output matches
// what the reader sees after client-side enhancement.
func TestLiterateResultQuotedAndHighlighted(t *testing.T) {
	root := &booklit.Section{Path: "result-test.md"}
	lit := &booklit.Section{Parent: root}
	Plugin{section: lit}.LiterateFences()

	content, err := Plugin{section: lit}.CodeBlock("dang", booklit.Preformatted{booklit.String(`"hi"`)})
	if err != nil {
		t.Fatalf("CodeBlock: %v", err)
	}
	value := content.(booklit.Styled).Partials["Value"]
	if value == nil {
		t.Fatal("string result baked no Value partial")
	}
	// Quoted (Repr), not the bare `hi` that String() would produce.
	if got := valueText(value); got != `"hi"` {
		t.Errorf("result text = %q, want %q", got, `"hi"`)
	}
	// Highlighted as a string token (same classes as the input).
	if html := value.String(); !strings.Contains(html, `class="tok-string"`) {
		t.Errorf("result not highlighted as a string: %s", html)
	}
}

// A ```dang-failure fence is required to fail: it bakes the error it raises
// (labelled like the playground's stage labels), runs against forks of the
// session so nothing it declares leaks into later fences, and a snippet that
// succeeds is a build error. Outside a literate scope it stays a plain
// highlighted block.
func TestCodeBlockFailureRouting(t *testing.T) {
	root := &booklit.Section{Path: "failure-routing-test.md"}
	lit := &booklit.Section{Parent: root}
	plain := &booklit.Section{Parent: root}

	Plugin{section: lit}.LiterateFences()

	render := func(language, source string) (booklit.Styled, error) {
		t.Helper()
		content, err := Plugin{section: lit}.CodeBlock(language, booklit.Preformatted{booklit.String(source)})
		if err != nil {
			return booklit.Styled{}, err
		}
		styled, ok := content.(booklit.Styled)
		if !ok {
			t.Fatalf("CodeBlock(%q, %q) returned %T, want booklit.Styled", language, source, content)
		}
		return styled, nil
	}

	if _, err := render("dang", "let x = 6 * 7"); err != nil {
		t.Fatalf("seeding session: %v", err)
	}

	// The failure block sees the session's state (x) and bakes its error as a
	// structured report: a header labeled like playground.js's STAGE_LABEL
	// (errorReportHtml must render the same DOM on replay), then the annotated
	// source snippet with gutter and underline.
	failed, err := render("dang-failure", "x.toUpper")
	if err != nil {
		t.Fatalf("dang-failure fence: %v", err)
	}
	errPartial := failed.Partials["Error"]
	if errPartial == nil {
		t.Fatal("dang-failure fence baked no Error partial")
	}
	baked := errPartial.String()
	if want := `<span class="dang-error-label">Type error:</span>`; !strings.Contains(baked, want) {
		t.Errorf("baked error %q missing header label %q", baked, want)
	}
	if want := `<span class="dang-error-arrow">  --&gt; 1:`; !strings.Contains(baked, want) {
		t.Errorf("baked error %q missing location arrow %q", baked, want)
	}
	if !strings.Contains(baked, `dang-error-underline`) || !strings.Contains(baked, "^") {
		t.Errorf("baked error %q missing the ^^^ underline", baked)
	}
	// The failing line is quoted (split across tok-* spans, so compare text).
	if text := valueText(errPartial); !strings.Contains(text, "x.toUpper") {
		t.Errorf("baked error text %q does not quote the failing source line", text)
	}
	if failed.Partials["Value"] != nil {
		t.Errorf("dang-failure fence baked a Value: %v", failed.Partials["Value"])
	}

	// Declarations made before the failure stay in the discarded fork.
	if _, err := render("dang-failure", "let leaked = 1\nleaked.toUpper"); err != nil {
		t.Fatalf("dang-failure fence with declaration: %v", err)
	}
	if _, err := render("dang", "leaked"); err == nil {
		t.Error("a dang-failure fence's declaration leaked into the session")
	}

	// The session itself is unharmed: later fences still chain.
	after, err := render("dang", "x + 1")
	if err != nil {
		t.Fatalf("fence after failures: %v", err)
	}
	if got := after.Partials["Value"]; got == nil || valueText(got) != "43" {
		t.Errorf("fence after failures: Value = %v, want 43", got)
	}

	// A succeeding snippet in a dang-failure fence fails the build.
	if _, err := render("dang-failure", "1 + 1"); err == nil {
		t.Error("dang-failure fence with a succeeding snippet did not error")
	}

	// Outside a literate scope, dang-failure is just a highlighted block.
	content, err := Plugin{section: plain}.CodeBlock("dang-failure", booklit.Preformatted{booklit.String("definitely not dang ((")})
	if err != nil {
		t.Fatalf("dang-failure outside literate scope: %v", err)
	}
	if styled, ok := content.(booklit.Styled); !ok || styled.Style != booklit.StyleCodeBlock {
		t.Errorf("dang-failure outside literate scope: got %v, want %q", content, booklit.StyleCodeBlock)
	}
}

// A failure whose raise site lives in an earlier fence quotes that fence's
// source: blocks parse under per-block filenames and the session records
// each block's text, so cross-fence locations resolve to the right snippet
// instead of misquoting the failing fence (or dangling with no snippet).
func TestFailureQuotesEarlierFence(t *testing.T) {
	root := &booklit.Section{Path: "cross-fence-test.md"}
	lit := &booklit.Section{Parent: root}
	Plugin{section: lit}.LiterateFences()

	render := func(language, source string) (booklit.Styled, error) {
		t.Helper()
		content, err := Plugin{section: lit}.CodeBlock(language, booklit.Preformatted{booklit.String(source)})
		if err != nil {
			return booklit.Styled{}, err
		}
		return content.(booklit.Styled), nil
	}

	if _, err := render("dang", `boom: String! { raise "kapow" }`); err != nil {
		t.Fatalf("defining fence: %v", err)
	}
	failed, err := render("dang-failure", "boom")
	if err != nil {
		t.Fatalf("dang-failure fence: %v", err)
	}
	text := valueText(failed.Partials["Error"])
	if !strings.Contains(text, "kapow") {
		t.Fatalf("baked error %q missing the raised message", text)
	}
	if !strings.Contains(text, `raise "kapow"`) {
		t.Errorf("baked error %q does not quote the raise site from the earlier fence", text)
	}
}

// Booklit's dev server re-loads the whole book on every page request,
// concurrently across requests, and Dang scopes are not safe for concurrent
// use — so each load must get its own sessions. Each goroutine here simulates
// one load evaluating the same source file; `go test -race` fails if loads
// share scopes. The loads declare distinct names: concurrent loads of a real
// book sit at different blocks of the page, and identical sources fail fast
// in Infer without the scope writes that make the detector fire.
func TestLiterateSessionsScopedPerLoad(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			root := &booklit.Section{Path: "blocks.md"}
			sess := literateSessionFor(root)
			if _, _, err := literateEval(fmt.Sprintf("let nums%d = [1, 2, 3]", i), sess); err != nil {
				t.Error(err)
				return
			}

			// An inline \section shares the file path but is its own Section;
			// the notebook chain spans it, so it must get the same session.
			sub := &booklit.Section{Parent: root}
			if subSess := literateSessionFor(sub); subSess != sess {
				t.Error("inline subsection got a different session than its file")
				return
			}

			// Earlier blocks' definitions are in scope for later blocks.
			if _, _, err := literateEval(fmt.Sprintf("nums%d.map { x => x + 1 }", i), sess); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

// Re-loading the same file in a new load must start the chain fresh rather
// than reusing (and accumulating into) the previous load's scopes.
func TestLiterateSessionsFreshPerLoad(t *testing.T) {
	first := literateSessionFor(&booklit.Section{Path: "blocks.md"})
	second := literateSessionFor(&booklit.Section{Path: "blocks.md"})
	if first == second {
		t.Error("new load reused the previous load's session")
	}
}
