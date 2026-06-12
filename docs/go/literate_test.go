package dangdocs

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/vito/booklit"
)

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
	if got := second.Partials["Value"]; got == nil || got.String() != "43" {
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

	// The failure block sees the session's state (x) and bakes its error.
	failed, err := render("dang-failure", "x.toUpper")
	if err != nil {
		t.Fatalf("dang-failure fence: %v", err)
	}
	errPartial := failed.Partials["Error"]
	if errPartial == nil {
		t.Fatal("dang-failure fence baked no Error partial")
	}
	if got := errPartial.String(); !strings.HasPrefix(got, "Type error: ") {
		t.Errorf("baked error %q, want a %q prefix matching playground.js's STAGE_LABEL", got, "Type error: ")
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
	if got := after.Partials["Value"]; got == nil || got.String() != "43" {
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
