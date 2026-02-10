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
			Expr:    nil, // not used directly; we set the type below
			Clauses: t.Clauses,
			Loc:     t.Loc,
		}
		_ = implicitCase

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
					// First clause determines the handler type; unify
					// handler with body.
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

// RaisedError is a sentinel wrapper that carries an ErrorValue through
// Go's error interface so that Eval methods propagate it up the call
// stack until a TryCatch catches it.
type RaisedError struct {
	Value    *ErrorValue
	Location *SourceLocation
}

func (r *RaisedError) Error() string {
	return r.Value.Message
}

func (t *TryCatch) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, t, func() (Value, error) {
		val, err := EvalNode(ctx, env, t.TryBody)
		if err == nil {
			return val, nil
		}

		// Extract the ErrorValue: either from a user-level raise or by
		// wrapping a runtime error.
		var errVal *ErrorValue
		var raised *RaisedError
		if errors.As(err, &raised) {
			errVal = raised.Value
		} else {
			// Wrap runtime errors (division by zero, null access, GraphQL
			// failures, etc.) so the catch handler can inspect them.
			msg := err.Error()
			// Unwrap SourceError to get the underlying message.
			var sourceErr *SourceError
			if errors.As(err, &sourceErr) {
				msg = sourceErr.Inner.Error()
			}
			errVal = &ErrorValue{Message: msg}
		}

		// Resolve the value to bind in catch clauses.  Custom error
		// types expose the original ModuleValue so type patterns work.
		var bindVal Value = errVal
		if errVal.Original != nil {
			bindVal = errVal.Original
		}

		// Dispatch through clauses just like case does.
		for _, clause := range t.Clauses {
			if clause.IsElse {
				childEnv := env.Fork()
				if clause.Binding != "" {
					childEnv.Set(clause.Binding, bindVal)
				}
				return EvalNode(ctx, childEnv, clause.Expr)
			}

			if clause.IsTypePattern() {
				if matchesType(bindVal, clause.TypePattern.Name) {
					childEnv := env.Fork()
					childEnv.Set(clause.Binding, bindVal)
					return EvalNode(ctx, childEnv, clause.Expr)
				}
				continue
			}
		}

		// No clause matched — re-raise the original error.
		return nil, err
	})
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
// string (sugar for Error(message: "...")) or a full Error value.
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

		// The raise value must be either a String! or an Error!.
		strSubs, strErr := hm.Assignable(valType, hm.NonNullType{Type: StringType})
		if strErr == nil {
			_ = strSubs
			// raise "message" is valid — returns bottom type (never completes).
			return hm.TypeVariable(fresh.Fresh()), nil
		}

		errSubs, errErr := hm.Assignable(valType, hm.NonNullType{Type: ErrorType})
		if errErr == nil {
			_ = errSubs
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
			Value:    &ErrorValue{Message: v.Val},
			Location: r.Loc,
		}
	case *ErrorValue:
		return nil, &RaisedError{Value: v, Location: r.Loc}
	case *ModuleValue:
		// Custom type implementing Error — extract the message field.
		msgVal, ok := v.Get("message")
		if !ok {
			return nil, fmt.Errorf("raise: error value has no message field")
		}
		msg, ok := msgVal.(StringValue)
		if !ok {
			return nil, fmt.Errorf("raise: error message must be a String, got %T", msgVal)
		}
		return nil, &RaisedError{
			Value:    &ErrorValue{Message: msg.Val, Original: v},
			Location: r.Loc,
		}
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

// ErrorValue is the runtime representation of a Dang error.
// Original holds the full value when raised from a custom type
// implementing the Error interface, so catch handlers can pattern
// match on the concrete type.
type ErrorValue struct {
	Message  string
	Original Value // non-nil when raised from a custom Error type
}

func (e *ErrorValue) Type() hm.Type {
	return hm.NonNullType{Type: ErrorType}
}

func (e *ErrorValue) String() string {
	return fmt.Sprintf("Error(%s)", e.Message)
}

func (e *ErrorValue) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"message":%q}`, e.Message)), nil
}

// SelectField allows field access on error values. If the error wraps
// a custom type (Original), fields are looked up on that value first.
func (e *ErrorValue) SelectField(name string) (Value, error) {
	// If we have the original custom error, delegate to it so that
	// catch handlers can access type-specific fields.
	if e.Original != nil {
		if mv, ok := e.Original.(*ModuleValue); ok {
			if val, found := mv.Get(name); found {
				return val, nil
			}
		}
	}
	switch name {
	case "message":
		return StringValue{Val: e.Message}, nil
	default:
		return nil, fmt.Errorf("Error has no field %q", name)
	}
}
