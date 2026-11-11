package dang

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// Return exits the current function early with a value.
type Return struct {
	InferredTypeHolder
	Value      Node
	ValueType  hm.Type
	Target     *InferControlTarget
	TargetKind ControlFrameKind
	Loc        *SourceLocation
}

var _ Node = (*Return)(nil)
var _ Evaluator = (*Return)(nil)

func (r *Return) DeclaredSymbols() []string { return nil }

func (r *Return) ReferencedSymbols() []string {
	return r.Value.ReferencedSymbols()
}

func (r *Return) Body() hm.Expression { return r }

func (r *Return) GetSourceLocation() *SourceLocation { return r.Loc }

func (r *Return) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(r, func() (hm.Type, error) {
		target := currentInferReturnTarget(ctx)
		if target == nil {
			return nil, NewInferError(fmt.Errorf("return outside of function"), r)
		}
		r.Target = target
		r.TargetKind = target.Kind
		markNonLocalReturn(ctx, r)

		valueType, err := r.Value.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		r.ValueType = valueType

		// Like raise/break/continue, return never produces a local value for the
		// surrounding expression. Use a fresh type so conditionals/cases containing
		// return can still type-check; function inference validates ValueType.
		t := fresh.Fresh()
		r.SetInferredType(t)
		return t, nil
	})
}

func (r *Return) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	target := currentReturnFrame(ctx)
	if target == nil {
		return nil, &ReturnException{Target: target, Location: r.Loc}
	}

	val, err := EvalNode(ctx, scope, r.Value)
	if err != nil {
		return nil, err
	}
	return nil, &ReturnException{Target: target, Value: val, Location: r.Loc}
}

func (r *Return) Walk(fn func(Node) bool) {
	if !fn(r) {
		return
	}
	r.Value.Walk(fn)
}

// ReturnException carries a function return value up the evaluator stack until
// the nearest function call boundary catches it.
type ReturnException struct {
	Target   *ControlFrame
	Value    Value
	Location *SourceLocation
}

func (e *ReturnException) Error() string {
	return "return outside of function"
}

func returnValueFromError(err error, frame *ControlFrame) (Value, bool) {
	var ret *ReturnException
	if errors.As(err, &ret) && controlFrameMatches(ret.Target, frame) {
		if ret.Value == nil {
			return NullValue{}, true
		}
		return ret.Value, true
	}
	return nil, false
}

func isReturnException(err error) bool {
	var ret *ReturnException
	return errors.As(err, &ret)
}

func collectReturnStatements(root Node, target *InferControlTarget) []*Return {
	if root == nil {
		return nil
	}

	var returns []*Return
	root.Walk(func(node Node) bool {
		if node != root {
			switch node.(type) {
			case *FunDecl, *NewConstructorDecl, *ObjectDecl:
				return false
			}
		}

		if ret, ok := node.(*Return); ok {
			if target == nil || ret.Target == target {
				returns = append(returns, ret)
			}
			// A return's value is evaluated before the return is raised, so
			// nested returns inside the value can be the one that actually
			// escapes and must be checked too.
			return true
		}

		return true
	})
	return returns
}

func returnValueType(ret *Return) hm.Type {
	if ret.ValueType != nil {
		return ret.ValueType
	}
	if ret.Value != nil {
		return ret.Value.GetInferredType()
	}
	return nil
}

// returnMismatch reports a returned type that doesn't fit the declared one,
// keeping the cause when it is a generic type escaping into a binding, or a
// possibly-null value for a generic one (which says how to fix it).
func returnMismatch(declared, inferred hm.Type, cause error) error {
	var escape hm.EscapeError
	var nullable *nullableRigidError
	if errors.As(cause, &escape) || errors.As(cause, &nullable) {
		return fmt.Errorf("return type mismatch: declared %s, inferred %s: %w", declared, inferred, cause)
	}
	return fmt.Errorf("return type mismatch: declared %s, inferred %s", declared, inferred)
}

// rejectedLiteral returns err when it is a literal that checkLiterals rejected
// (an enum value it doesn't name, or text a Go scalar hook refuses), which
// explains itself and points at the literal, or nil otherwise.
func rejectedLiteral(err error) error {
	var literalErr *InferError
	if errors.As(err, &literalErr) {
		return err
	}
	return nil
}

// env is the function body's environment, where its result materializes.
func inferReturnTypeWithEarlyReturns(env hm.Env, body Node, bodyType hm.Type, declaredType hm.Type, target *InferControlTarget) (hm.Type, error) {
	returns := collectReturnStatements(body, target)

	if declaredType != nil {
		subs, err := assignableForValue(env, bodyType, declaredType, body)
		if err != nil {
			if literalErr := rejectedLiteral(err); literalErr != nil {
				return nil, literalErr
			}
			return nil, NewInferError(
				withUnionProvenance(returnMismatch(declaredType, bodyType, err), bodyType),
				body,
			)
		}
		// The declared type wins: user asked for it, and a literal-coercion
		// fallback produces an empty substitution against bodyType anyway.
		effectiveType := declaredType.Apply(subs).(hm.Type)

		for _, ret := range returns {
			retType := returnValueType(ret)
			if retType == nil {
				continue
			}
			retSubs, err := assignableForValue(env, retType, declaredType, ret.Value)
			if err != nil {
				if literalErr := rejectedLiteral(err); literalErr != nil {
					return nil, literalErr
				}
				return nil, NewInferError(
					withUnionProvenance(returnMismatch(declaredType, retType, err), retType),
					ret.Value,
				)
			}
			effectiveType = effectiveType.Apply(retSubs).(hm.Type)
		}

		return effectiveType, nil
	}

	effectiveType := bodyType
	for _, ret := range returns {
		retType := returnValueType(ret)
		if retType == nil {
			continue
		}

		merged, err := mergeInferredReturnTypes(effectiveType, retType)
		if err != nil {
			return nil, NewInferError(
				fmt.Errorf("return type mismatch: inferred %s and %s", effectiveType, retType),
				ret.Value,
			)
		}
		effectiveType = merged
	}

	return effectiveType, nil
}

func mergeInferredReturnTypes(current hm.Type, next hm.Type) (hm.Type, error) {
	merged, _, err := hm.MergeTypes(current, next)
	return merged, err
}
