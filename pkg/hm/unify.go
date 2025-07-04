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
	// Handle qualified types by unifying the underlying types and checking nullability
	if qt1, ok := t1.(*QualifiedType); ok {
		if qt2, ok := t2.(*QualifiedType); ok {
			// Both are qualified - nullability must match
			if qt1.NonNull != qt2.NonNull {
				return nil, UnificationError{fmt.Sprintf("Cannot unify %s with %s: nullability mismatch", t1, t2)}
			}
			return unify(qt1.Type, qt2.Type)
		}
		// t1 is qualified, t2 is not - unify underlying types
		return unify(qt1.Type, t2)
	}
	
	if qt2, ok := t2.(*QualifiedType); ok {
		// t2 is qualified, t1 is not - unify underlying types
		return unify(t1, qt2.Type)
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
		return nil, UnificationError{fmt.Sprintf("Cannot unify function type %s with non-function type %s", t1, t2)}
	}
	
	// Handle type constants
	if tc1, ok := t1.(TypeConst); ok {
		if tc2, ok := t2.(TypeConst); ok {
			if tc1 == tc2 {
				return NewSubs(), nil
			}
			return nil, UnificationError{fmt.Sprintf("Cannot unify type constant %s with %s", tc1, tc2)}
		}
		return nil, UnificationError{fmt.Sprintf("Cannot unify type constant %s with %s", t1, t2)}
	}
	
	return nil, UnificationError{fmt.Sprintf("Cannot unify %s with %s", t1, t2)}
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