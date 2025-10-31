package hm

import (
	"fmt"
)

// UnificationError represents errors during unification
type UnificationError struct {
	Have, Want Type
}

func (e UnificationError) Error() string {
	return fmt.Sprintf("cannot use %s as %s", e.Have, e.Want)
}

// Assignable attempts to unify two types, returning a substitution or error
func Assignable(have, want Type) (Subs, error) {
	return unify(have, want)
}

func unify(have, want Type) (Subs, error) {
	// Handle type variables
	if haveTV, ok := have.(TypeVariable); ok {
		return bindVar(haveTV, want)
	}

	if wantTV, ok := want.(TypeVariable); ok {
		return bindVar(wantTV, have)
	}

	// Handle non-null types
	if haveNonNull, ok := have.(NonNullType); ok {
		if wantNonNull, ok := want.(NonNullType); ok {
			// Both are non-null - unify underlying types
			return unify(haveNonNull.Type, wantNonNull.Type)
		}
		// have non-null, want is nullable - unify with underlying type
		// NonNull T can unify with T (non-null is subtype of nullable)
		return unify(haveNonNull.Type, want)
		// return nil, fmt.Errorf("Unification Fail: %s ~ %s cannot be unified", have, want)
	}

	if _, ok := want.(NonNullType); ok {
		// want non-null, t1 is nullable - not allowed
		// return unify(have, wantNonNull.Type)
		return nil, UnificationError{have, want}
	}

	// Handle composite types using Types() method
	haveTypes := have.Types()
	wantTypes := want.Types()

	if haveTypes != nil && wantTypes != nil {
		// Both have component types - check length and unify components
		if len(haveTypes) != len(wantTypes) {
			return nil, UnificationError{have, want}
		}

		var subs Subs = NewSubs()
		for i, comp1 := range haveTypes {
			comp2 := wantTypes[i]
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
	if have.Eq(want) {
		return NewSubs(), nil
	}

	return nil, UnificationError{have, want}
}

// bindVar binds a type variable to a type
func bindVar(tv TypeVariable, t Type) (Subs, error) {
	// Check if tv and t are the same
	if tv2, ok := t.(TypeVariable); ok && tv == tv2 {
		return NewSubs(), nil
	}

	// Occurs check
	if occursCheck(tv, t) {
		return nil, fmt.Errorf("Occurs check failed: %s occurs in %s", tv, t)
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
