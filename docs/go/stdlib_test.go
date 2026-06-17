package dangdocs

import (
	"testing"

	"github.com/vito/booklit"
)

func TestReplacementTag(t *testing.T) {
	cases := map[string]string{
		"JSON.encode":     "stdlib-JSON-encode",
		"JSON.decode":     "stdlib-JSON-decode",
		"YAML.decode":     "stdlib-YAML-decode",
		"String.toBase64": "stdlib-String-toBase64",
		"loop":            "stdlib-fn-loop", // bare name => top-level function card
	}
	for replacement, want := range cases {
		if got := replacementTag(replacement); got != want {
			t.Errorf("replacementTag(%q) = %q, want %q", replacement, got, want)
		}
	}
}

func TestDeprecatedBadgeLinksReplacement(t *testing.T) {
	var p Plugin

	// With a replacement, the badge appends a reference to the replacement's card.
	badge := p.deprecatedBadge("JSON.encode")
	seq, ok := badge.(booklit.Sequence)
	if !ok {
		t.Fatalf("expected a Sequence, got %T", badge)
	}
	var ref *booklit.Reference
	for _, c := range seq {
		if r, isRef := c.(*booklit.Reference); isRef {
			ref = r
		}
	}
	if ref == nil {
		t.Fatalf("badge has no reference to the replacement: %#v", seq)
	}
	if ref.TagName != "stdlib-JSON-encode" {
		t.Errorf("reference tag = %q, want %q", ref.TagName, "stdlib-JSON-encode")
	}
	if ref.Content.String() != "JSON.encode" {
		t.Errorf("reference display = %q, want %q", ref.Content.String(), "JSON.encode")
	}

	// Without a replacement, it is just the plain marker — no link.
	bare := p.deprecatedBadge("")
	bareSeq, ok := bare.(booklit.Sequence)
	if !ok {
		t.Fatalf("expected a Sequence, got %T", bare)
	}
	for _, c := range bareSeq {
		if _, isRef := c.(*booklit.Reference); isRef {
			t.Errorf("badge with no replacement should not contain a reference: %#v", bareSeq)
		}
	}
	if bareSeq.String() != "@deprecated" {
		t.Errorf("bare badge = %q, want %q", bareSeq.String(), "@deprecated")
	}
}
