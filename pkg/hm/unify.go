package hm

import (
	"fmt"
	"reflect"
)

// UnificationError represents errors during unification
type UnificationError struct {
	Have, Want Type
}

func (e UnificationError) Error() string {
	return fmt.Sprintf("cannot use %s as %s", e.Have, e.Want)
}

// Assignable attempts to unify two types, returning a substitution or error.
// If unification fails, it checks subtyping: have can be assigned to want if
// have is a subtype of want.
func Assignable(have, want Type) (Subs, error) {
	// Handle type variables first
	if haveTV, ok := have.(TypeVariable); ok {
		return bindVar(haveTV, want)
	}
	if wantTV, ok := want.(TypeVariable); ok {
		return bindVar(wantTV, have)
	}

	// Try Type.Eq first (simplest check)
	if have.Eq(want) {
		return NewSubs(), nil
	}

	// Handle composite types using Types() method
	haveTypes := have.Types()
	wantTypes := want.Types()

	if haveTypes != nil && wantTypes != nil {
		// Both have component types - but we must ensure they're the same
		// type constructor before unifying components.
		// For example, NonNullType{Int} and ListType{Int} both have component
		// types, but they're different constructors and should not unify.
		//
		// TODO: cleaner way to do this
		if reflect.TypeOf(have) == reflect.TypeOf(want) {
			// Check length and unify components
			if len(haveTypes) != len(wantTypes) {
				return nil, UnificationError{have, want}
			}

			var subs = NewSubs()
			for i, comp1 := range haveTypes {
				comp2 := wantTypes[i]
				// Apply current substitutions to both components
				comp1Applied := comp1.Apply(subs).(Type)
				comp2Applied := comp2.Apply(subs).(Type)

				// Unify the components
				componentSubs, err := Assignable(comp1Applied, comp2Applied)
				if err != nil {
					return nil, UnificationError{have, want}
				}

				// Compose the substitutions
				subs = subs.Compose(componentSubs)
			}
			return subs, nil
		}
	}

	// Fall back on checking supertypes
	for _, supertype := range have.Supertypes() {
		if subs, err := Assignable(supertype, want); err == nil {
			return subs, nil
		}
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
		return nil, fmt.Errorf("occurs check failed: %s occurs in %s", tv, t)
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
