package dang

// SPIKE: the `select` expression proposed in
// docs/proposals/concurrency-and-time.md. Each arm has a guard (any
// expression; it should block on something), an optional binding for the
// guard's value, an optional `if (cond)` that enables it, and a body. The
// enabled guards race concurrently (raceValues); the first to finish wins,
// the losers are cancelled and awaited, and then the winner's body runs
// sequentially in the enclosing scope, so `break`/`continue`/`return` and
// reassignments in a body behave like they do in a `case` clause.

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// SelectExpr is `select { arm... }`.
type SelectExpr struct {
	InferredTypeHolder
	Arms []*SelectArm
	Loc  *SourceLocation
}

// SelectArm is one `binding = guard if (cond) => body` arm, or `else => body`.
type SelectArm struct {
	Binding string // optional: names the guard's value inside Body
	Guard   Node   // nil for else
	Cond    Node   // optional enabling condition
	Body    Node
	IsElse  bool
	Loc     *SourceLocation
}

var _ Node = (*SelectExpr)(nil)
var _ SourceLocatable = (*SelectArm)(nil)

func (a *SelectArm) GetSourceLocation() *SourceLocation { return a.Loc }

func (s *SelectExpr) DeclaredSymbols() []string { return nil }

func (s *SelectExpr) ReferencedSymbols() []string {
	var symbols []string
	for _, arm := range s.Arms {
		if arm.Guard != nil {
			symbols = append(symbols, arm.Guard.ReferencedSymbols()...)
		}
		if arm.Cond != nil {
			symbols = append(symbols, arm.Cond.ReferencedSymbols()...)
		}
		symbols = append(symbols, arm.Body.ReferencedSymbols()...)
	}
	return symbols
}

func (s *SelectExpr) Body() hm.Expression { return s }

func (s *SelectExpr) GetSourceLocation() *SourceLocation { return s.Loc }

func (s *SelectExpr) Walk(fn func(Node) bool) {
	if !fn(s) {
		return
	}
	for _, arm := range s.Arms {
		if arm.Guard != nil {
			arm.Guard.Walk(fn)
		}
		if arm.Cond != nil {
			arm.Cond.Walk(fn)
		}
		arm.Body.Walk(fn)
	}
}

func (s *SelectExpr) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(s, func() (hm.Type, error) {
		var resultType hm.Type
		var first *SelectArm
		var elseArm *SelectArm
		guarded := 0
		for _, arm := range s.Arms {
			if elseArm != nil {
				return nil, NewInferError(
					fmt.Errorf("unreachable arm: follows the else arm on line %d", elseArm.Loc.Line),
					arm,
				)
			}

			armEnv := env
			if arm.IsElse {
				elseArm = arm
			} else {
				guarded++
				// A guard races on its own goroutine and may be cancelled, so
				// control flow cannot escape it: break/continue/return inside a
				// guard has nowhere sensible to go.
				guardCtx := contextWithInferFunctionControlBoundary(ctx)
				guardType, err := arm.Guard.Infer(guardCtx, env, fresh)
				if err != nil {
					return nil, err
				}
				if arm.Cond != nil {
					condType, err := arm.Cond.Infer(ctx, env, fresh)
					if err != nil {
						return nil, err
					}
					if _, err := hm.Assignable(condType, hm.NonNullType{Type: BooleanType}); err != nil {
						return nil, NewInferError(fmt.Errorf("condition must be Boolean, got %s", condType), arm.Cond)
					}
				}
				if arm.Binding != "" {
					armEnv = env.Clone().Add(arm.Binding, hm.NewScheme(nil, guardType))
				}
			}

			bodyType, err := WithInferErrorHandling(arm, func() (hm.Type, error) {
				return arm.Body.Infer(ctx, armEnv, fresh)
			})
			if err != nil {
				return nil, err
			}

			if first == nil {
				first = arm
				resultType = bodyType
				continue
			}
			// Exactly one body runs, so bodies merge like case clauses,
			// widening to a union when they diverge.
			resultType = mergeControlResultTypesTagged(
				resultType, armOrigin("select arm", first.Loc),
				bodyType, armOrigin("select arm", arm.Loc),
			)
		}
		if guarded == 0 {
			return nil, fmt.Errorf("select requires at least one arm with a guard")
		}
		return resultType, nil
	})
}

func (s *SelectExpr) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, s, func() (Value, error) {
		var enabled []*SelectArm
		var elseArm *SelectArm
		for _, arm := range s.Arms {
			if arm.IsElse {
				elseArm = arm
				continue
			}
			if arm.Cond != nil {
				condVal, err := EvalNode(ctx, scope, arm.Cond)
				if err != nil {
					return nil, err
				}
				b, ok := condVal.(BoolValue)
				if !ok {
					return nil, fmt.Errorf("select arm condition must be Boolean!, got %T", condVal)
				}
				if !b.Val {
					continue
				}
			}
			enabled = append(enabled, arm)
		}

		if len(enabled) == 0 {
			if elseArm != nil {
				return EvalNode(ctx, scope, elseArm.Body)
			}
			return nil, fmt.Errorf("select: every arm is disabled and there is no else arm")
		}

		// Guards run concurrently in sealed child scopes, like `{{ }}`
		// fields, so their incidental writes stay private; they see no break
		// or return targets (inference already rejected those).
		guardCtx := contextWithReturnFrame(contextWithFunctionControlBoundary(ctx), nil)
		idx, val, err := raceValues(guardCtx, len(enabled), func(ctx context.Context, i int) (Value, error) {
			return EvalNode(ctx, scope.Derive(true), enabled[i].Guard)
		})
		if err != nil {
			return nil, err
		}

		// The winner's body runs sequentially on the caller's goroutine, in
		// an unsealed child scope: the binding stays local while
		// reassignments of enclosing bindings walk outward.
		arm := enabled[idx]
		bodyScope := scope
		if arm.Binding != "" {
			bodyScope = scope.Derive(false)
			bodyScope.Bind(arm.Binding, val, PrivateVisibility)
		}
		return EvalNode(ctx, bodyScope, arm.Body)
	})
}
