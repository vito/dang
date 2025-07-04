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

	// Handle type variables
	if tv1, ok := t1.(TypeVariable); ok {
		return bindVar(tv1, t2)
	}

	if tv2, ok := t2.(TypeVariable); ok {
		return bindVar(tv2, t1)
	}

	// Handle function types
	if ft1, ok := t1.(*FunctionType); ok {
		if ft2, ok := t2.(*FunctionType); ok {
			// Unify argument types
			s1, err := unify(ft1.arg, ft2.arg)
			if err != nil {
				return nil, err
			}

			// Apply s1 to return types and unify
			ret1 := ft1.ret.Apply(s1).(Type)
			ret2 := ft2.ret.Apply(s1).(Type)
			s2, err := unify(ret1, ret2)
			if err != nil {
				return nil, err
			}

			return s1.Compose(s2), nil
		}
		return nil, UnificationError{fmt.Sprintf("Unification Fail: %s ~ %s cannot be unified", t1, t2)}
	}

	// General case - use Type.Eq for any remaining types
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
