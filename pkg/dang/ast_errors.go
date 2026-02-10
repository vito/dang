package dang

import (
	"context"
	"errors"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// TryCatch is an expression that evaluates a body block and, if it raises
// an error, evaluates a catch handler block with the error bound to a
// parameter.  The try and catch blocks must return the same type.
type TryCatch struct {
	InferredTypeHolder
	TryBody *Block
	Handler *BlockArg
	Loc     *SourceLocation
}

var _ Node = (*TryCatch)(nil)
var _ Evaluator = (*TryCatch)(nil)

func (t *TryCatch) DeclaredSymbols() []string { return nil }

func (t *TryCatch) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, t.TryBody.ReferencedSymbols()...)
	symbols = append(symbols, t.Handler.ReferencedSymbols()...)
	return symbols
}

func (t *TryCatch) Body() hm.Expression { return t }

func (t *TryCatch) GetSourceLocation() *SourceLocation { return t.Loc }

func (t *TryCatch) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(t, func() (hm.Type, error) {
		// Infer the try body type
		bodyType, err := t.TryBody.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// The handler receives one parameter of type Error! and must
		// return a type compatible with the body.
		errorType := hm.NonNullType{Type: ErrorType}
		t.Handler.ExpectedParamTypes = []hm.Type{errorType}
		t.Handler.ExpectedReturnType = bodyType

		handlerType, err := t.Handler.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// The handler must be a function type; extract its return type.
		handlerFn, ok := handlerType.(*hm.FunctionType)
		if !ok {
			return nil, NewInferError(fmt.Errorf("catch handler must be a function, got %s", handlerType), t.Handler)
		}

		handlerRet := handlerFn.Ret(false)

		// Unify the body and handler return types.
		subs, err := hm.Assignable(handlerRet, bodyType)
		if err != nil {
			return nil, NewInferError(err, t.Handler)
		}

		resultType := bodyType.Apply(subs).(hm.Type)

		// If either side is nullable the result is nullable.
		_, bodyNonNull := resultType.(hm.NonNullType)
		_, handlerNonNull := handlerRet.Apply(subs).(hm.Type).(hm.NonNullType)
		if bodyNonNull && !handlerNonNull {
			resultType = resultType.(hm.NonNullType).Type
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

		// Evaluate the catch handler with the error value bound.
		// When the error wraps a custom type (Original), bind that so
		// case-matching on the concrete type works naturally.
		var bindVal Value = errVal
		if errVal.Original != nil {
			bindVal = errVal.Original
		}
		handlerEnv := env.Clone()
		if len(t.Handler.Args) > 0 {
			handlerEnv.Set(t.Handler.Args[0].Name.Name, bindVal)
		}

		return EvalNode(ctx, handlerEnv, t.Handler.BodyNode)
	})
}

func (t *TryCatch) Walk(fn func(Node) bool) {
	if !fn(t) {
		return
	}
	t.TryBody.Walk(fn)
	t.Handler.Walk(fn)
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
