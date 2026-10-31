package hm

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"weak"
)

// Weak type variables are the unknown parts of a mutable binding's type,
// like the element type of `let xs = []`. They are monomorphic: unlike a
// flexible variable, which every unification may bind to something else
// (and which therefore stands for any type at all), a weak variable is
// bound once, by the first unification that pins it, wherever in the
// program that happens, and is that type from then on. So `xs += [1]` makes
// xs an `[Int!]!` for every later use, and `xs += ["s"]` is an error.
//
// A weak variable's binding lives in a WeakCell, which its owner (the
// declaration that weakened it) keeps alive; variables find their cells
// through a process-wide table of weak pointers.
//
// Unification never binds a weak variable itself: it reports the binding
// in its substitution like any other, and Assignable commits it to the
// cell once the whole unification succeeds, so a failed attempt binds
// nothing. Applying any substitution resolves bound weak variables.

// weakBit marks weak type variables.
const weakBit = 1 << 61

// WeakCell holds what a weak variable has been bound to.
type WeakCell struct {
	bound atomic.Pointer[Type]
	// scope is the rigid variables in scope where the variable's binding
	// was declared: the only ones it may be bound to mention.
	scope TypeVarSet
	// children are the cells of weak variables made for flexible variables
	// in this cell's binding; they live as long as it does.
	mu       sync.Mutex
	children []*WeakCell
}

var weakCells sync.Map // TypeVariable -> weak.Pointer[WeakCell]

// weakVars counts the weak variables ever made; while it is zero, nothing
// needs resolving.
var weakVars atomic.Int64

// NewWeakVar returns a new weak variable named like tv, and its cell. scope
// lists the rigid variables in scope where the binding it belongs to is
// declared (a generic function's own `a`, for a `let` in its body); it may
// be bound to types mentioning those, but no other rigid variable, which
// would escape the function it stands for. The caller must keep the cell
// alive as long as types mention the variable.
func NewWeakVar(tv TypeVariable, scope TypeVarSet) (TypeVariable, *WeakCell) {
	w := Freshen(tv) | weakBit
	cell := &WeakCell{scope: scope}
	weakCells.Store(w, weak.Make(cell))
	runtime.AddCleanup(cell, func(w TypeVariable) { weakCells.Delete(w) }, w)
	weakVars.Add(1)
	return w, cell
}

// EscapeError reports a rigid variable bound into a weak variable declared
// outside the generic function the rigid one belongs to.
type EscapeError struct {
	Rigid TypeVariable
	Type  Type
}

func (e EscapeError) Error() string {
	return fmt.Sprintf("cannot use %s here: %s is a type its generic function's caller picks, and this binding is declared outside that function", e.Type, e.Rigid)
}

// IsWeak reports whether tv is a weak type variable.
func (tv TypeVariable) IsWeak() bool {
	return tv&weakBit != 0
}

// WeakCells returns the cells of the weak variables in t, for whoever keeps
// t to keep alive with it.
func WeakCells(t Type) []*WeakCell {
	var cells []*WeakCell
	for tv := range t.FreeTypeVar() {
		if c := tv.cell(); c != nil {
			cells = append(cells, c)
		}
	}
	return cells
}

func (tv TypeVariable) cell() *WeakCell {
	if !tv.IsWeak() {
		return nil
	}
	p, ok := weakCells.Load(tv)
	if !ok {
		return nil
	}
	return p.(weak.Pointer[WeakCell]).Value()
}

// weakBinding returns what the weak variable tv is bound to, if anything.
func (tv TypeVariable) weakBinding() (Type, bool) {
	cell := tv.cell()
	if cell == nil {
		return nil, false
	}
	t := cell.bound.Load()
	if t == nil {
		return nil, false
	}
	return *t, true
}

// prune resolves t when it is a bound weak variable, nullable or not.
func prune(t Type) Type {
	for {
		switch v := t.(type) {
		case TypeVariable:
			b, ok := v.weakBinding()
			if !ok {
				return t
			}
			t = b
		case NullableTypeVariable:
			b, ok := v.TypeVariable.weakBinding()
			if !ok {
				return t
			}
			t = makeNullable(b)
		default:
			return t
		}
	}
}

// ResolveWeak returns t with its bound weak variables replaced by what they
// are bound to, or t itself when it has none.
func ResolveWeak(t Type) Type {
	if t == nil || weakVars.Load() == 0 || !mentionsBoundWeak(t) {
		return t
	}
	return t.Apply(Subs{}).(Type)
}

func mentionsBoundWeak(t Type) bool {
	switch v := t.(type) {
	case TypeVariable:
		_, ok := v.weakBinding()
		return ok
	case NullableTypeVariable:
		_, ok := v.TypeVariable.weakBinding()
		return ok
	case *UnionType:
		for _, o := range v.Options {
			if mentionsBoundWeak(o) {
				return true
			}
		}
		return false
	case *FunctionType:
		return mentionsBoundWeak(v.arg) || mentionsBoundWeak(v.ret) ||
			(v.block != nil && mentionsBoundWeak(v.block))
	}
	for _, c := range t.Types() {
		if c != nil && mentionsBoundWeak(c) {
			return true
		}
	}
	return false
}

// CommitWeak binds the weak variables subs binds, now that the unification
// producing it succeeded. Assignable and MergeTypes commit their own; a
// caller composing substitutions itself commits the result. A binding
// mentioning flexible variables weakens them too: they become part of the
// binding's one type. It fails, binding nothing, when a binding would let a
// rigid variable escape (see NewWeakVar).
func CommitWeak(subs Subs) error {
	if weakVars.Load() == 0 {
		return nil
	}
	type binding struct {
		cell *WeakCell
		t    Type
	}
	var bindings []binding
	for tv, t := range subs {
		if !tv.IsWeak() {
			continue
		}
		cell := tv.cell()
		if cell == nil || cell.bound.Load() != nil {
			continue
		}
		t = prune(t.Apply(subs).(Type))
		if varOf(t) == tv {
			continue
		}
		cell.mu.Lock()
		scope := cell.scope
		cell.mu.Unlock()
		for fv := range t.FreeTypeVar() {
			if fv.IsRigid() && !scope.Contains(fv) {
				return EscapeError{Rigid: fv, Type: t}
			}
		}
		bindings = append(bindings, binding{cell, t})
	}
	for _, b := range bindings {
		cell, t := b.cell, b.t
		weakened := NewSubs()
		for fv := range t.FreeTypeVar() {
			switch {
			case fv.IsRigid():
			case fv.IsWeak():
				// It is now part of this binding too, so it may mention
				// only what both may.
				if other := fv.cell(); other != nil && other != cell {
					other.mu.Lock()
					other.scope = intersect(other.scope, cell.scope)
					other.mu.Unlock()
				}
			default:
				cell.mu.Lock()
				w, child := NewWeakVar(fv, cell.scope)
				cell.children = append(cell.children, child)
				cell.mu.Unlock()
				weakened.Add(fv, w)
			}
		}
		if len(weakened) > 0 {
			t = t.Apply(weakened).(Type)
		}
		cell.bound.CompareAndSwap(nil, &t)
	}
	return nil
}

func intersect(a, b TypeVarSet) TypeVarSet {
	out := TypeVarSet{}
	for tv := range a {
		if b.Contains(tv) {
			out.Add(tv)
		}
	}
	return out
}
