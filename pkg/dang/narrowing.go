package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// Narrowing refines the type of a symbol in some scope.
type Narrowing struct {
	Symbol string
	Type   hm.Type

	// Set when the refinement is applied (applyNarrowings) under a write log:
	// the binding it refines, the binding's type without it, whether that
	// binding is top-level (as seen from a function body), and the log tick
	// it holds from. pos is 0 when there is no log.
	target   effTarget
	declared hm.Type
	topLevel bool
	pos      int
	// check is the null comparison the refinement comes from: it holds from
	// the tick the comparison was inferred at.
	check Node
}

// ConditionFacts captures the type refinements that hold along each
// branch of a boolean expression.
//
//	Truthy: refinements that are true when the condition evaluates to true
//	Falsy:  refinements that are true when the condition evaluates to false
type ConditionFacts struct {
	Truthy []Narrowing
	Falsy  []Narrowing
}

// analyzeCondition walks a boolean expression and extracts type refinements
// for each branch. It currently handles:
//   - x == null / null == x  (truthy: x is null, falsy: x is non-null)
//   - x != null / null != x  (truthy: x is non-null, falsy: x is null)
//   - a and b                (truthy: combine both; falsy: cannot narrow)
//   - a or b                 (truthy: cannot narrow; falsy: combine both)
//   - (a)                    (same as a)
//   - !a                     (truthy and falsy swapped)
//
// Unknown shapes contribute no facts.
func analyzeCondition(cond Node, env hm.Env) ConditionFacts {
	switch c := cond.(type) {
	case *Grouped:
		return analyzeCondition(c.Expr, env)
	case *UnaryNegation:
		inner := analyzeCondition(c.Expr, env)
		return ConditionFacts{Truthy: inner.Falsy, Falsy: inner.Truthy}
	case *Equality:
		return equalityFacts(c, c.Left, c.Right, env, true)
	case *Inequality:
		return equalityFacts(c, c.Left, c.Right, env, false)
	case *LogicalAnd:
		left := analyzeCondition(c.Left, env)
		right := analyzeCondition(c.Right, env)
		// Truthy: both sides must be true, so combine refinements from both.
		// Falsy: short-circuit can fire on either side, so we can't narrow.
		return ConditionFacts{
			Truthy: append(append([]Narrowing(nil), left.Truthy...), right.Truthy...),
		}
	case *LogicalOr:
		left := analyzeCondition(c.Left, env)
		right := analyzeCondition(c.Right, env)
		// Falsy: both sides must be false, so combine falsy refinements.
		// Truthy: short-circuit can fire on either side, so we can't narrow.
		return ConditionFacts{
			Falsy: append(append([]Narrowing(nil), left.Falsy...), right.Falsy...),
		}
	}
	return ConditionFacts{}
}

// equalityFacts builds the truthy/falsy facts for an equality (or, when
// isEquality=false, inequality) comparison check between a symbol and null.
func equalityFacts(check, left, right Node, env hm.Env, isEquality bool) ConditionFacts {
	sym := nullCheckSymbol(left, right)
	if sym == "" {
		return ConditionFacts{}
	}
	nonNull, ok := nonNullType(env, sym)
	if !ok {
		return ConditionFacts{}
	}
	if isEquality {
		// x == null: truthy says x is null (leave nullable), falsy says x is non-null
		return ConditionFacts{
			Falsy: []Narrowing{{Symbol: sym, Type: nonNull, check: check}},
		}
	}
	// x != null: truthy says x is non-null, falsy says x is null
	return ConditionFacts{
		Truthy: []Narrowing{{Symbol: sym, Type: nonNull, check: check}},
	}
}

// nullCheckSymbol returns the symbol name when one side is a Symbol and the
// other is the null literal; otherwise returns "".
//
// Narrowing is intentionally restricted to bare symbols. Expressions like
// `obj.field == null` or `someCall() == null` cannot soundly narrow their
// receiver, because each access could yield a different value: the first
// call might return non-null and the next might return null. To narrow a
// field or call result, bind it to a local variable first.
func nullCheckSymbol(a, b Node) string {
	if sym, ok := a.(*Symbol); ok {
		if _, isNull := b.(*Null); isNull {
			return sym.Name
		}
	}
	if sym, ok := b.(*Symbol); ok {
		if _, isNull := a.(*Null); isNull {
			return sym.Name
		}
	}
	return ""
}

// nonNullType returns the NonNull version of a symbol's current type, or
// false if the symbol isn't in scope or its type can't be narrowed. A
// function-typed symbol (e.g. a zero-arity computed member, auto-called on
// each read) isn't narrowed: like a field access, each call could differ.
func nonNullType(env hm.Env, sym string) (hm.Type, bool) {
	scheme, found := env.SchemeOf(sym)
	if !found {
		return nil, false
	}
	t, mono := scheme.Type()
	if !mono {
		return nil, false
	}
	// A binding pinned since it was declared narrows from what it is now,
	// and a? (a possibly-null a) narrows to a!, not a?!.
	t = hm.ResolveWeak(t)
	if _, isFn := unwrapNonNull(t).(*hm.FunctionType); isFn {
		return nil, false
	}
	return hm.NonNull(t), true
}

