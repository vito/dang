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
// If unification fails, it checks pure subtyping: have can be assigned to want
// if have is a subtype of want. It does not accept value-level coercions such
// as String -> ID/custom scalar; use AssignableWithCoercion at explicit Coerce
// boundaries instead.
func Assignable(have, want Type) (Subs, error) {
	return assignable(have, want, false)
}

// AssignableWithCoercion is like Assignable, but additionally accepts explicit
// Coercible relationships such as String -> ID/custom scalar/enum. Use this
// only when the expression is wrapped in a runtime Coerce node.
func AssignableWithCoercion(have, want Type) (Subs, error) {
	return assignable(have, want, true)
}

// AssignableNoCoercion is kept as a compatibility alias for Assignable.
// Deprecated: use Assignable.
func AssignableNoCoercion(have, want Type) (Subs, error) {
	return Assignable(have, want)
}

func assignable(have, want Type, allowCoercion bool) (Subs, error) {
	// Handle nullable type variables first (before regular TypeVariable,
	// since NullableTypeVariable embeds TypeVariable)
	if haveNTV, ok := have.(NullableTypeVariable); ok {
		if _, wantNonNull := want.(NonNullType); wantNonNull {
			return nil, UnificationError{have, want}
		}
		return bindNullableVar(haveNTV, want)
	}
	if wantNTV, ok := want.(NullableTypeVariable); ok {
		return bindNullableVar(wantNTV, have)
	}

	// Handle type variables
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

	// Handle inline union sources. Assigning from a union requires every
	// possible option to be assignable to the target. If the target is also a
	// union, each option may match any of the target union's options.
	if haveUnion, ok := have.(*UnionType); ok {
		return assignableFromUnion(haveUnion, want, allowCoercion)
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

				// Unify each component against the original component types, then
				// merge the resulting substitutions. Applying substitutions eagerly
				// makes repeated type variables order-dependent (e.g. (Int!, null)
				// against (a, a)); checked composition widens those constraints once
				// all component evidence is available.
				componentSubs, err := assignable(comp1, comp2, allowCoercion)
				if err != nil {
					return nil, UnificationError{have, want}
				}

				subs, err = subs.Compose(componentSubs)
				if err != nil {
					return nil, UnificationError{have, want}
				}
			}
			return subs, nil
		}
	}

	// Fall back on checking supertypes
	for _, supertype := range have.Supertypes() {
		if subs, err := assignable(supertype, want, allowCoercion); err == nil {
			return subs, nil
		}
	}

	// Check if want type accepts coercion from have type
	// This is used for custom scalar types that accept built-in scalar values
	if allowCoercion {
		if coercible, ok := want.(Coercible); ok {
			if coercible.AcceptsCoercionFrom(have) {
				return NewSubs(), nil
			}
		}
	}

	// Assigning to a union succeeds if any option accepts the value. This runs
	// after supertype checks so NonNull(T1 | T2) can still flow to T1 | T2.
	if wantUnion, ok := want.(*UnionType); ok {
		return assignableToUnion(have, wantUnion, allowCoercion)
	}

	return nil, UnificationError{have, want}
}

func assignableToUnion(have Type, want *UnionType, allowCoercion bool) (Subs, error) {
	for _, option := range want.Options {
		if subs, err := assignable(have, option, allowCoercion); err == nil {
			return subs, nil
		}
	}
	return nil, UnificationError{have, want}
}

func assignableFromUnion(have *UnionType, want Type, allowCoercion bool) (Subs, error) {
	subs := NewSubs()
	for _, option := range have.Options {
		optionType := option.Apply(subs).(Type)
		wantType := want.Apply(subs).(Type)
		optionSubs, err := assignable(optionType, wantType, allowCoercion)
		if err != nil {
			return nil, UnificationError{have, want}
		}
		var composeErr error
		subs, composeErr = subs.Compose(optionSubs)
		if composeErr != nil {
			return nil, UnificationError{have, want}
		}
	}
	return subs, nil
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

// bindNullableVar binds a nullable type variable to a type while preserving
// its nullability taint. This ensures that null always resolves to a nullable
// type: binding NullableTypeVariable to String! produces String, not String!,
// and binding it to a plain type variable taints that variable as nullable.
func bindNullableVar(tv NullableTypeVariable, t Type) (Subs, error) {
	subs := NewSubs()

	if tv2, ok := t.(NullableTypeVariable); ok {
		if tv.TypeVariable == tv2.TypeVariable {
			return subs, nil
		}
		subs.Add(tv.TypeVariable, tv2)
		return subs, nil
	}

	if tv2, ok := t.(TypeVariable); ok {
		// Bind the other variable to the nullable one so the taint is visible when
		// that variable appears elsewhere, such as a generic function's return type.
		subs.Add(tv2, tv)
		return subs, nil
	}

	// Strip NonNull — null is inherently nullable.
	t = makeNullable(t)
	return bindVar(tv.TypeVariable, t)
}

// occursCheck checks if a type variable occurs in a type
func occursCheck(tv TypeVariable, t Type) bool {
	ftvs := t.FreeTypeVar()
	return ftvs.Contains(tv)
}
