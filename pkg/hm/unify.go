package hm

import (
	"fmt"
)

// UnificationError represents errors during unification
type UnificationError struct {
	msg string
}

func (e UnificationError) Error() string {
	return e.msg
}

// Unify attempts to unify two types, returning a substitution or error
func Unify(t1, t2 Type) (Subs, error) {
	return unify(t1, t2)
}

func unify(t1, t2 Type) (Subs, error) {
	// Handle type variables
	if tv1, ok := t1.(TypeVariable); ok {
		return bindVar(tv1, t2)
	}

	if tv2, ok := t2.(TypeVariable); ok {
		return bindVar(tv2, t1)
	}

	// Handle non-null types
	if nt1, ok := t1.(NonNullType); ok {
		if nt2, ok := t2.(NonNullType); ok {
			// Both are non-null - unify underlying types
			return unify(nt1.Type, nt2.Type)
		}
		// t1 is non-null, t2 is nullable - unify with underlying type
		// NonNull T can unify with T (non-null is subtype of nullable)
		// return unify(nt1.Type, t2)
		return nil, fmt.Errorf("Unification Fail: %s ~ %s cannot be unified", t1, t2)
	}

	if nt2, ok := t2.(NonNullType); ok {
		// t2 is non-null, t1 is nullable - not allowed
		return unify(t1, nt2.Type)
		// return nil, fmt.Errorf("Unification Fail: %s ~ %s cannot be unified", t1, t2)
	}

	// Handle composite types using Types() method
	t1Types := t1.Types()
	t2Types := t2.Types()

	if t1Types != nil && t2Types != nil {
		// Both have component types - check length and unify components
		if len(t1Types) != len(t2Types) {
			return nil, UnificationError{fmt.Sprintf("Unification Fail: %s ~ %s cannot be unified (different arities)", t1, t2)}
		}

		var subs Subs = NewSubs()
		for i, comp1 := range t1Types {
			comp2 := t2Types[i]
			// Apply current substitutions to both components
			comp1Applied := comp1.Apply(subs).(Type)
			comp2Applied := comp2.Apply(subs).(Type)

			// Unify the components
			componentSubs, err := unify(comp1Applied, comp2Applied)
			if err != nil {
				return nil, err
			}

			// Compose the substitutions
			subs = subs.Compose(componentSubs)
		}
		return subs, nil
	}

	// Fall back to Type.Eq for atomic types or when only one has component types
	if t1.Eq(t2) {
		return NewSubs(), nil
	}

	return nil, UnificationError{fmt.Sprintf("Unification Fail: %s ~ %s cannot be unified", t1, t2)}
}

// bindVar binds a type variable to a type
func bindVar(tv TypeVariable, t Type) (Subs, error) {
	// Check if tv and t are the same
	if tv2, ok := t.(TypeVariable); ok && tv == tv2 {
		return NewSubs(), nil
	}

	// Occurs check
	if occursCheck(tv, t) {
		return nil, UnificationError{fmt.Sprintf("Occurs check failed: %s occurs in %s", tv, t)}
	}

	subs := NewSubs()
	subs.Add(tv, t)
	return subs, nil
}

// occursCheck checks if a type variable occurs in a type
func occursCheck(tv TypeVariable, t Type) bool {
	ftvs := t.FreeTypeVar()
	return ftvs.Contains(tv)
}
