package dang

import (
	"github.com/vito/dang/v2/pkg/hm"
)

// Narrowing refines the type of a symbol in some scope.
type Narrowing struct {
	Symbol string
	Type   hm.Type
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
//
// Unknown shapes contribute no facts.
func analyzeCondition(cond Node, env hm.Env) ConditionFacts {
	switch c := cond.(type) {
	case *Equality:
		return equalityFacts(c, env, true)
	case *Inequality:
		return equalityFacts(&Equality{Left: c.Left, Right: c.Right, Loc: c.Loc}, env, false)
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
// negate=true, inequality) comparison between a symbol and null.
func equalityFacts(eq *Equality, env hm.Env, isEquality bool) ConditionFacts {
	sym := nullCheckSymbol(eq.Left, eq.Right)
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
			Falsy: []Narrowing{{Symbol: sym, Type: nonNull}},
		}
	}
	// x != null: truthy says x is non-null, falsy says x is null
	return ConditionFacts{
		Truthy: []Narrowing{{Symbol: sym, Type: nonNull}},
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
// false if the symbol isn't in scope or its type can't be narrowed.
func nonNullType(env hm.Env, sym string) (hm.Type, bool) {
	scheme, found := env.SchemeOf(sym)
	if !found {
		return nil, false
	}
	t, mono := scheme.Type()
	if !mono {
		return nil, false
	}
	if _, isNonNull := t.(hm.NonNullType); isNonNull {
		return t, true
	}
	return hm.NonNullType{Type: t}, true
}

// applyNarrowings installs the given refinements on env in place. Each
// refinement rebinds a symbol to a monomorphic scheme of the narrowed type.
// The env should already be cloned from a parent scope.
func applyNarrowings(env hm.Env, narrowings []Narrowing) {
	for _, n := range narrowings {
		env.Add(n.Symbol, hm.NewScheme(nil, n.Type))
	}
}

// withNarrowings returns a clone of env with the given refinements applied.
// Use this when refinements should be scoped to a sub-expression and not
// leak out to the surrounding scope.
func withNarrowings(env hm.Env, narrowings []Narrowing) hm.Env {
	if len(narrowings) == 0 {
		return env
	}
	cloned := env.Clone()
	applyNarrowings(cloned, narrowings)
	return cloned
}
