package main

import "testing"

// These excerpts are taken verbatim from the rendered HTML (rendered text, not
// markdown) to exercise the fuzzy match against the real markdown sources.
func TestResolveRealSources(t *testing.T) {
	r := newResolver("../lit")

	cases := []struct {
		page    string
		excerpt string
		want    string // file the match must land in
	}{
		{
			page:    "/fields.html",
			excerpt: "The pub and let keywords declare fields in the current scope:",
			want:    "../lit/language/fields.md",
		},
		{
			// Rendered: the [#mutation] link became "Mutation and copy-on-write".
			page:    "/fields.html",
			excerpt: "These keywords distinguish the expression from Mutation and copy-on-write, which updates an already-declared field.",
			want:    "../lit/language/fields.md",
		},
	}

	for _, c := range cases {
		loc := r.resolve(c.page, c.excerpt)
		if !loc.ok {
			t.Errorf("resolve(%q) unresolved, want %s", c.excerpt, c.want)
			continue
		}
		if loc.file != c.want {
			t.Errorf("resolve(%q) = %s:%d, want file %s", c.excerpt, loc.file, loc.line, c.want)
			continue
		}
		t.Logf("resolve(%q) -> %s:%d", c.excerpt, loc.file, loc.line)
	}
}
