package hm

import "testing"

type simpleType string

func (t simpleType) Name() string { return string(t) }

func (t simpleType) Apply(Subs) Substitutable { return t }

func (t simpleType) FreeTypeVar() TypeVarSet { return NewTypeVarSet() }

func (t simpleType) Normalize(TypeVarSet, TypeVarSet) (Type, error) { return t, nil }

func (t simpleType) Types() Types { return nil }

func (t simpleType) Eq(other Type) bool {
	otherSimple, ok := other.(simpleType)
	return ok && t == otherSimple
}

func (t simpleType) Supertypes() []Type { return nil }

func (t simpleType) String() string { return string(t) }

func TestComposeMergesNullableConstraint(t *testing.T) {
	intType := simpleType("Int")

	left := Subs{
		TypeVariable('a'): NonNullType{Type: intType},
	}
	right := Subs{
		TypeVariable('a'): NullableTypeVariable{TypeVariable: TypeVariable('n')},
	}

	got, err := left.Compose(right)
	if err != nil {
		t.Fatalf("compose failed: %v", err)
	}

	merged, found := got.Get(TypeVariable('a'))
	if !found {
		t.Fatalf("missing merged substitution for a")
	}
	if !merged.Eq(intType) {
		t.Fatalf("expected nullable Int, got %s", merged)
	}
}

func TestComposeRejectsIncompatibleRepeatedBinding(t *testing.T) {
	left := Subs{
		TypeVariable('a'): NonNullType{Type: simpleType("Int")},
	}
	right := Subs{
		TypeVariable('a'): NonNullType{Type: simpleType("String")},
	}

	if _, err := left.Compose(right); err == nil {
		t.Fatalf("expected incompatible repeated binding to fail")
	}
}
