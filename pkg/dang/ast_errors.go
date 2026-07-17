package dang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/Khan/genqlient/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
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
	// inference warns with a migration hint.
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
			EmitInferWarning(ctx, t, "try/catch was replaced by postfix `rescue`; attach `rescue` to an expression or block — run `dang fmt -w` to migrate")
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
			checkRescueLaziness(ctx, env, t, bodyType)
			return mergeControlResultTypesTagged(
				bodyType, nodeOrigin("rescue operand", t.Operand),
				fallbackType, nodeOrigin("rescue fallback", t.Fallback),
			), nil
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
		operandSrc := nodeOrigin("rescue operand", t.Operand)

		var elseClause *CaseClause
		var priorPatterns []*CaseClause
		for _, clause := range t.Clauses {
			// A clause after an else catch-all can never match.
			if err := checkClauseReachable(clause, elseClause, nil); err != nil {
				return nil, err
			}

			var clauseType hm.Type

			if clause.IsTypePattern() {
				// Type pattern: v: SomeError => ...
				// Validate that the type implements Error.
				if err := implicitCase.inferTypePatternClause(ctx, env, fresh, clause, errorType); err != nil {
					return nil, err
				}
				if err := checkClauseReachable(clause, nil, priorPatterns); err != nil {
					return nil, err
				}
				priorPatterns = append(priorPatterns, clause)

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
				// Unlike case — whose nullable operands fall through every
				// type pattern — a rescue's operand is always Error!, so an
				// `e: Error` pattern is a complete catch-all and a later
				// else can never fire.
				for _, prev := range priorPatterns {
					if canonicalModule(prev.resolvedMemberType) == ErrorType {
						return nil, NewInferError(
							fmt.Errorf("unreachable clause: the %s clause on line %d already matches every error", prev.TypePattern.Name, prev.Loc.Line),
							clause,
						)
					}
				}
				elseClause = clause
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
			resultType = mergeControlResultTypesTagged(
				resultType, operandSrc,
				clauseType, armOrigin("rescue clause", clause.Loc),
			)
		}

		checkRescueLaziness(ctx, env, t, bodyType)

		return resultType, nil
	})
}

// RaisedError is a sentinel wrapper that carries an error value through
// Go's error interface so that Eval methods propagate it up the call
// stack until a RescueExpr rescues it.
//
// A RaisedError is immutable once returned: memoized lazy-slot errors mean
// the same wrapper pointer can resurface at multiple observation sites, so
// mutating one after the fact would retroactively rewrite history.
type RaisedError struct {
	Value    Value // always an *Object implementing Error
	Location *SourceLocation

	// Cause is the error that was being rescued when this one was raised,
	// recorded out-of-band (never surfaced through the Error interface or
	// the type system). It is left nil for a plain re-raise of the rescued
	// error itself, and when the raised value carries its own non-null
	// `cause` field — an explicit cause wins over the implicit record.
	Cause *RaisedError
}

// Error returns just the message field. Keep it that way: this string is
// load-bearing in the docs literate build, the playground, and the REPL,
// which all render failures from Error() — richer uncaught output belongs
// to the boundary printer, not here.
func (r *RaisedError) Error() string {
	if mv, ok := r.Value.(*Object); ok {
		if msg, found := mv.lookupValue("message"); found {
			return msg.String()
		}
	}
	return "unknown error"
}

// inFlightErrorKey carries the error currently being rescued through the
// dynamic extent of a rescue arm (clause expr or fallback), so that a
// raise evaluating during recovery can record it as the new error's cause.
type inFlightErrorKey struct{}

// raisedErrorFor returns the *RaisedError wrapper for an error caught by a
// rescue: the original wrapper when the error came from raise (preserving
// its location and cause chain), or a fresh one around the classified
// value otherwise.
func raisedErrorFor(err error, errVal Value) *RaisedError {
	var raised *RaisedError
	if errors.As(err, &raised) {
		return raised
	}
	return &RaisedError{Value: errVal}
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

		// Resolve the error value once, and expose it to the arm's dynamic
		// extent so a raise during recovery records it as the cause.
		errVal := extractErrorValue(err)
		armCtx := context.WithValue(ctx, inFlightErrorKey{}, raisedErrorFor(err, errVal))

		// Fallback form: any error yields the fallback value.
		if t.Fallback != nil {
			return EvalNode(armCtx, scope.Derive(true), t.Fallback)
		}

		// Dispatch through clauses using resolved types from inference.
		for _, clause := range t.Clauses {
			if clause.IsElse {
				childScope := scope.Derive(true)
				if clause.Binding != "" {
					childScope.Bind(clause.Binding, errVal, PrivateVisibility)
				}
				return EvalNode(armCtx, childScope, clause.Expr)
			}

			if clause.IsTypePattern() {
				if matchesType(errVal, clause.resolvedMemberType) {
					childScope := scope.Derive(true)
					childScope.Bind(clause.Binding, errVal, PrivateVisibility)
					return EvalNode(armCtx, childScope, clause.Expr)
				}
				continue
			}
		}

		// No clause matched — re-raise the original error.
		return nil, err
	})
}

