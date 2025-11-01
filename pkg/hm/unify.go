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

// isSubtype checks if sub is a subtype of super (transitively)
// This implements the subtype relationship using the Supertypes() method.
func isSubtype(sub, super Type) bool {
	if sub.Eq(super) {
		return true
	}

	// Check composite type covariance (e.g., list element covariance)
	// If both types have component types with matching structure,
	// check if all components have subtype relationships
	subTypes := sub.Types()
	superTypes := super.Types()
	if subTypes != nil && superTypes != nil {
		if len(subTypes) == len(superTypes) {
			allCovariant := true
			for i := range subTypes {
				if !isSubtype(subTypes[i], superTypes[i]) {
					allCovariant = false
					break
				}
			}
			if allCovariant {
				return true
			}
		}
	}

	// Check direct supertypes recursively
	for _, supertype := range sub.Supertypes() {
		if isSubtype(supertype, super) {
			return true
		}
	}

	return false
}

// IsSubtype is the exported version of isSubtype for use by other packages
func IsSubtype(sub, super Type) bool {
	return isSubtype(sub, super)
}

// Assignable attempts to unify two types, returning a substitution or error.
// If unification fails, it checks subtyping: have can be assigned to want if
// have is a subtype of want.
func Assignable(have, want Type) (Subs, error) {
	// First try direct unification
	subs, err := unify(have, want)
	if err == nil {
		return subs, nil
	}

	// If that fails, try subtyping: check if have is a subtype of want
	if isSubtype(have, want) {
		return NewSubs(), nil
	}

	return nil, UnificationError{have, want}
}

func unify(have, want Type) (Subs, error) {
	// Handle type variables first
	if haveTV, ok := have.(TypeVariable); ok {
		return bindVar(haveTV, want)
	}
	if wantTV, ok := want.(TypeVariable); ok {
		return bindVar(wantTV, have)
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
