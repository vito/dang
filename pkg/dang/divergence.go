package dang

import (
	"github.com/vito/dang/v2/pkg/hm"
)

// divergesNormally reports whether a node never completes normally — i.e.
// whether evaluating it always raises, returns, breaks, or continues, so that
// any code following it in source order is unreachable.
//
// This is used to power guard-clause narrowing: when one branch of an `if`
// always diverges, the surrounding scope can apply the opposite branch's
// type refinements.
func divergesNormally(node Node) bool {
	switch n := node.(type) {
	case *Raise:
		return true
	case *Return:
		return true
	case *Break:
		return true
	case *Continue:
		return true
	case *Block:
		// A block diverges iff its last form diverges. An empty block falls
		// through (it evaluates to null).
		if len(n.Forms) == 0 {
			return false
		}
		return divergesNormally(n.Forms[len(n.Forms)-1])
	case *Conditional:
		// A conditional without an else can always fall through with null.
		if n.Else == nil {
			return false
		}
		return divergesNormally(n.Then) && divergesNormally(n.Else)
	}
	return false
}

// returnsAlways reports whether a node always leaves its function with
// `return`, so nothing written in it reaches the code after it. (A raise may
// be rescued, and break/continue stay in the function.)
func returnsAlways(node Node) bool {
	switch n := node.(type) {
	case *Return:
		return true
	case *Block:
		if len(n.Forms) == 0 {
			return false
		}
		return returnsAlways(n.Forms[len(n.Forms)-1])
	case *Conditional:
		return n.Else != nil && returnsAlways(n.Then) && returnsAlways(n.Else)
	}
	return false
}

// leavesIteration reports whether a node always leaves the rest of the
// enclosing block with `break`, `continue` or `return`.
func leavesIteration(node Node) bool {
	switch n := node.(type) {
	case *Return, *Break, *Continue:
		return true
	case *Block:
		if len(n.Forms) == 0 {
			return false
		}
		return leavesIteration(n.Forms[len(n.Forms)-1])
	case *Conditional:
		return n.Else != nil && leavesIteration(n.Then) && leavesIteration(n.Else)
	}
	return false
}

// conditionalExitNarrowings returns the type refinements that hold after a
// conditional statement completes normally. When one branch diverges (e.g.
// raises), the opposite branch's refinements are guaranteed to apply.
//
// For an `if (cond) { raise }` with no else, the falsy facts of `cond` apply
// to subsequent statements — the classic guard-clause pattern.
func conditionalExitNarrowings(cond *Conditional, env hm.Env) []Narrowing {
	facts := analyzeCondition(cond.Condition, env)

	thenDiverges := divergesNormally(cond.Then)

	if cond.Else == nil {
		// Without an else, the "else" path is empty fall-through. If the
		// then-branch diverges, anything after the conditional took the
		// implicit else, so the condition was falsy there.
		if thenDiverges {
			return facts.Falsy
		}
		return nil
	}

	elseDiverges := divergesNormally(cond.Else)

	switch {
	case thenDiverges && !elseDiverges:
		return facts.Falsy
	case elseDiverges && !thenDiverges:
		return facts.Truthy
	default:
		// Both diverge or neither diverges — nothing further to say.
		return nil
	}
}
