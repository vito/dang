package hm

import (
	"runtime"
	"testing"
)

func TestWeakVariablesBindOnce(t *testing.T) {
	intType := NonNullType{Type: simpleType("Int")}
	strType := NonNullType{Type: simpleType("String")}

	w, cell := NewWeakVar(TypeVariable('a'))
	defer runtime.KeepAlive(cell)
	if !w.IsWeak() || w.IsRigid() {
		t.Fatalf("%v should be weak and not rigid", w)
	}

	// A failed unification binds nothing.
	if _, err := Assignable(listOf(w), simpleType("Int")); err == nil {
		t.Fatal("a list is no Int")
	}
	if _, ok := w.weakBinding(); ok {
		t.Fatal("a failed unification bound a weak variable")
	}

	// A flexible variable takes the weak one on, not the reverse.
	flex := FreshVar(0)
	if _, err := Assignable(w, flex); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.weakBinding(); ok {
		t.Fatal("a weak variable was bound to a flexible one")
	}

	// The first unification pinning it binds it for good.
	if _, err := Assignable(intType, w); err != nil {
		t.Fatal(err)
	}
	if !w.Eq(intType) || w.String() != intType.String() {
		t.Fatalf("w should be Int!, is %s", w)
	}
	if _, err := Assignable(strType, w); err == nil {
		t.Fatal("w is an Int!, not a String!")
	}
	if got := ResolveWeak(listOf(w)); !got.Eq(listOf(intType)) {
		t.Fatalf("resolved to %s", got)
	}
}

func TestWeakVariableThroughNullableView(t *testing.T) {
	intType := simpleType("Int")

	w, cell := NewWeakVar(TypeVariable('a'))
	defer runtime.KeepAlive(cell)

	// `let n: Int = xs[0]`, with xs: [w]!: w? fits Int, so w is Int!.
	if _, err := Assignable(NullableTypeVariable{TypeVariable: w}, intType); err != nil {
		t.Fatal(err)
	}
	if got := prune(w); !got.Eq(NonNullType{Type: intType}) {
		t.Fatalf("w should be Int!, is %s", got)
	}
}

func TestWeakVariableWeakensFlexibleParts(t *testing.T) {
	w, cell := NewWeakVar(TypeVariable('a'))
	defer runtime.KeepAlive(cell)

	// w := [b]! with a flexible b: b becomes weak too, so the next
	// unification pins it rather than meeting a wildcard.
	flex := FreshVar(1)
	if _, err := Assignable(listOf(flex), w); err != nil {
		t.Fatal(err)
	}
	if _, err := Assignable(listOf(NonNullType{Type: simpleType("Int")}), w); err != nil {
		t.Fatal(err)
	}
	if _, err := Assignable(listOf(NonNullType{Type: simpleType("String")}), w); err == nil {
		t.Fatalf("w is %s, which holds no String!", w)
	}
}

// listOf is a minimal list constructor for these tests.
type testList struct{ elem Type }

func listOf(t Type) Type { return testList{t} }

func (l testList) Name() string { return "[" + l.elem.String() + "]" }
func (l testList) Apply(s Subs) Substitutable {
	return testList{l.elem.Apply(s).(Type)}
}
func (l testList) FreeTypeVar() TypeVarSet { return l.elem.FreeTypeVar() }
func (l testList) Normalize(TypeVarSet, TypeVarSet) (Type, error) {
	return l, nil
}
func (l testList) Types() Types { return Types{l.elem} }
func (l testList) Eq(o Type) bool {
	ol, ok := o.(testList)
	return ok && l.elem.Eq(ol.elem)
}
func (l testList) Supertypes() []Type { return nil }
func (l testList) String() string     { return l.Name() }
