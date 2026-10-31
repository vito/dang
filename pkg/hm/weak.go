package hm

import (
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
	// children are the cells of weak variables made for flexible variables
	// in this cell's binding; they live as long as it does.
	mu       sync.Mutex
	children []*WeakCell
}

var weakCells sync.Map // TypeVariable -> weak.Pointer[WeakCell]

// weakVars counts the weak variables ever made; while it is zero, nothing
// needs resolving.
var weakVars atomic.Int64

// NewWeakVar returns a new weak variable named like tv, and its cell. The
// caller must keep the cell alive as long as types mention the variable.
func NewWeakVar(tv TypeVariable) (TypeVariable, *WeakCell) {
	w := Freshen(tv) | weakBit
	cell := &WeakCell{}
	weakCells.Store(w, weak.Make(cell))
	runtime.AddCleanup(cell, func(w TypeVariable) { weakCells.Delete(w) }, w)
	weakVars.Add(1)
	return w, cell
}

// IsWeak reports whether tv is a weak type variable.
func (tv TypeVariable) IsWeak() bool {
	return tv&weakBit != 0
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
// binding's one type.
func CommitWeak(subs Subs) {
	if weakVars.Load() == 0 {
		return
	}
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
		weakened := NewSubs()
		for fv := range t.FreeTypeVar() {
			if fv.IsRigid() || fv.IsWeak() {
				continue
			}
			w, child := NewWeakVar(fv)
			cell.mu.Lock()
			cell.children = append(cell.children, child)
			cell.mu.Unlock()
			weakened.Add(fv, w)
		}
		if len(weakened) > 0 {
			t = t.Apply(weakened).(Type)
		}
		cell.bound.CompareAndSwap(nil, &t)
	}
}
