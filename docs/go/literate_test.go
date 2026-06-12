package dangdocs

import (
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
