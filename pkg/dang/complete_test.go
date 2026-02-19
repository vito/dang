package dang

import (
	"reflect"
	"testing"

	"github.com/vito/dang/pkg/hm"
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

func TestSplitArgExpr(t *testing.T) {
	tests := []struct {
		text         string
		wantFuncExpr string
		wantPartial  string
		wantProvided []string
		wantOK       bool
	}{
		// Simple function call with no args yet
		{"foo(", "foo", "", nil, true},
		// Partial arg name
		{"foo(addr", "foo", "addr", nil, true},
		// Method call
		{"container.from(", "container.from", "", nil, true},
		// Method call with partial
		{"container.from(addr", "container.from", "addr", nil, true},
		// Already provided named arg
		{"foo(name: x, addr", "foo", "addr", []string{"name"}, true},
		// Nested call - should find innermost
		{"foo(bar(", "bar", "", nil, true},
		// No parens
		{"foo", "", "", nil, false},
		// Empty before paren
		{"(", "", "", nil, false},
		// Closing paren balanced
		{"foo(1)", "", "", nil, false},
		// Inside brackets - should not suggest args
		{`foo([`, "", "", nil, false},
		{`foo(["go", `, "", "", nil, false},
		{`foo([x`, "", "", nil, false},
		// After brackets - should suggest args
		{`foo(["go"], `, "foo", "", []string(nil), true},
		{`foo(["go"], plat`, "foo", "plat", []string(nil), true},
		// In value position after "name:" - should not suggest args
		{`foo(args: `, "", "", nil, false},
		{`foo(args: bar`, "", "", nil, false},
		{`foo(name: x, args:`, "", "", nil, false},
		// But after comma following a value - should suggest args
		{`foo(args: x, `, "foo", "", []string{"args"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			funcExpr, partial, provided, ok := splitArgExpr(tt.text)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if funcExpr != tt.wantFuncExpr {
				t.Errorf("funcExpr = %q, want %q", funcExpr, tt.wantFuncExpr)
			}
			if partial != tt.wantPartial {
				t.Errorf("partial = %q, want %q", partial, tt.wantPartial)
			}
			if !reflect.DeepEqual(provided, tt.wantProvided) {
				t.Errorf("provided = %v, want %v", provided, tt.wantProvided)
			}
		})
	}
}

func TestArgsOf(t *testing.T) {
	// Build a function type: (address: String!, platform: String) -> Container
	args := NewRecordType("")
	args.Add("address", hm.NewScheme(nil, hm.NonNullType{Type: StringType}))
	args.Add("platform", hm.NewScheme(nil, StringType))
	args.DocStrings = map[string]string{
		"address":  "Image's address from its registry.",
		"platform": "Platform to resolve.",
	}
	containerType := NewModule("Container", ObjectKind)
	fnType := hm.NewFnType(args, containerType)

	t.Run("all args", func(t *testing.T) {
		completions := ArgsOf(fnType, "", nil)
		if len(completions) != 2 {
			t.Fatalf("got %d completions, want 2", len(completions))
		}
		if completions[0].Label != "address" {
			t.Errorf("label = %q, want %q", completions[0].Label, "address")
		}
		if !completions[0].IsArg {
			t.Error("expected IsArg = true")
		}
		if completions[0].Documentation != "Image's address from its registry." {
			t.Errorf("doc = %q", completions[0].Documentation)
		}
	})

	t.Run("filter by prefix", func(t *testing.T) {
		completions := ArgsOf(fnType, "addr", nil)
		if len(completions) != 1 {
			t.Fatalf("got %d completions, want 1", len(completions))
		}
		if completions[0].Label != "address" {
			t.Errorf("label = %q, want %q", completions[0].Label, "address")
		}
	})

	t.Run("exclude provided", func(t *testing.T) {
		completions := ArgsOf(fnType, "", []string{"address"})
		if len(completions) != 1 {
			t.Fatalf("got %d completions, want 1", len(completions))
		}
		if completions[0].Label != "platform" {
			t.Errorf("label = %q, want %q", completions[0].Label, "platform")
		}
	})

	t.Run("no match", func(t *testing.T) {
		completions := ArgsOf(fnType, "xyz", nil)
		if len(completions) != 0 {
			t.Fatalf("got %d completions, want 0", len(completions))
		}
	})

	t.Run("wrapped in NonNull", func(t *testing.T) {
		wrapped := hm.NonNullType{Type: fnType}
		completions := ArgsOf(wrapped, "", nil)
		if len(completions) != 2 {
			t.Fatalf("got %d completions, want 2", len(completions))
		}
	})
}
