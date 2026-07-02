package dang

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// RescueExpr is the postfix error-handling expression.  The operand is any
// expression — commonly a call chain or a block — and if evaluating it
// raises, the handler runs.  Clause form dispatches on the error's type
// using the same type patterns as case and re-raises when nothing matches;
// fallback form yields a replacement value on any error:
//
//	validate(name) rescue {
//	  v: ValidationError => v.field
//	  e: Error           => e.message   # typed catch-all, binds the error
//	  else               => "?"         # catch-all, discards the error
//	}
//
//	dir.file("VERSION").contents rescue null
type RescueExpr struct {
	InferredTypeHolder
	Operand Node
	Clauses []*CaseClause
	// Fallback is the expression after `rescue` in the fallback form;
	// mutually exclusive with Clauses.
	Fallback Node
	// Legacy marks a node parsed from the removed `try { } catch { }`
	// block syntax: the formatter rewrites it to the postfix form, and
	// inference rejects it with a migration hint.
	Legacy bool
	Loc    *SourceLocation
}

var _ Node = (*RescueExpr)(nil)
var _ Evaluator = (*RescueExpr)(nil)

func (t *RescueExpr) DeclaredSymbols() []string { return nil }

func (t *RescueExpr) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, t.Operand.ReferencedSymbols()...)
	if t.Fallback != nil {
		symbols = append(symbols, t.Fallback.ReferencedSymbols()...)
	}
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

func (t *RescueExpr) Body() hm.Expression { return t }

func (t *RescueExpr) GetSourceLocation() *SourceLocation { return t.Loc }

func (t *RescueExpr) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(t, func() (hm.Type, error) {
		if t.Legacy {
			return nil, NewInferError(
				fmt.Errorf("try/catch was replaced by postfix `rescue`; attach `rescue` to an expression or block — run `dang fmt -w` to migrate"),
				t,
			)
		}

		bodyType, err := t.Operand.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Fallback form: `expr rescue fallback` merges the two types the
		// same way an else clause would.
		if t.Fallback != nil {
			fallbackType, err := t.Fallback.Infer(ctx, env, fresh)
			if err != nil {
				return nil, err
			}
			return mergeControlResultTypes(bodyType, fallbackType), nil
		}

		if len(t.Clauses) == 0 {
			return nil, NewInferError(
				fmt.Errorf("rescue requires at least one clause; to replace any error with a value, use the fallback form: `expr rescue value`"),
				t,
			)
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

		for _, clause := range t.Clauses {
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
				// A binding on a catch-all can only come from the removed
				// bare-binding form (`err =>`), which parses so it can be
				// rejected with a targeted error here.
				if clause.Binding != "" {
					return nil, NewInferError(
						fmt.Errorf("bare catch-all `%s =>` is no longer supported; bind the error with `%s: Error =>` or discard it with `else =>`", clause.Binding, clause.Binding),
						clause,
					)
				}
				// Catch-all: else => ...
				clauseType, err = WithInferErrorHandling(clause, func() (hm.Type, error) {
					return clause.Expr.Infer(ctx, env.Clone(), fresh)
				})
				if err != nil {
					return nil, err
				}
			} else {
				return nil, NewInferError(fmt.Errorf("rescue clauses must be type patterns or a catch-all"), clause)
			}

			// Arms that diverge from the body (or each other) widen to a
			// union, same as if branches and case clauses. There is no null
			// fallthrough to account for: a catch with no matching clause
			// re-raises rather than yielding null.
			resultType = mergeControlResultTypes(resultType, clauseType)
		}

		return resultType, nil
	})
}

// RaisedError is a sentinel wrapper that carries an error value through
// Go's error interface so that Eval methods propagate it up the call
// stack until a RescueExpr rescues it.
type RaisedError struct {
	Value    Value // always an *Object implementing Error
	Location *SourceLocation
}

func (r *RaisedError) Error() string {
	if mv, ok := r.Value.(*Object); ok {
		if msg, found := mv.lookupValue("message"); found {
			return msg.String()
		}
	}
	return "unknown error"
}

func (t *RescueExpr) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, t, func() (Value, error) {
		val, err := EvalNode(ctx, scope, t.Operand)
		if err == nil {
			return val, nil
		}

		if isControlFlowException(err) {
			return nil, err
		}

		// Fallback form: any error yields the fallback value.
		if t.Fallback != nil {
			return EvalNode(ctx, scope.Derive(true), t.Fallback)
		}

		// Resolve the error value to bind in rescue clauses.
		errVal := extractErrorValue(err)

		// Dispatch through clauses using resolved types from inference.
		for _, clause := range t.Clauses {
			if clause.IsElse {
				childScope := scope.Derive(true)
				if clause.Binding != "" {
					childScope.Bind(clause.Binding, errVal, PrivateVisibility)
				}
				return EvalNode(ctx, childScope, clause.Expr)
			}

			if clause.IsTypePattern() {
				if matchesType(errVal, clause.resolvedMemberType) {
					childScope := scope.Derive(true)
					childScope.Bind(clause.Binding, errVal, PrivateVisibility)
					return EvalNode(ctx, childScope, clause.Expr)
				}
				continue
			}
		}

		// No clause matched — re-raise the original error.
		return nil, err
	})
}

// extractErrorValue turns any caught error into an *Object.  User-
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

// newBasicError creates an *Object of type BasicError with the given
// message.
func newBasicError(message string) *Object {
	mv := NewObject(BasicErrorType)
	mv.Bind("message", StringValue{Val: message}, PublicVisibility)
	return mv
}

func (t *RescueExpr) Walk(fn func(Node) bool) {
	if !fn(t) {
		return
	}
	t.Operand.Walk(fn)
	if t.Fallback != nil {
		t.Fallback.Walk(fn)
	}
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

func (r *Raise) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	val, err := EvalNode(ctx, scope, r.Value)
	if err != nil {
		return nil, err
	}

	switch v := val.(type) {
	case StringValue:
		return nil, &RaisedError{
			Value:    newBasicError(v.Val),
			Location: r.Loc,
		}
	case *Object:
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