// applyNarrowings installs the given refinements on env in place. Each
// refinement rebinds a symbol to a monomorphic scheme of the narrowed type.
// The env should already be cloned from a parent scope.
func applyNarrowings(ctx context.Context, env hm.Env, narrowings []Narrowing) {
	log := narrowLogFrom(ctx)
	for _, n := range narrowings {
		applied := &Narrowing{Symbol: n.Symbol, Type: n.Type}
		// The type without any refinement: an enclosing refinement's, or
		// the binding's current one.
		if prev := activeNarrowing(env, n.Symbol); prev != nil {
			applied.declared = prev.declared
		} else if scheme, ok := env.SchemeOf(n.Symbol); ok {
			applied.declared, _ = scheme.Type()
		}
		if log != nil {
			// A refinement holds from its null check on: writes made after
			// it, even within the same condition, end it.
			if at, ok := log.checks[n.check]; ok && n.check != nil {
				applied.pos = at
			} else {
				applied.pos = log.tick()
			}
			if frame := resolveBinding(env, n.Symbol); frame != nil {
				applied.target = effTarget{frame: frame, name: n.Symbol}
				applied.topLevel = inFunctionBody(ctx) && isTopLevelFrame(frame, topLevelFrameFrom(ctx))
			}
		}
		if frame, ok := envFrame(env).(*Type); ok {
			// The refinement shadows the binding's type, not the binding: a
			// write still targets the original declaration (see
			// resolveBinding). Only mark names this frame does not itself
			// declare.
			if _, declared := frame.vars[n.Symbol]; !declared {
				if frame.narrowed == nil {
					frame.narrowed = map[string]bool{}
				}
				frame.narrowed[n.Symbol] = true
			}
			if frame.narrowings == nil {
				frame.narrowings = map[string]*Narrowing{}
			}
			frame.narrowings[n.Symbol] = applied
		}
		env.Add(n.Symbol, hm.NewScheme(nil, n.Type))
	}
}

// withNarrowings returns a clone of env with the given refinements applied.
// Use this when refinements should be scoped to a sub-expression and not
// leak out to the surrounding scope.
func withNarrowings(ctx context.Context, env hm.Env, narrowings []Narrowing) hm.Env {
	if len(narrowings) == 0 {
		return env
	}
	cloned := env.Clone()
	applyNarrowings(ctx, cloned, narrowings)
	return cloned
}

// activeNarrowing returns the refinement name's type has in env, or nil when
// it is not narrowed. It mirrors SchemeOf's lookup order (see resolveBinding),
// stopping at the first frame binding name.
func activeNarrowing(env hm.Env, name string) *Narrowing {
	switch e := env.(type) {
	case *OverlayTypeScope:
		if _, found := e.primary.SchemeOf(name); found {
			return activeNarrowing(e.primary, name)
		}
		return activeNarrowing(e.lexical, name)
	case *Type:
		if _, ok := e.vars[name]; ok {
			return e.narrowings[name]
		}
		if e.Parent != nil {
			return activeNarrowing(e.Parent, name)
		}
	}
	return nil
}

type assignTargetKey struct{}

// withAssignTarget marks node as the target of a plain assignment.
func withAssignTarget(ctx context.Context, node Node) context.Context {
	return context.WithValue(ctx, assignTargetKey{}, node)
}

// noteAssignment logs an assignment of a value of type valueType to target
// (in env), when it may set a narrowed binding to null: a bare name, or
// `self.x`, which is the bare field `x`. A longer path copies its root, which
// stays non-null.
func noteAssignment(ctx context.Context, env hm.Env, target Node, valueType hm.Type) {
	log := narrowLogFrom(ctx)
	if log == nil {
		return
	}
	if _, nonNull := valueType.(hm.NonNullType); nonNull {
		return
	}
	var frame TypeScope
	var name string
	switch t := target.(type) {
	case *Symbol:
		name = t.Name
		frame = resolveBinding(env, name)
	case *Select:
		if _, isSelf := t.Receiver.(*SelfKeyword); isSelf {
			name = t.Field.Name
			frame = selfTypeFrame(env)
		}
	}
	if frame == nil {
		return
	}
	t := effTarget{frame: frame, name: name}
	// A closure assigning null to a binding outside it nulls it when called.
	if fr := bodyFrame(ctx); fr != nil && !fr.inside(frame) {
		if fr.eff.nullTargets == nil {
			fr.eff.nullTargets = map[effTarget]bool{}
		}
		fr.eff.nullTargets[t] = true
	}
	log.writes = append(log.writes, narrowWrite{at: log.tick(), target: t, frame: effFrameFrom(ctx), node: target})
}

