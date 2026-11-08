package hm

import (
	"errors"
	"runtime"
	"testing"
)

// testOrdered makes Int! and lists of non-null Ordered elements Ordered;
// String! has no order. testPositive is a second constraint, which only Int!
// satisfies.
var testOrdered, testPositive Constraint

func init() {
	testOrdered = RegisterConstraint("Ordered", func(t Type) ([]Need, string) {
		var needs []Need
		why := testOrder(t, false, &needs)
		return needs, why
	})
	testPositive = RegisterConstraint("Positive", func(t Type) ([]Need, string) {
		if nn, ok := t.(NonNullType); ok {
			if v, ok := nn.Type.(TypeVariable); ok && !v.IsRigid() {
				return []Need{{Var: v, NonNull: true}}, ""
			}
			if nn.Type == simpleType("Int") {
				return nil, ""
			}
		}
		return nil, "not an Int!"
	})
}

func testOrder(t Type, nonNull bool, needs *[]Need) string {
	if nn, ok := t.(NonNullType); ok {
		return testOrder(nn.Type, true, needs)
	}
	switch v := t.(type) {
	case TypeVariable:
		if v.IsRigid() {
			if !v.Has(testOrdered) {
				return "unbounded"
			}
			if !nonNull {
				return "may be null"
			}
			return ""
		}
		*needs = append(*needs, Need{Var: v, NonNull: nonNull})
		return ""
	case NullableTypeVariable:
		return "may be null"
	}
	if !nonNull {
		return "may be null"
	}
	switch v := t.(type) {
	case simpleType:
		if v == "Int" {
			return ""
		}
	case testList:
		return testOrder(v.elem, false, needs)
	}
	return "no order"
}

func TestBoundsSurviveCopies(t *testing.T) {
	a := Bound(TypeVariable('a'), testOrdered)
	if !Freshen(a).Has(testOrdered) || !Rigid(a).Has(testOrdered) {
		t.Fatal("a copy of a bounded variable lost its bound")
	}
	if !a.IsWritten() || Freshen(a).IsWritten() {
		t.Fatal("the bound changed whether a variable is written")
	}
	if Freshen(TypeVariable('a')).Has(testOrdered) {
		t.Fatal("Freshen added a bound")
	}
	both := Bound(a, testPositive)
	if bs := Freshen(both).Bounds(); len(bs) != 2 {
		t.Fatalf("Freshen kept %v of two bounds", bs)
	}
	if both.Unbounded() != TypeVariable('a') {
		t.Fatal("Unbounded kept a bound")
	}
	w, cell := NewWeakVar(a, nil)
	defer runtime.KeepAlive(cell)
	if !w.Has(testOrdered) {
		t.Fatal("a weak copy lost its bound")
	}
}

func TestBoundChecksBindings(t *testing.T) {
	intT, strT := simpleType("Int"), simpleType("String")
	a := Bound(FreshVar(0), testOrdered)

	// The bound ignores nullability: a! := Int! binds a to Int.
	if _, err := Assignable(NonNullType{Type: intT}, NonNullType{Type: a}); err != nil {
		t.Errorf("Int! for a!: %v", err)
	}
	if _, err := Assignable(NonNullType{Type: listOf(NonNullType{Type: intT})}, a); err != nil {
		t.Errorf("[Int!]! for a: %v", err)
	}

	refused := []Type{
		NonNullType{Type: strT},
		NonNullType{Type: listOf(intT)},
		listOf(NonNullType{Type: listOf(NonNullType{Type: strT})}),
		Rigid(TypeVariable('r')),
	}
	for _, have := range refused {
		_, err := Assignable(have, a)
		var ce ConstraintError
		if !errors.As(err, &ce) || ce.Constraint != testOrdered {
			t.Errorf("%s for a: want an Ordered ConstraintError, got %v", have, err)
		}
	}
	// Under a composite type, the bound's error is still the one reported.
	_, err := Assignable(NonNullType{Type: listOf(NonNullType{Type: strT})}, NonNullType{Type: listOf(NonNullType{Type: a})})
	var ce ConstraintError
	if !errors.As(err, &ce) || ce.Var != a {
		t.Errorf("[String!]! for [a!]!: want a ConstraintError for a, got %v", err)
	}

	if _, err := Assignable(Rigid(Bound(TypeVariable('r'), testOrdered)), a); err != nil {
		t.Errorf("a bounded rigid variable for a: %v", err)
	}
}

