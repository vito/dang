package hm

import "fmt"

// Ordered type variables are bounded by the Ordered constraint: they stand
// only for types whose values have an order (`[a: Ordered]`). Like
// nullability, the bound is a constraint on the variable that travels with
// it through unification: binding the variable checks the type it is bound
// to, and binding it to another variable passes the bound on, so a variable
// left unknown by one call keeps its bound until some later unification
// learns its type.
//
// The bound lives in the variable itself, as a flag bit beside the rigid and
// weak ones, so every copy of the variable carries it: Freshen's, a rigid
// one's (a bounded parameter inside the body declaring it), a weak one's (a
// binding's unknown part), and its nullable form's. Which types are Ordered
// is the host language's to say (see OrderedHost); hm only knows how to keep
// the bound attached.
//
// The bound ignores nullability: an Ordered variable stands for T or T! for
// any Ordered T!, so `[a: Ordered](xs: [a!]!)` binds a to Int for [Int!]!.
// Whether a value of the variable's type may be null is checked where it is
// compared, like any other value's.

// orderedBit marks type variables bounded by Ordered.
const orderedBit = 1 << 60

// Ordered returns tv bounded by Ordered.
func Ordered(tv TypeVariable) TypeVariable { return tv | orderedBit }

// IsOrdered reports whether tv is bounded by Ordered.
func (tv TypeVariable) IsOrdered() bool { return tv&orderedBit != 0 }

// Unbounded returns tv without its Ordered bound.
func (tv TypeVariable) Unbounded() TypeVariable { return tv &^ orderedBit }

// OrderedNeed is a variable whose type an Ordered type's order depends on:
// the T of [T]! is Ordered only if T is.
type OrderedNeed struct {
	Var TypeVariable
	// NonNull reports whether the variable is written non-null where it
	// occurs (the a! of [a!]!). Otherwise it must also come to stand for a
	// non-null type, as the elements of an Ordered list must be.
	NonNull bool
}

// OrderedHost decides which types are Ordered; the host language sets it.
// Given a type of values, it returns why they have no order, or "" when
// they have one or might: then needs lists the flexible variables their
// order still depends on. It decides rigid variables itself: a rigid
// variable's type is its declaration's to say, and nothing binds it.
var OrderedHost func(t Type) (needs []OrderedNeed, why string)

// ConstraintError reports a type that does not satisfy a bound.
type ConstraintError struct {
	// Var is the bounded variable the type was bound to, if any.
	Var  TypeVariable
	Type Type
	// Why explains it, as the host does.
	Why string
}

func (e ConstraintError) Error() string {
	if e.Var != 0 {
		return fmt.Sprintf("cannot use %s for `%s: Ordered`: %s", e.Type, e.Var.name(), e.Why)
	}
	return fmt.Sprintf("%s is not Ordered: %s", e.Type, e.Why)
}

// RequireOrdered returns the substitution bounding the unknown parts of t, a
// type of values, so that t is Ordered, or the ConstraintError saying why
// it can't be. A variable of t written non-null (a!) becomes an Ordered one;
// one that isn't (the a of [a]!) becomes the non-null form of an Ordered one,
// since an Ordered type's parts are never null. The caller commits the
// substitution's weak bindings (CommitWeak) if it keeps it.
func RequireOrdered(t Type) (Subs, error) {
	if OrderedHost == nil || t == nil {
		return nil, nil
	}
	t = ResolveWeak(t)
	needs, why := OrderedHost(t)
	if why != "" {
		return nil, ConstraintError{Type: t, Why: why}
	}
	var subs Subs
	for _, need := range needs {
		tv := need.Var
		if tv.IsRigid() || (tv.IsOrdered() && need.NonNull) {
			continue
		}
		if _, done := subs[tv]; done {
			continue
		}
		var to Type = Ordered(Freshen(tv))
		if !need.NonNull {
			to = NonNullType{Type: to}
		}
		if subs == nil {
			subs = NewSubs()
		}
		subs.Add(tv, to)
	}
	return subs, nil
}

// boundSubs checks binding tv to t against tv's bound, returning the
// substitution that bounds t's unknown parts in turn.
func boundSubs(tv TypeVariable, t Type) (Subs, error) {
	if !tv.IsOrdered() {
		return nil, nil
	}
	if other := varOf(prune(t)); other >= 0 && other.IsOrdered() {
		return nil, nil
	}
	// The bound ignores nullability: a may be bound to Int for Int!.
	subs, err := RequireOrdered(NonNull(t))
	if ce, ok := err.(ConstraintError); ok {
		ce.Var = tv
		return nil, ce
	}
	return subs, err
}

// bind adds tv := t to subs, after checking t against tv's bound; what the
// bound needs of t's own variables is added too.
func bind(subs Subs, tv TypeVariable, t Type) error {
	extra, err := boundSubs(tv, t)
	if err != nil {
		return err
	}
	if len(extra) == 0 {
		subs.Add(tv, t)
		return nil
	}
	for v, bound := range subs {
		subs[v] = bound.Apply(extra).(Type)
	}
	subs.Add(tv, t.Apply(extra).(Type))
	for v, bound := range extra {
		subs.Add(v, bound)
	}
	return nil
}

// unifyError is the error a composite unification of have and want reports
// for a failure of one of its parts: a bound's own error, which says more
// than that the two don't unify, or that they don't.
func unifyError(err error, have, want Type) error {
	if ce, ok := err.(ConstraintError); ok {
		return ce
	}
	return UnificationError{have, want}
}
