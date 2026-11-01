package hm

// IsSubtypeOf checks whether sub is a true subtype of super, ignoring
// value-level Coercible relationships. It is a question, not a constraint:
// it binds no weak variable.
func IsSubtypeOf(sub, super Type) bool {
	_, err := assignable(sub, super, false)
	return err == nil
}

// IsSupertypeOf checks whether super is a supertype of sub.
func IsSupertypeOf(super, sub Type) bool {
	return IsSubtypeOf(sub, super)
}

// CommonSupertype finds the least common supertype of two types using the
// Type.Supertypes lattice. It returns nil if no common supertype exists.
func CommonSupertype(t1, t2 Type) Type {
	if t1.Eq(t2) {
		return t1
	}

	if IsSubtypeOf(t1, t2) {
		return t2
	}
	if IsSubtypeOf(t2, t1) {
		return t1
	}

	supers1 := buildSupertypeSet(t1)
	supers2 := buildSupertypeSet(t2)

	var common []Type
	for super := range supers1 {
		if supers2[super] {
			common = append(common, super)
		}
	}
	if len(common) == 0 {
		return nil
	}

	for _, candidate := range common {
		least := true
		for _, other := range common {
			if candidate.Eq(other) {
				continue
			}
			if IsSubtypeOf(other, candidate) {
				least = false
				break
			}
		}
		if least {
			return candidate
		}
	}

	return common[0]
}

// MergeTypes returns the least type that can accept values of both current and
// next, plus any substitutions discovered while merging type variables.
func MergeTypes(current, next Type) (Type, Subs, error) {
	merged, subs, err := mergeTypes(current, next)
	if err == nil {
		err = CommitWeak(subs)
	}
	if err != nil {
		return nil, nil, err
	}
	return merged, subs, nil
}

// mergeTypes is MergeTypes without committing weak variables, for merging
// within a unification, which commits them only if it succeeds as a whole.
func mergeTypes(current, next Type) (Type, Subs, error) {
	if current == nil {
		return next, NewSubs(), nil
	}
	if next == nil {
		return current, NewSubs(), nil
	}

	if subs, err := assignable(next, current, false); err == nil {
		merged := current.Apply(subs).(Type)
		resolvedNext := next.Apply(subs).(Type)
		if mergedNonNull, ok := merged.(NonNullType); ok {
			if _, nextNonNull := resolvedNext.(NonNullType); !nextNonNull {
				merged = mergedNonNull.Type
			}
		}
		merged = keepNullableFn(merged, resolvedNext)
		return merged, subs, nil
	}

	if subs, err := assignable(current, next, false); err == nil {
		merged := next.Apply(subs).(Type)
		resolvedCurrent := current.Apply(subs).(Type)
		if mergedNonNull, ok := merged.(NonNullType); ok {
			if _, currentNonNull := resolvedCurrent.(NonNullType); !currentNonNull {
				merged = mergedNonNull.Type
			}
		}
		merged = keepNullableFn(merged, resolvedCurrent)
		return merged, subs, nil
	}

	if common := CommonSupertype(next, current); common != nil {
		return common, NewSubs(), nil
	}

	return nil, nil, UnificationError{Have: next, Want: current}
}

// keepNullableFn keeps a merge of a function with a possibly-null function
// (or null) nullable: the function is assignable to it, but the merge is not
// a callable function.
func keepNullableFn(merged, other Type) Type {
	if fn, ok := merged.(*FunctionType); ok {
		if _, nullable := other.(NullableFunctionType); nullable {
			return NullableFunctionType{Fn: fn}
		}
	}
	return merged
}

func buildSupertypeSet(t Type) map[Type]bool {
	result := map[Type]bool{t: true}
	for _, super := range t.Supertypes() {
		for s := range buildSupertypeSet(super) {
			result[s] = true
		}
	}
	return result
}
