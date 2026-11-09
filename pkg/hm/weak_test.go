package hm

import (
	"runtime"
	"testing"
)

func TestWeakVariablesBindOnce(t *testing.T) {
	intType := NonNullType{Type: simpleType("Int")}
	strType := NonNullType{Type: simpleType("String")}

	w, cell := NewWeakVar(TypeVariable('a'), nil)
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

	w, cell := NewWeakVar(TypeVariable('a'), nil)
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
	w, cell := NewWeakVar(TypeVariable('a'), nil)
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

// The substitution binding a weak variable to a type with flexible parts
// maps those parts to the weak variables they became, so a caller that
// later pins one (through the substitution) pins the weak binding's part.
func TestWeakBindingsReportWeakenedParts(t *testing.T) {
	w, cell := NewWeakVar(TypeVariable('a'), nil)
	defer runtime.KeepAlive(cell)

	flex := FreshVar(2)
	subs, err := Assignable(w, NonNullType{Type: flex})
	if err != nil {
		t.Fatal(err)
	}
	part := flex.Apply(subs).(Type)
	if v, ok := part.(TypeVariable); !ok || !v.IsWeak() {
		t.Fatalf("%s should stand for a weak variable, is %s", flex, part)
	}
	intType := simpleType("Int")
	if _, err := Assignable(intType, part); err != nil {
		t.Fatal(err)
	}
	if got := ResolveWeak(w); !got.Eq(NonNullType{Type: intType}) {
		t.Fatalf("w should be Int!, is %s", got)
	}
}

// A weak variable may hold only the rigid variables in scope where its
// binding was declared: a generic function's own `a`, for a `let` in its
// body, but no `a` for a field, which outlives every method call.
func TestWeakVariablesKeepRigidOnesInScope(t *testing.T) {
	a := Rigid(TypeVariable('a'))

	local, localCell := NewWeakVar(TypeVariable('a'), NewTypeVarSet(a))
	defer runtime.KeepAlive(localCell)
	if _, err := Assignable(a, local); err != nil {
		t.Fatalf("a local of a's function may hold an a: %v", err)
	}

	field, fieldCell := NewWeakVar(TypeVariable('b'), nil)
	defer runtime.KeepAlive(fieldCell)
	if _, err := Assignable(listOf(a), field); err == nil {
		t.Fatalf("a field held an a: %s", field)
	}
	if _, ok := field.weakBinding(); ok {
		t.Fatal("a refused binding was committed")
	}

	// Through another weak variable: binding the field to the local's
	// (still unbound) variable narrows what the local may hold.
	other, otherCell := NewWeakVar(TypeVariable('c'), NewTypeVarSet(a))
	defer runtime.KeepAlive(otherCell)
	if _, err := Assignable(listOf(other), field); err != nil {
		t.Fatal(err)
	}
	if _, err := Assignable(a, other); err == nil {
		t.Fatal("an a reached a field through a local")
	}
}

// An open weak variable (an expression's, like an empty literal's element
// type) may hold any rigid variable, but taken into a binding's weak
// variable it may hold only what the binding may.
func TestOpenWeakVariables(t *testing.T) {
	a := Rigid(TypeVariable('a'))

	expr, exprCell := NewOpenWeakVar(TypeVariable('e'))
	defer runtime.KeepAlive(exprCell)
	if !expr.IsOpenWeak() {
		t.Fatal("expected an open weak variable")
	}
	if _, err := Assignable(a, expr); err != nil {
		t.Fatalf("an expression's variable may hold any rigid variable: %v", err)
	}
	if expr.IsOpenWeak() {
		t.Fatal("a bound variable is no longer open")
	}
	if got := ResolveWeak(listOf(expr)); !got.Eq(listOf(a)) {
		t.Fatalf("resolved to %s", got)
	}

	// Bound to a binding's variable, which may hold no rigid variable,
	// the expression's flexible parts are the binding's too.
	inner, innerCell := NewOpenWeakVar(TypeVariable('i'))
	defer runtime.KeepAlive(innerCell)
	field, fieldCell := NewWeakVar(TypeVariable('f'), nil)
	defer runtime.KeepAlive(fieldCell)
	if _, err := Assignable(listOf(inner), field); err != nil {
		t.Fatal(err)
	}
	if _, err := Assignable(a, inner); err == nil {
		t.Fatal("an a reached a field through an expression")
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
