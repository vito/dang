package hm

import (
	"strings"
	"testing"
)

// genericType is a test stand-in for a nominal generic application. Two
// genericTypes share a constructor iff they have the same base string.
type genericType struct {
	base string
	args []Type
}

func (g *genericType) Name() string { return g.String() }
func (g *genericType) Apply(subs Subs) Substitutable {
	args := make([]Type, len(g.args))
	for i, a := range g.args {
		args[i] = a.Apply(subs).(Type)
	}
	return &genericType{base: g.base, args: args}
}
func (g *genericType) FreeTypeVar() TypeVarSet {
	var tvs TypeVarSet
	for _, a := range g.args {
		tvs = a.FreeTypeVar().Union(tvs)
	}
	return tvs
}
func (g *genericType) Normalize(k, v TypeVarSet) (Type, error) { return g, nil }
func (g *genericType) Types() Types {
	out := make(Types, len(g.args))
	copy(out, g.args)
	return out
}
func (g *genericType) Eq(other Type) bool {
	og, ok := other.(*genericType)
	if !ok || g.base != og.base || len(g.args) != len(og.args) {
		return false
	}
	for i, a := range g.args {
		if !a.Eq(og.args[i]) {
			return false
		}
	}
	return true
}
func (g *genericType) Supertypes() []Type { return nil }
func (g *genericType) SameTypeConstructor(other Type) bool {
	og, ok := other.(*genericType)
	return ok && g.base == og.base
}
func (g *genericType) String() string {
	var b strings.Builder
	b.WriteString(g.base)
	b.WriteByte('[')
	for i, a := range g.args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(a.String())
	}
	b.WriteByte(']')
	return b.String()
}

func TestAssignableInvariantGenericMatchingArgs(t *testing.T) {
	intT := simpleType("Int")
	box1 := &genericType{base: "Box", args: []Type{intT}}
	box2 := &genericType{base: "Box", args: []Type{intT}}

	if _, err := Assignable(box1, box2); err != nil {
		t.Fatalf("Box[Int] should unify with Box[Int]: %v", err)
	}
}

func TestAssignableInvariantGenericDifferentArgs(t *testing.T) {
	intT := simpleType("Int")
	strT := simpleType("String")
	box1 := &genericType{base: "Box", args: []Type{intT}}
	box2 := &genericType{base: "Box", args: []Type{strT}}

	if _, err := Assignable(box1, box2); err == nil {
		t.Fatalf("Box[Int] should not unify with Box[String]")
	}
}

func TestAssignableInvariantGenericDifferentBases(t *testing.T) {
	intT := simpleType("Int")
	box := &genericType{base: "Box", args: []Type{intT}}
	other := &genericType{base: "Container", args: []Type{intT}}

	if _, err := Assignable(box, other); err == nil {
		t.Fatalf("Box[Int] should not unify with Container[Int]")
	}
}

func TestAssignableInvariantBindsTypeVariable(t *testing.T) {
	intT := simpleType("Int")
	have := &genericType{base: "Box", args: []Type{intT}}
	want := &genericType{base: "Box", args: []Type{TypeVariable('a')}}

	subs, err := Assignable(have, want)
	if err != nil {
		t.Fatalf("Box[Int] should unify with Box[a]: %v", err)
	}
	bound, found := subs.Get(TypeVariable('a'))
	if !found || !bound.Eq(intT) {
		t.Fatalf("expected a := Int, got %v (found=%v)", bound, found)
	}
}

// Even though NonNull(Int) is a subtype of Int, a generic argument must
// be invariant: Box[NonNull(Int)] is not the same type as Box[Int].
func TestAssignableInvariantBlocksSubtyping(t *testing.T) {
	intT := simpleType("Int")
	nnIntT := NonNullType{Type: intT}
	have := &genericType{base: "Box", args: []Type{nnIntT}}
	want := &genericType{base: "Box", args: []Type{intT}}

	if _, err := Assignable(have, want); err == nil {
		t.Fatalf("Box[Int!] should not be assignable to Box[Int] (invariance)")
	}
}

// Reflect-based composite unification must still reject mixing a
// TypeConstructor with a non-TypeConstructor that happens to expose
// the same number of component types.
func TestAssignableRejectsConstructorAgainstNonConstructor(t *testing.T) {
	intT := simpleType("Int")
	box := &genericType{base: "Box", args: []Type{intT}}
	nn := NonNullType{Type: intT}

	if _, err := Assignable(box, nn); err == nil {
		t.Fatalf("Box[Int] should not unify with NonNull(Int)")
	}
	if _, err := Assignable(nn, box); err == nil {
		t.Fatalf("NonNull(Int) should not unify with Box[Int]")
	}
}
