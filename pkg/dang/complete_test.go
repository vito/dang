package dang

import (
	"testing"

	"github.com/vito/dang/pkg/hm"
)

func TestMembersOfBuiltinTypes(t *testing.T) {
	// Ensure builtin method types are registered
	registerBuiltinTypes()

	t.Run("string methods", func(t *testing.T) {
		completions := MembersOf(hm.NonNullType{Type: StringType}, "")
		if len(completions) == 0 {
			t.Fatal("expected completions for String type")
		}
		found := false
		for _, c := range completions {
			if c.Label == "split" {
				found = true
				if !c.IsFunction {
					t.Error("expected split to be a function")
				}
				break
			}
		}
		if !found {
			t.Error("expected 'split' in String completions")
		}
	})

	t.Run("string methods filtered", func(t *testing.T) {
		completions := MembersOf(hm.NonNullType{Type: StringType}, "sp")
		if len(completions) != 1 || completions[0].Label != "split" {
			t.Errorf("got %v, want [split]", completions)
		}
	})

	t.Run("nullable string methods", func(t *testing.T) {
		completions := MembersOf(StringType, "split")
		if len(completions) != 1 || completions[0].Label != "split" {
			t.Errorf("got %v, want [split]", completions)
		}
	})

	t.Run("list methods non-null", func(t *testing.T) {
		listType := hm.NonNullType{Type: ListType{Type: hm.NonNullType{Type: IntType}}}
		completions := MembersOf(listType, "")
		if len(completions) == 0 {
			t.Fatal("expected completions for list type")
		}
		names := map[string]bool{}
		for _, c := range completions {
			names[c.Label] = true
		}
		for _, want := range []string{"map", "filter", "reduce", "length", "contains"} {
			if !names[want] {
				t.Errorf("expected %q in list completions, got %v", want, names)
			}
		}
	})

	t.Run("list methods nullable", func(t *testing.T) {
		listType := ListType{Type: hm.NonNullType{Type: IntType}}
		completions := MembersOf(listType, "red")
		if len(completions) != 1 || completions[0].Label != "reduce" {
			t.Errorf("got %v, want [reduce]", completions)
		}
	})

	t.Run("int methods", func(t *testing.T) {
		completions := MembersOf(hm.NonNullType{Type: IntType}, "")
		// Int may have few or no methods currently, just verify no crash
		_ = completions
	})

	t.Run("object type still works", func(t *testing.T) {
		mod := NewModule("TestObj", ObjectKind)
		mod.Add("myField", hm.NewScheme(nil, hm.NonNullType{Type: StringType}))
		mod.SetVisibility("myField", PublicVisibility)
		completions := MembersOf(hm.NonNullType{Type: mod}, "")
		if len(completions) != 1 || completions[0].Label != "myField" {
			t.Errorf("got %v, want [myField]", completions)
		}
	})
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
