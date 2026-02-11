package dang

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// TryCatch is an expression that evaluates a body block and, if it raises
// an error, dispatches to catch clauses.  The catch block uses the same
// clause syntax as case:
//
//	try { ... } catch {
//	  v: ValidationError => handle(v)
//	  n: NotFoundError   => handle(n)
//	  err                => fallback(err)  # catch-all
//	}
type TryCatch struct {
	InferredTypeHolder
	TryBody *Block
	Clauses []*CaseClause
	Loc     *SourceLocation
}

var _ Node = (*TryCatch)(nil)
var _ Evaluator = (*TryCatch)(nil)

func (t *TryCatch) DeclaredSymbols() []string { return nil }

func (t *TryCatch) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, t.TryBody.ReferencedSymbols()...)
	for _, clause := range t.Clauses {
		if clause.Value != nil {
			symbols = append(symbols, clause.Value.ReferencedSymbols()...)
		}
		if clause.TypePattern != nil {
			symbols = append(symbols, clause.TypePattern.Name)
		}
		symbols = append(symbols, clause.Expr.ReferencedSymbols()...)
	}
	return symbols
}

func (t *TryCatch) Body() hm.Expression { return t }

func (t *TryCatch) GetSourceLocation() *SourceLocation { return t.Loc }

func (t *TryCatch) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(t, func() (hm.Type, error) {
		// Infer the try body type.
		bodyType, err := t.TryBody.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		errorType := hm.NonNullType{Type: ErrorType}

		// Build an implicit Case over Error! and infer each clause the
		// same way case does.
		implicitCase := &Case{
			Expr:    nil,
			Clauses: t.Clauses,
			Loc:     t.Loc,
		}

		resultType := bodyType

		for i, clause := range t.Clauses {
			var clauseType hm.Type

			if clause.IsTypePattern() {
				// Type pattern: v: SomeError => ...
				// Validate that the type implements Error.
				if err := implicitCase.inferTypePatternClause(ctx, env, fresh, clause, errorType); err != nil {
					return nil, err
				}

				clauseType, err = WithInferErrorHandling(clause, func() (hm.Type, error) {
					clauseEnv := env.Clone()
					clauseEnv = clauseEnv.Add(clause.Binding, hm.NewScheme(nil, hm.NonNullType{Type: clause.resolvedMemberType}))
					return clause.Expr.Infer(ctx, clauseEnv, fresh)
				})
				if err != nil {
					return nil, err
				}
			} else if clause.IsElse {
				// Catch-all: err => ...
				clauseType, err = WithInferErrorHandling(clause, func() (hm.Type, error) {
					clauseEnv := env.Clone()
					if clause.Binding != "" {
						clauseEnv = clauseEnv.Add(clause.Binding, hm.NewScheme(nil, errorType))
					}
					return clause.Expr.Infer(ctx, clauseEnv, fresh)
				})
				if err != nil {
					return nil, err
				}
			} else {
				return nil, NewInferError(fmt.Errorf("catch clauses must be type patterns or a catch-all"), clause)
			}

			// Unify this clause's return type with the body type.
			subs, err := hm.Assignable(clauseType, resultType)
			if err != nil {
				if i == 0 {
					return nil, NewInferError(err, clause)
				}
				return nil, NewInferError(fmt.Errorf("catch clause type mismatch: %s vs %s", clauseType, resultType), clause)
			}
			resultType = resultType.Apply(subs).(hm.Type)
		}

		// If either side is nullable the result is nullable.
		_, bodyNonNull := resultType.(hm.NonNullType)
		lastClause := t.Clauses[len(t.Clauses)-1]
		lastClauseType := lastClause.GetInferredType()
		if lastClauseType == nil {
			lastClauseType = resultType
		}
		_, handlerNonNull := lastClauseType.(hm.NonNullType)
		if bodyNonNull && !handlerNonNull {
			if nn, ok := resultType.(hm.NonNullType); ok {
				resultType = nn.Type
			}
		}

		return resultType, nil
	})
}

// RaisedError is a sentinel wrapper that carries an error value through
// Go's error interface so that Eval methods propagate it up the call
// stack until a TryCatch catches it.
type RaisedError struct {
	Value    Value // always a *ModuleValue implementing Error
	Location *SourceLocation
}