// extractErrorValue turns any caught error into an *Object, classifying
// it into the built-in taxonomy. User-level raises already carry one;
// assert{} failures become AssertionError; errors returned in a GraphQL
// response become GraphQLError; every other fault becomes RuntimeError.
// BasicError is reserved for `raise "string"` (see Raise.Eval).
func extractErrorValue(err error) Value {
	var raised *RaisedError
	if errors.As(err, &raised) {
		return raised.Value
	}

	var assertErr *AssertionError
	if errors.As(err, &assertErr) {
		// Use Message, not Error(): the "  Location: file:line:col" suffix
		// is for uncaught rendering, not the caught value.
		return newTypedError(AssertionErrorType, assertErr.Message)
	}

	var gqlErrs gqlerror.List
	if errors.As(err, &gqlErrs) && len(gqlErrs) > 0 {
		return newGraphQLError(gqlErrs)
	}
	// genqlient's HTTPError (non-200 responses) has no Unwrap, so any
	// GraphQL errors in its body are invisible to errors.As above.
	var httpErr *graphql.HTTPError
	if errors.As(err, &httpErr) && len(httpErr.Response.Errors) > 0 {
		return newGraphQLError(httpErr.Response.Errors)
	}

	// Everything else — interpreter faults, transport failures — is a
	// RuntimeError carrying the innermost message.
	msg := err.Error()
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) {
		msg = sourceErr.Inner.Error()
	}
	return newTypedError(RuntimeErrorType, msg)
}

// newTypedError creates an *Object of the given prelude error type with
// the given message.
func newTypedError(mod *Type, message string) *Object {
	mv := NewObject(mod)
	mv.Bind("message", StringValue{Val: message}, PublicVisibility)
	return mv
}

// newBasicError creates an *Object of type BasicError with the given
// message.
func newBasicError(message string) *Object {
	return newTypedError(BasicErrorType, message)
}

// newGraphQLError builds a GraphQLError from the errors in a GraphQL
// response. The first error provides the message, path, and extensions;
// GraphQL servers (and Dagger in particular) return one error per failed
// request in practice.
func newGraphQLError(list gqlerror.List) *Object {
	first := list[0]

	mv := newTypedError(GraphQLErrorType, first.Message)

	elems := []Value{}
	for _, seg := range first.Path {
		switch s := seg.(type) {
		case ast.PathName:
			elems = append(elems, StringValue{Val: string(s)})
		case ast.PathIndex:
			elems = append(elems, StringValue{Val: strconv.Itoa(int(s))})
		}
	}
	mv.Bind("path", ListValue{
		Elements: elems,
		ElemType: hm.NonNullType{Type: StringType},
	}, PublicVisibility)

	extensions := "{}"
	if len(first.Extensions) > 0 {
		if encoded, err := json.Marshal(first.Extensions); err == nil {
			extensions = string(encoded)
		}
	}
	mv.Bind("extensions", StringValue{Val: extensions}, PublicVisibility)

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

	inFlight, _ := ctx.Value(inFlightErrorKey{}).(*RaisedError)

	switch v := val.(type) {
	case StringValue:
		// A string raise mints a fresh BasicError, which can never be the
		// rescued value itself, so the implicit cause always applies.
		return nil, &RaisedError{
			Value:    newBasicError(v.Val),
			Location: r.Loc,
			Cause:    inFlight,
		}
	case *Object:
		cause := inFlight
		if inFlight != nil && v == inFlight.Value {
			// Plain re-raise of the rescued error: no self-cause.
			cause = nil
		} else if hasExplicitCause(v) {
			// The raised value carries its own cause; explicit wins.
			cause = nil
		}
		return nil, &RaisedError{Value: v, Location: r.Loc, Cause: cause}
	default:
		return nil, fmt.Errorf("raise: expected String or Error, got %T", val)
	}
}

// hasExplicitCause reports whether an error object carries a non-null
// `cause` field of its own. lookupValue never forces pending initializers,
// and declared fields are always published by the time a constructed
// object reaches raise, so this stays pure.
func hasExplicitCause(v *Object) bool {
	cv, ok := v.lookupValue("cause")
	if !ok {
		return false
	}
	_, isNull := cv.(NullValue)
	return !isNull
}

func (r *Raise) Walk(fn func(Node) bool) {
	if !fn(r) {
		return
	}
	r.Value.Walk(fn)
}
