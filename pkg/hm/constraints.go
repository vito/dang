package hm

import (
	"fmt"
	"sync"
)

// Bounded type variables stand only for types that satisfy a constraint,
// like the `a` of `[a: Ordered]`. Like nullability, a bound is a constraint
// on the variable that travels with it through unification: binding the
// variable checks the type it is bound to, and binding it to another
// variable passes the bound on, so a variable left unknown by one call keeps
// its bound until some later unification learns its type.
//
// Bounds live in the variable itself, as flag bits beside the rigid and weak
// ones, one per constraint, so every copy of the variable carries them:
// Freshen's, a rigid one's (a bounded parameter inside the body declaring
// it), a weak one's (a binding's unknown part), and its nullable form's.
// Which constraints exist, and which types satisfy them, is the host
// language's to say (see RegisterConstraint); hm only keeps the bounds
// attached and checks them.
//
// A bound ignores nullability: a variable bounded by C stands for T or T!
// for any T! that satisfies C, so `[a: Ordered](xs: [a!]!)` binds a to Int
// for [Int!]!. Whether a value of the variable's type may be null is checked
// where the value is used, like any other value's.

// Constraint identifies a constraint the host registered.
type Constraint uint8

const (
	// maxConstraints is how many constraints there may be; each takes a
	// bit of every type variable.
	maxConstraints = 4
	// constraintShift is the first constraint bit, just below weakBit. The
	// id of a fresh variable takes the bits between its name and these.
	constraintShift = 57
	constraintBits  = (1<<maxConstraints - 1) << constraintShift
)

// ConstraintHost decides which types satisfy a constraint. Given a type of
// values, it returns why they don't, or "" when they do or might: then needs
// lists the flexible variables of t whose types must satisfy the constraint
// for t to (the T of an Ordered [T]!). It decides rigid variables itself: a
// rigid variable's bounds are its declaration's to say, and nothing binds it.
type ConstraintHost func(t Type) (needs []Need, why string)

// Need is a variable a type's satisfying a constraint depends on.
type Need struct {
	Var TypeVariable
	// NonNull reports whether the variable is written non-null where it
	// occurs (the a! of [a!]!). Otherwise it must also come to stand for a
	// non-null type, as the elements of an Ordered list must be.
	NonNull bool
}

type constraintDef struct {
	name string
	host ConstraintHost
}

var (
	constraintsMu sync.RWMutex
	constraints   []constraintDef
)

// RegisterConstraint adds a constraint type variables can be bounded by,
// named for messages, with the host deciding which types satisfy it. The
// host registers each constraint once, when it starts.
func RegisterConstraint(name string, host ConstraintHost) Constraint {
	constraintsMu.Lock()
	defer constraintsMu.Unlock()
	if len(constraints) == maxConstraints {
		panic(fmt.Sprintf("hm: cannot register constraint %s: there may be only %d", name, maxConstraints))
	}
	constraints = append(constraints, constraintDef{name, host})
	return Constraint(len(constraints) - 1)
}

func (c Constraint) def() constraintDef {
	constraintsMu.RLock()
	defer constraintsMu.RUnlock()
	return constraints[c]
}

// Name returns the name the constraint was registered with.
func (c Constraint) Name() string { return c.def().name }

func (c Constraint) bit() TypeVariable { return 1 << (constraintShift + TypeVariable(c)) }

// Bound returns tv bounded by c, besides any bounds it has.
func Bound(tv TypeVariable, c Constraint) TypeVariable { return tv | c.bit() }

// Has reports whether tv is bounded by c.
func (tv TypeVariable) Has(c Constraint) bool { return tv&c.bit() != 0 }

// Bounds returns the constraints tv is bounded by.
func (tv TypeVariable) Bounds() []Constraint {
	if tv&constraintBits == 0 {
		return nil
	}
	var cs []Constraint
	for c := Constraint(0); c < maxConstraints; c++ {
		if tv.Has(c) {
			cs = append(cs, c)
		}
	}
	return cs
}

// Unbounded returns tv without its bounds.
func (tv TypeVariable) Unbounded() TypeVariable { return tv &^ constraintBits }

// ConstraintError reports a type that does not satisfy a bound.
type ConstraintError struct {
	// Var is the bounded variable the type was bound to, if any.
	Var        TypeVariable
	Constraint Constraint
	Type       Type
	// Why explains it, as the host does.
	Why string
}

func (e ConstraintError) Error() string {
	if e.Var != 0 {
		return fmt.Sprintf("cannot use %s for `%s: %s`: %s", e.Type, e.Var.name(), e.Constraint.Name(), e.Why)
	}
	return fmt.Sprintf("%s is not %s: %s", e.Type, e.Constraint.Name(), e.Why)
}

// Require returns the substitution bounding the unknown parts of t, a type
// of values, so that t satisfies c, or the ConstraintError saying why it
// can't. A variable of t written non-null (a!) becomes one bounded by c too;
// one that isn't (the a of [a]!) becomes the non-null form of one, since the
// host asked for it to be non-null as well. The caller commits the
// substitution's weak bindings (CommitWeak) if it keeps it.
func Require(c Constraint, t Type) (Subs, error) {
	if t == nil {
		return nil, nil
	}
	t = ResolveWeak(t)
	needs, why := c.def().host(t)
	if why != "" {
		return nil, ConstraintError{Constraint: c, Type: t, Why: why}
	}
	var subs Subs
	for _, need := range needs {
		tv := need.Var
		if tv.IsRigid() || (tv.Has(c) && need.NonNull) {
			continue
		}
		if _, done := subs[tv]; done {
			continue
		}
		// Freshen keeps the bounds tv has already.
		var to Type = Bound(Freshen(tv), c)
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

// boundSubs checks binding tv to t against tv's bounds, returning the
// substitution that bounds t's unknown parts in turn.
func boundSubs(tv TypeVariable, t Type) (Subs, error) {
	var all Subs
	for _, c := range tv.Bounds() {
		if other := varOf(prune(t)); other >= 0 && other.Has(c) {
			continue
		}
		// A bound ignores nullability: a may be bound to Int for Int!.
		extra, err := Require(c, NonNull(t))
		if ce, ok := err.(ConstraintError); ok {
			ce.Var = tv
			return nil, ce
		}
		if err != nil {
			return nil, err
		}
		if len(extra) == 0 {
			continue
		}
		// The next bound checks t as this one leaves it, so a variable
		// two bounds need ends up bounded by both.
		t = t.Apply(extra).(Type)
		if all == nil {
			all = NewSubs()
		}
		for v, bound := range all {
			all[v] = bound.Apply(extra).(Type)
		}
		for v, bound := range extra {
			if _, done := all[v]; !done {
				all[v] = bound
			}
		}
	}
	return all, nil
}

// bind adds tv := t to subs, after checking t against tv's bounds; what the
// bounds need of t's own variables is added too.
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