func (r *RaisedError) Error() string {
	if mv, ok := r.Value.(*ModuleValue); ok {
		if msg, found := mv.Get("message"); found {
			return msg.String()
		}
	}
	return "unknown error"
}

func (t *TryCatch) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, t, func() (Value, error) {
		val, err := EvalNode(ctx, env, t.TryBody)
		if err == nil {
			return val, nil
		}

		// Resolve the error value to bind in catch clauses.
		errVal := extractErrorValue(err)

		// Dispatch through clauses using resolved types from inference.
		for _, clause := range t.Clauses {
			if clause.IsElse {
				childEnv := env.Fork()
				if clause.Binding != "" {
					childEnv.Set(clause.Binding, errVal)
				}
				return EvalNode(ctx, childEnv, clause.Expr)
			}

			if clause.IsTypePattern() {
				if matchesType(errVal, clause.resolvedMemberType) {
					childEnv := env.Fork()
					childEnv.Set(clause.Binding, errVal)
					return EvalNode(ctx, childEnv, clause.Expr)
				}
				continue
			}
		}

		// No clause matched — re-raise the original error.
		return nil, err
	})
}

// extractErrorValue turns any caught error into a *ModuleValue.  User-
// level raises already carry one; runtime errors are wrapped in a
// BasicError.
func extractErrorValue(err error) Value {
	var raised *RaisedError
	if errors.As(err, &raised) {
		return raised.Value
	}
	// Wrap runtime errors in a BasicError.
	msg := err.Error()
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) {
		msg = sourceErr.Inner.Error()
	}
	return newBasicError(msg)
}

// newBasicError creates a *ModuleValue of type BasicError with the given
// message.
func newBasicError(message string) *ModuleValue {
	mv := NewModuleValue(BasicErrorType)
	mv.Set("message", StringValue{Val: message})
	mv.Visibilities["message"] = PublicVisibility
	return mv
}

func (t *TryCatch) Walk(fn func(Node) bool) {
	if !fn(t) {
		return
	}
	t.TryBody.Walk(fn)
	for _, clause := range t.Clauses {
		if clause.Value != nil {
			clause.Value.Walk(fn)
		}
		if clause.TypePattern != nil {
			fn(clause.TypePattern)
		}
		clause.Expr.Walk(fn)
	}
}

// Raise is a statement that raises an error.  The value can be a bare
// string (sugar for BasicError(message: "...")) or any value implementing
// the Error interface.
type Raise struct {
	InferredTypeHolder
	Value Node
	Loc   *SourceLocation
}

var _ Node = (*Raise)(nil)
var _ Evaluator = (*Raise)(nil)

func (r *Raise) DeclaredSymbols() []string { return nil }

func (r *Raise) ReferencedSymbols() []string {
	return r.Value.ReferencedSymbols()
}

func (r *Raise) Body() hm.Expression { return r }

func (r *Raise) GetSourceLocation() *SourceLocation { return r.Loc }

func (r *Raise) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(r, func() (hm.Type, error) {
		valType, err := r.Value.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// The raise value must be either a String! or implement Error.
		if _, strErr := hm.Assignable(valType, hm.NonNullType{Type: StringType}); strErr == nil {
			return hm.TypeVariable(fresh.Fresh()), nil
		}

		if _, errErr := hm.Assignable(valType, hm.NonNullType{Type: ErrorType}); errErr == nil {
			return hm.TypeVariable(fresh.Fresh()), nil
		}

		return nil, NewInferError(
			fmt.Errorf("raise requires a String! or Error!, got %s", valType),
			r.Value,
		)
	})
}

func (r *Raise) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	val, err := EvalNode(ctx, env, r.Value)
	if err != nil {
		return nil, err
	}

	switch v := val.(type) {
	case StringValue:
		return nil, &RaisedError{
			Value:    newBasicError(v.Val),
			Location: r.Loc,
		}
	case *ModuleValue:
		return nil, &RaisedError{Value: v, Location: r.Loc}
	default:
		return nil, fmt.Errorf("raise: expected String or Error, got %T", val)
	}
}

func (r *Raise) Walk(fn func(Node) bool) {
	if !fn(r) {
		return
	}
	r.Value.Walk(fn)
}
