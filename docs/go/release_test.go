package dangdocs

import (
	"strings"
	"testing"

	"github.com/vito/booklit"
)

// ```output and ```error fences quote a program's result verbatim, even in
// a \literate-fences scope, where a ```dang fence would be evaluated.
func TestCodeBlockOutputFences(t *testing.T) {
	root := &booklit.Section{Path: "output-test.md"}
	Plugin{section: root}.LiterateFences()

	for language, kind := range map[string]string{"output": "ok", "error": "err"} {
		content, err := Plugin{section: root}.CodeBlock(language, booklit.Preformatted{booklit.String("not { dang\n")})
		if err != nil {
			t.Fatalf("CodeBlock(%q): %v", language, err)
		}
		styled, ok := content.(booklit.Styled)
		if !ok || styled.Style != "code-output" {
			t.Fatalf("CodeBlock(%q) = %#v, want a code-output block", language, content)
		}
		if got := styled.Partial("Kind").String(); got != kind {
			t.Errorf("CodeBlock(%q) kind = %q, want %q", language, got, kind)
		}
		if got := styled.Content.String(); got != "not { dang" {
			t.Errorf("CodeBlock(%q) content = %q, want the text verbatim without the trailing newline", language, got)
		}
	}
}

// A \pill with an unknown kind fails the build instead of rendering
// uncolored.
func TestPillKinds(t *testing.T) {
	p := Plugin{section: &booklit.Section{Path: "pill-test.md"}}
	for kind := range pillKinds {
		if _, err := p.Pill(booklit.String(kind), booklit.String("label")); err != nil {
			t.Errorf("Pill(%q): %v", kind, err)
		}
	}
	_, err := p.Pill(booklit.String("every"), booklit.String("label"))
	if err == nil || !strings.Contains(err.Error(), "want one of") {
		t.Errorf("Pill(\"every\") error = %v, want one listing the kinds", err)
	}
}

// \pane takes a label and content, optionally with a note between them.
func TestPaneArguments(t *testing.T) {
	p := Plugin{section: &booklit.Section{Path: "pane-test.md"}}
	label, note, body := booklit.String("Before"), booklit.String("v2.3"), booklit.String("code")

	plain, err := p.Pane(label, body)
	if err != nil {
		t.Fatal(err)
	}
	if styled := plain.(booklit.Styled); styled.Partial("Note") != nil || styled.Content != body {
		t.Errorf("Pane(label, body) = %#v, want no note and the body as content", styled)
	}

	noted, err := p.Pane(label, note, body)
	if err != nil {
		t.Fatal(err)
	}
	if styled := noted.(booklit.Styled); styled.Partial("Note") != note || styled.Content != body {
		t.Errorf("Pane(label, note, body) = %#v, want the note and the body as content", styled)
	}

	if _, err := p.Pane(label); err == nil {
		t.Error("Pane(label) succeeded, want an error")
	}
	if _, err := p.Pane(label, note, body, body); err == nil {
		t.Error("Pane with four arguments succeeded, want an error")
	}
}
