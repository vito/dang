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

// TypeConstructor is implemented by composite types whose unification cannot
// be determined by reflect.TypeOf alone, such as nominal generic types where
// every applied instance has the same Go type but different bases. Returning
// true from SameTypeConstructor means the two values share a constructor and
// can be unified by walking their component types. The unification of those
// components is invariant.
type TypeConstructor interface {
	SameTypeConstructor(Type) bool
}

// Assignable attempts to unify two types, returning a substitution or error.
// If unification fails, it checks subtyping: have can be assigned to want if
// have is a subtype of want.
func Assignable(have, want Type) (Subs, error) {
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
		return assignableFromUnion(haveUnion, want)
	}

	// Handle composite types using Types() method
	haveTypes := have.Types()
	wantTypes := want.Types()

	if haveTypes != nil && wantTypes != nil {
		// Types that implement TypeConstructor (e.g. nominal generic
		// applications) need an explicit base-equality check since their
		// Go type alone does not distinguish constructors. Arguments are
		// also unified invariantly.
		haveTC, haveHasTC := have.(TypeConstructor)
		_, wantHasTC := want.(TypeConstructor)

		switch {
		case haveHasTC && wantHasTC && haveTC.SameTypeConstructor(want):
			// Same nominal constructor: invariant unify arguments.
			if len(haveTypes) != len(wantTypes) {
				return nil, UnificationError{have, want}
			}

			var subs = NewSubs()
			for i, comp1 := range haveTypes {
				comp2 := wantTypes[i]
				componentSubs, err := unifyInvariant(comp1, comp2)
				if err != nil {
					return nil, UnificationError{have, want}
				}
				subs, err = subs.Compose(componentSubs)
				if err != nil {
					return nil, UnificationError{have, want}
				}
			}
			return subs, nil

		case haveHasTC || wantHasTC:
			// At least one side is a constructed nominal type but they
			// don't share a constructor (or only one side is). Skip the
			// reflect.TypeOf composite branch — its component-wise
			// unification would silently equate different constructors —
			// and fall through to the supertype/coercion fallback so e.g.
			// NonNull(Box[T]) → Box[T] resolves via NonNullType's
			// Supertypes().

		case reflect.TypeOf(have) == reflect.TypeOf(want):
			// Same concrete Go composite type (e.g. NonNull vs NonNull,
			// List vs List). For example, NonNullType{Int} and
			// ListType{Int} both have component types but different
			// constructors and so should not unify here — they don't
			// share a Go type either.
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
				componentSubs, err := Assignable(comp1, comp2)
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
		if subs, err := Assignable(supertype, want); err == nil {
			return subs, nil
		}
	}

	// Check if want type accepts coercion from have type
	// This is used for custom scalar types that accept built-in scalar values
	if coercible, ok := want.(Coercible); ok {
		if coercible.AcceptsCoercionFrom(have) {
			return NewSubs(), nil
		}
	}

	// Assigning to a union succeeds if any option accepts the value. This runs
	// after supertype checks so NonNull(T1 | T2) can still flow to T1 | T2.
	if wantUnion, ok := want.(*UnionType); ok {
		return assignableToUnion(have, wantUnion)
	}

	return nil, UnificationError{have, want}
}

func assignableToUnion(have Type, want *UnionType) (Subs, error) {
	for _, option := range want.Options {
		if subs, err := Assignable(have, option); err == nil {
			return subs, nil
		}
	}
	return nil, UnificationError{have, want}
}

func assignableFromUnion(have *UnionType, want Type) (Subs, error) {
	subs := NewSubs()
	for _, option := range have.Options {
		optionType := option.Apply(subs).(Type)
		wantType := want.Apply(subs).(Type)
		optionSubs, err := Assignable(optionType, wantType)
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

// unifyInvariant unifies two types treating them as invariant: type variables
// may still be bound, but the resulting types must be equal in both
// directions. It is the symmetric counterpart to Assignable, used for
// generic type arguments where subtyping would be unsound.
func unifyInvariant(a, b Type) (Subs, error) {
	subs, err := Assignable(a, b)
	if err != nil {
		return nil, err
	}
	reverse, err := Assignable(b.Apply(subs).(Type), a.Apply(subs).(Type))
	if err != nil {
		return nil, err
	}
	return subs.Compose(reverse)
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
