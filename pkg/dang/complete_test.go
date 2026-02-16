package dang

import (
	"testing"
)

func TestSplitDotExpr(t *testing.T) {
	tests := []struct {
		text        string
		wantDotIdx  int
		wantRecv    string
		wantPartial string
	}{
		// Simple dotted access
		{"foo.ba", 3, "foo", "ba"},
		// Chained dots
		{"a.b.c", 3, "a.b", "c"},
		// No dot
		{"foobar", -1, "", ""},
		// Dot at end with no partial
		{"foo.", 3, "foo", ""},
		// Function call receiver
		{"foo(1).ba", 6, "foo(1)", "ba"},
		// Nested parens
		{"foo(a(b)).x", 9, "foo(a(b))", "x"},
		// Bracket receiver
		{`apko.wolfi(["go"]).std`, 18, `apko.wolfi(["go"])`, "std"},
		// Chained calls
		{"a.b(1).c(2).d", 11, "a.b(1).c(2)", "d"},
		// Just a dot after parens, no partial
		{"foo(1).", 6, "foo(1)", ""},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			dotIdx, recv, partial := splitDotExpr(tt.text)
			if dotIdx != tt.wantDotIdx {
				t.Errorf("dotIdx = %d, want %d", dotIdx, tt.wantDotIdx)
			}
			if recv != tt.wantRecv {
				t.Errorf("receiver = %q, want %q", recv, tt.wantRecv)
			}
			if partial != tt.wantPartial {
				t.Errorf("partial = %q, want %q", partial, tt.wantPartial)
			}
		})
	}
}
