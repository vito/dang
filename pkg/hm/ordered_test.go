package hm

import (
	"errors"
	"runtime"
	"testing"
)

// withTestOrder makes Int! and lists of non-null Ordered elements the
// Ordered types for the duration of a test; String! has no order.
func withTestOrder(t *testing.T) {
	t.Helper()
	prev := OrderedHost
	var walk func(t Type, nonNull bool, needs *[]OrderedNeed) string
	walk = func(t Type, nonNull bool, needs *[]OrderedNeed) string {
		if nn, ok := t.(NonNullType); ok {
			return walk(nn.Type, true, needs)
		}
		switch v := t.(type) {
		case TypeVariable:
			if v.IsRigid() {
				if !v.IsOrdered() {
					return "unbounded"
				}
				if !nonNull {
					return "may be null"
				}
				return ""
			}
			*needs = append(*needs, OrderedNeed{Var: v, NonNull: nonNull})
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
			return walk(v.elem, false, needs)
		}
		return "no order"
	}
	OrderedHost = func(t Type) ([]OrderedNeed, string) {
		var needs []OrderedNeed
		why := walk(t, false, &needs)
		return needs, why
	}
	t.Cleanup(func() { OrderedHost = prev })
}

func TestOrderedBoundSurvivesCopies(t *testing.T) {
	a := Ordered(TypeVariable('a'))
	if !Freshen(a).IsOrdered() || !Rigid(a).IsOrdered() {
		t.Fatal("a copy of an Ordered variable lost its bound")
	}
	if !a.IsWritten() || Freshen(a).IsWritten() {
		t.Fatal("the bound changed whether a variable is written")
	}
	if Freshen(TypeVariable('a')).IsOrdered() {
		t.Fatal("Freshen added a bound")
	}
	w, cell := NewWeakVar(a, nil)
	defer runtime.KeepAlive(cell)
	if !w.IsOrdered() {
		t.Fatal("a weak copy lost its bound")
	}
}

func TestOrderedBoundChecksBindings(t *testing.T) {
	withTestOrder(t)
	intT, strT := simpleType("Int"), simpleType("String")
	a := Ordered(FreshVar(0))

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
		if !errors.As(err, &ce) {
			t.Errorf("%s for a: want a ConstraintError, got %v", have, err)
		}
	}
	// Under a composite type, the bound's error is still the one reported.
	_, err := Assignable(NonNullType{Type: listOf(NonNullType{Type: strT})}, NonNullType{Type: listOf(NonNullType{Type: a})})
	var ce ConstraintError
	if !errors.As(err, &ce) || ce.Var != a {
		t.Errorf("[String!]! for [a!]!: want a ConstraintError for a, got %v", err)
	}

	if _, err := Assignable(Rigid(Ordered(TypeVariable('r'))), a); err != nil {
		t.Errorf("a bounded rigid variable for a: %v", err)
	}
}

func TestOrderedBoundPassesToVariables(t *testing.T) {
	withTestOrder(t)
	intT, strT := simpleType("Int"), simpleType("String")
	a := Ordered(FreshVar(0))
	b := FreshVar(1)

	// a := b makes b Ordered too.
	subs, err := Assignable(b, a)
	if err != nil {
		t.Fatal(err)
	}
	bound := b.Apply(subs).(Type)
	if v, ok := bound.(TypeVariable); !ok || !v.IsOrdered() {
		t.Fatalf("b became %s, want an Ordered variable", bound)
	}
	if _, err := Assignable(NonNullType{Type: strT}, bound); err == nil {
		t.Error("String! for b, bounded through a: want an error")
	}
	if _, err := Assignable(NonNullType{Type: intT}, bound); err != nil {
		t.Errorf("Int! for b, bounded through a: %v", err)
	}

	// a := [c]! makes c a non-null Ordered type: the elements of an Ordered
	// list can't be null.
	c := FreshVar(2)
	subs, err = Assignable(NonNullType{Type: listOf(c)}, a)
	if err != nil {
		t.Fatal(err)
	}
	elem := c.Apply(subs).(Type)
	nn, ok := elem.(NonNullType)
	if !ok {
		t.Fatalf("c became %s, want a non-null Ordered variable", elem)
	}
	if v, ok := nn.Type.(TypeVariable); !ok || !v.IsOrdered() {
		t.Fatalf("c became %s, want a non-null Ordered variable", elem)
	}

	// Composing two bindings checks their merge.
	left := Subs{a: NonNullType{Type: intT}}
	right := Subs{a: NonNullType{Type: strT}}
	if _, err := left.Compose(right); err == nil {
		t.Error("Int! and String! for a: want an error")
	}
}

func TestOrderedBoundOnWeakVariables(t *testing.T) {
	withTestOrder(t)
	w, cell := NewWeakVar(TypeVariable('w'), nil)
	defer runtime.KeepAlive(cell)
	a := Ordered(FreshVar(0))

	// Unifying a weak variable with an Ordered one bounds it for good.
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

func TestRequireOrdered(t *testing.T) {
	withTestOrder(t)
	if _, err := RequireOrdered(simpleType("Int")); err == nil {
		t.Error("Int: want an error, it may be null")
	}
	b := FreshVar(0)
	subs, err := RequireOrdered(NonNullType{Type: listOf(b)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Assignable(simpleType("Int"), b.Apply(subs).(Type)); err == nil {
		t.Error("Int for an element of an Ordered list: want an error, it may be null")
	}
}