func TestBoundsPassToVariables(t *testing.T) {
	intT, strT := simpleType("Int"), simpleType("String")
	a := Bound(FreshVar(0), testOrdered)
	b := FreshVar(1)

	// a := b bounds b too.
	subs, err := Assignable(b, a)
	if err != nil {
		t.Fatal(err)
	}
	bound := b.Apply(subs).(Type)
	if v, ok := bound.(TypeVariable); !ok || !v.Has(testOrdered) {
		t.Fatalf("b became %s, want a bounded variable", bound)
	}
	if _, err := Assignable(NonNullType{Type: strT}, bound); err == nil {
		t.Error("String! for b, bounded through a: want an error")
	}
	if _, err := Assignable(NonNullType{Type: intT}, bound); err != nil {
		t.Errorf("Int! for b, bounded through a: %v", err)
	}

	// a := [c]! makes c the non-null form of a bounded variable: the
	// elements of an Ordered list can't be null.
	c := FreshVar(2)
	subs, err = Assignable(NonNullType{Type: listOf(c)}, a)
	if err != nil {
		t.Fatal(err)
	}
	elem := c.Apply(subs).(Type)
	nn, ok := elem.(NonNullType)
	if !ok {
		t.Fatalf("c became %s, want a non-null bounded variable", elem)
	}
	if v, ok := nn.Type.(TypeVariable); !ok || !v.Has(testOrdered) {
		t.Fatalf("c became %s, want a non-null bounded variable", elem)
	}

	// Composing two bindings checks their merge.
	left := Subs{a: NonNullType{Type: intT}}
	right := Subs{a: NonNullType{Type: strT}}
	if _, err := left.Compose(right); err == nil {
		t.Error("Int! and String! for a: want an error")
	}
}

func TestBoundsMerge(t *testing.T) {
	a := Bound(FreshVar(0), testOrdered)
	p := Bound(FreshVar(1), testPositive)

	// Binding two variables bounded differently bounds the result by both.
	subs, err := Assignable(p, a)
	if err != nil {
		t.Fatal(err)
	}
	merged := a.Apply(subs).(Type)
	v, ok := merged.(TypeVariable)
	if !ok || !v.Has(testOrdered) || !v.Has(testPositive) {
		t.Fatalf("a and p became %s, want a variable bounded by both", merged)
	}
	if _, err := Assignable(NonNullType{Type: listOf(NonNullType{Type: simpleType("Int")})}, merged); err == nil {
		t.Error("[Int!]! for a variable bounded by both: want an error, it isn't Positive")
	}
	_, err = Assignable(NonNullType{Type: simpleType("Int")}, merged)
	if err != nil {
		t.Errorf("Int! for a variable bounded by both: %v", err)
	}
}

func TestBoundOnWeakVariables(t *testing.T) {
	w, cell := NewWeakVar(TypeVariable('w'), nil)
	defer runtime.KeepAlive(cell)
	a := Bound(FreshVar(0), testOrdered)

	// Unifying a weak variable with a bounded one bounds it for good.
	if _, err := Assignable(w, a); err != nil {
		t.Fatal(err)
	}
	if _, err := Assignable(NonNullType{Type: simpleType("String")}, w); err == nil {
		t.Fatal("String! for a weak variable bounded by Ordered: want an error")
	}
	if _, err := Assignable(NonNullType{Type: simpleType("Int")}, w); err != nil {
		t.Fatal(err)
	}
}

func TestRequire(t *testing.T) {
	if _, err := Require(testOrdered, simpleType("Int")); err == nil {
		t.Error("Int: want an error, it may be null")
	}
	b := FreshVar(0)
	subs, err := Require(testOrdered, NonNullType{Type: listOf(b)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Assignable(simpleType("Int"), b.Apply(subs).(Type)); err == nil {
		t.Error("Int for an element of an Ordered list: want an error, it may be null")
	}
}
