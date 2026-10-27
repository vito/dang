package dang

import (
	"reflect"
	"testing"
)

func TestSyntaxBlueprintRejectsInteriorPointers(t *testing.T) {
	type storage struct {
		Value NamedTypeNode
		Alias *NamedTypeNode
	}
	original := &storage{}
	original.Alias = &original.Value
	if _, err := syntaxBlueprint(reflect.ValueOf(original)); err == nil {
		t.Fatal("interior pointer requires an unsupported reconstruction")
	}
}

func TestSyntaxBlueprintPreservesAliases(t *testing.T) {
	shared := &NamedTypeNode{}
	original := struct{ First, Second *NamedTypeNode }{shared, shared}
	factory, err := syntaxBlueprint(reflect.ValueOf(original))
	if err != nil {
		t.Fatal(err)
	}
	first := cloneSyntaxForTest(t, factory, "one.dang").Interface().(struct{ First, Second *NamedTypeNode })
	second := cloneSyntaxForTest(t, factory, "two.dang").Interface().(struct{ First, Second *NamedTypeNode })
	if first.First != first.Second || second.First != second.Second {
		t.Fatal("internal alias lost")
	}
	if first.First == shared || first.First == second.First {
		t.Fatal("calls share syntax")
	}
}

func TestSyntaxBlueprintPreservesSliceStorage(t *testing.T) {
	original := make([]int, 2, 3)
	original[0], original[1] = 1, 2
	original[:3][2] = 3
	value := struct{ First, Second []int }{original, original[:1]}
	factory, err := syntaxBlueprint(reflect.ValueOf(value))
	if err != nil {
		t.Fatal(err)
	}
	first := cloneSyntaxForTest(t, factory, "one.dang").Interface().(struct{ First, Second []int })
	second := cloneSyntaxForTest(t, factory, "two.dang").Interface().(struct{ First, Second []int })
	first.First[0] = 9
	if first.Second[0] != 9 {
		t.Fatal("shared backing storage was lost")
	}
	if original[0] != 1 || second.First[0] != 1 {
		t.Fatal("calls share backing storage")
	}
	if len(first.Second) != 1 || cap(first.Second) != 3 || first.Second[:3][2] != 3 {
		t.Fatal("slice view changed")
	}
	overlapping := struct{ First, Second []int }{original, original[1:]}
	if _, err := syntaxBlueprint(reflect.ValueOf(overlapping)); err == nil {
		t.Fatal("unsupported overlapping views accepted")
	}
}

func cloneSyntaxForTest(t testing.TB, factory parseBlueprint, filename string) reflect.Value {
	t.Helper()
	value, err := factory.copy(reflect.ValueOf(filename))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