// readNarrowed returns the type a read (at node) of name gets: its refined
// type t, or the unrefined one once the refinement has been invalidated. The
// target of a plain assignment is not a read: it gets the unrefined type,
// which is what the binding may hold.
func readNarrowed(ctx context.Context, env hm.Env, name string, node Node, t hm.Type) hm.Type {
	n := activeNarrowing(env, name)
	if n == nil || n.declared == nil {
		return t
	}
	if target, _ := ctx.Value(assignTargetKey{}).(Node); target == node {
		return n.declared
	}
	if n.pos == 0 {
		return t
	}
	log := narrowLogFrom(ctx)
	if log == nil {
		return t
	}
	reader := effFrameFrom(ctx)
	for _, w := range log.writes {
		if w.at > n.pos && !log.excluded(w.at) && w.hits(n, reader) {
			return n.declared
		}
	}
	// Record the read with every enclosing closure the refinement was made
	// outside of.
	for fr := reader; fr != nil; fr = fr.parent {
		if fr.field {
			continue
		}
		if fr.logStart < n.pos {
			break
		}
		fr.reads = append(fr.reads, narrowRead{n: n, node: node})
	}
	return t
}

// exitNarrowBody checks a closure body's reads of outer refinements against
// the writes in the body, once it has been inferred, and keeps them to check
// against the writes elsewhere (see checkCaptures).
func exitNarrowBody(ctx context.Context, fr *effFrame) {
	log := narrowLogFrom(ctx)
	if log == nil || fr == nil || len(fr.reads) == 0 {
		return
	}
	end := log.tick()
	what := "function"
	if fr.block {
		what = "block"
	}
	for _, r := range fr.reads {
		for _, w := range log.writes {
			if w.at > fr.logStart && w.at < end && w.hits(r.n, fr) && !log.rerunSkips(w.at, fr) {
				log.errs = append(log.errs, narrowError{node: r.node, msg: fmt.Sprintf(
					"`%s` is checked for null outside this %s, but the %s may set it to null (%s), so it may be null here when the %s runs again; check it inside the %s",
					r.n.Symbol, what, what, writeDesc(w), what, what)})
				break
			}
		}
	}
	scope, _ := ctx.Value(narrowScopeKey{}).(*narrowScope)
	// The outermost body around the closure that may run again: a write
	// in it made before a check may follow a capture from an earlier run.
	rerun := 0
	for f := fr.parent; f != nil; f = f.parent {
		if f.block || f.parent != nil {
			rerun = f.logStart
		}
	}
	// Calling the closure (a nested function) reads what it reads, so a
	// closure calling it captures that too (noteCall).
	fr.eff.narrowReads = fr.reads
	log.captures = append(log.captures, narrowCapture{
		eff: fr.eff, reads: fr.reads, start: fr.logStart, end: end,
		block: fr.block, parent: fr.parent, scope: scope, rerun: rerun,
	})
}

func writeDesc(w narrowWrite) string {
	where := ""
	if loc := w.node.GetSourceLocation(); loc != nil {
		where = fmt.Sprintf(" at line %d", loc.Line)
	}
	switch {
	case w.wild:
		return "by calling a function parameter" + where + ", which may run its caller's code"
	case w.call:
		return "by calling a function that assigns it" + where
	}
	return "by assigning it" + where
}

// checkCaptures reports closures that read an outer refinement whose binding
// may be set to null outside them, and that may run after that: a nested
// function, which can be called later in its scope, or an escaping closure,
// which can run any time.
func (l *narrowLog) checkCaptures(s *effectSolver, errs *effErrors) {
	for _, e := range l.errs {
		errs.add(e.node, e.msg)
	}
	for _, c := range l.captures {
		escapes := c.eff.escaped || s.escapes(c.eff)
		if c.block && !escapes {
			continue
		}
		what := "function, which may be called"
		if c.block {
			what = "block, which may be stored and run"
		}
		for _, r := range c.reads {
			for _, w := range l.writes {
				if w.at > c.start && w.at < c.end {
					continue
				}
				// A function that stays in its scope is called after it is
				// created and before the scope ends (a loop creates it
				// anew); a stored one may run any time, but writes before
				// the check only reach it from an earlier run of a body
				// around it.
				if !escapes && (w.at < c.start || (c.scope != nil && c.scope.end != 0 && w.at > c.scope.end)) {
					continue
				}
				if escapes && w.at < r.n.pos && (c.rerun == 0 || w.at < c.rerun) {
					continue
				}
				if !w.hits(r.n, c.parent) {
					continue
				}
				errs.add(r.node, fmt.Sprintf(
					"`%s` is checked for null outside this %s after `%s` is set to null (%s); bind the checked value to a local with let and use that instead",
					r.n.Symbol, what, r.n.Symbol, writeDesc(w)))
				break
			}
		}
	}
}
