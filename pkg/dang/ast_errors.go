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
//	  v: ValidationError! => v.field
//	  e: Error!           => e.message   # typed catch-all, binds the error
//	  else                => "?"         # catch-all, discards the error
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
	// TryCatch marks a node parsed from the `try { } catch { }` block
	// syntax, which Dang 2.1 replaced with the postfix form: the formatter
	// keeps it where postfix rescue is unavailable and rewrites it to the
	// postfix form where it is (see modernizeTryCatch).
	TryCatch bool
	// Legacy marks a TryCatch node parsed in a version that has postfix
	// rescue, where try/catch is deprecated: inference warns with a
	// migration hint.
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

		// Writes before a raise in the operand reach the code after it.
		bodyType, err := t.Operand.Infer(withinRescue(ctx), env, fresh)
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

		// A try/catch keeps its own rules, in every version: a catch with no
		// clauses re-raises every error, and clauses that can never match
		// are dead code, not errors. (Before 2.1 it is the error handling
		// syntax; from 2.1 on it still runs as it did, with a warning.)
		current := t.TryCatch

		if len(t.Clauses) == 0 {
			if current {
				return bodyType, nil
			}
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
			if err := checkClauseReachable(clause, elseClause, nil); err != nil && !current {
				return nil, err
			}

			var clauseType hm.Type

			if clause.IsTypePattern() {
				// Type pattern: v: SomeError! => ...
				// Validate that the type implements Error.
				if err := implicitCase.inferTypePatternClause(ctx, env, fresh, clause, errorType); err != nil {
					return nil, err
				}
				if clause.matchType != nil && !current {
					if bad := unraisableMember(clause.matchType); bad != nil {
						return nil, NewInferError(fmt.Errorf("rescue pattern %s includes %s, which isn't an Error, so it is never raised", clause.matchType, unwrapNonNull(bad)), clause.patternNode())
					}
				}
				if err := checkClauseReachable(clause, nil, priorPatterns); err != nil && !current {
					return nil, err
				}
				priorPatterns = append(priorPatterns, clause)

				clauseType, err = WithInferErrorHandling(clause, func() (hm.Type, error) {
					clauseEnv := env.Clone()
					clauseEnv = clauseEnv.Add(clause.Binding, hm.NewScheme(nil, clause.matchType))
					return clause.Expr.Infer(ctx, clauseEnv, fresh)
				})
				if err != nil {
					return nil, err
				}
			} else if clause.IsElse {
				// A binding on a catch-all can only come from the removed
				// bare-binding form (`err =>`), which parses so it can be
				// rejected with a targeted error here. Try/catch blocks
				// predate the removal: they warn instead where try/catch
				// itself is deprecated, and the binding behaves like
				// `err: Error! =>`.
				if clause.Binding != "" {
					if !t.TryCatch {
						return nil, NewInferError(
							fmt.Errorf("bare catch-all `%s =>` is no longer supported; bind the error with `%s: Error! =>` or discard it with `else =>`", clause.Binding, clause.Binding),
							clause,
						)
					}
					if t.Legacy {
						EmitInferWarning(ctx, clause, fmt.Sprintf("bare catch-all `%s =>` is no longer supported; bind the error with `%s: Error! =>` or discard it with `else =>`", clause.Binding, clause.Binding))
					}
				}
				// Unlike case — whose nullable operands fall through every
				// type pattern — a rescue's operand is always Error!, so an
				// `e: Error` pattern is a complete catch-all and a later
				// else can never fire.
				for _, prev := range priorPatterns {
					if prev.resolvedMemberType != nil && canonicalModule(prev.resolvedMemberType) == ErrorType && !current {
						return nil, NewInferError(
							fmt.Errorf("unreachable clause: the %s clause on line %d already matches every error", prev.patternSource(), prev.Loc.Line),
							clause,
						)
					}
				}
				elseClause = clause
				// Catch-all: else => ...
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
	// Value is the error raised: an *Object implementing Error, or a
	// GraphQL object of a type that satisfies it, such as Dagger's Error
	// (see structural.go).
	Value    Value
	Location *SourceLocation

	// Cause is the error that was being rescued when this one was raised,
	// recorded out-of-band (never surfaced through the Error interface or
	// the type system). It is left nil for a plain re-raise of the rescued
	// error itself, and when the raised value carries its own non-null
	// `cause` field — an explicit cause wins over the implicit record.
	Cause *RaisedError

	// message is the raised value's message, read when it was raised, where
	// reading it takes a query (a GraphQL Value's) or a call (an object's
	// computed message), which Error has no context to make. messageRead
	// says whether it was.
	message     string
	messageRead bool

	// origin is the Go error a rescued value was classified from (see
	// extractErrorValue), such as the GraphQL error a GraphQLError was made
	// of. A plain re-raise of the rescued value carries it on (see Origin).
	origin error

	// explicitCause is the value's explicit `cause` field, raised with its
	// message read (see explicitCauses), for the uncaught report to show,
	// though it can neither query nor call (see nextCause).
	explicitCause *RaisedError
}

// Error returns just the message field. Keep it that way: this string is
// load-bearing in the docs literate build, the playground, and the REPL,
// which all render failures from Error() — richer uncaught output belongs
// to the boundary printer, not here.
func (r *RaisedError) Error() string {
	if r.messageRead {
		return r.message
	}
	if mv, ok := r.Value.(*Object); ok {
		if msg, found := mv.lookupValue("message"); found {
			return msg.String()
		}
	}
	return "unknown error"
}

// Origin returns the Go error the raised value was made of, when it is a
// rescued error raised again as it was: after `x rescue { e: GraphQLError!
// => raise e }`, it is the GraphQL error x failed with, extensions and all,
// for a host to report as it would have had nothing rescued it. A value
// raised any other way has none.
//
// It is deliberately not Unwrap: the origin carries the failure's own
// source location, which errors.As would then find in place of the raise.
func (r *RaisedError) Origin() error {
	return r.origin
}

// raisedErrorFor returns the *RaisedError wrapper for an error caught by a
// rescue: the original wrapper when the error came from raise (preserving
// its location and cause chain), or a fresh one around the classified
// value otherwise, which remembers the error it was classified from.
func raisedErrorFor(err error, errVal Value) *RaisedError {
	var raised *RaisedError
	if errors.As(err, &raised) {
		return raised
	}
	return &RaisedError{Value: errVal, origin: err}
}

func (t *RescueExpr) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, t, func() (Value, error) {
		val, err := EvalNode(ctx, scope, t.Operand)
		if err == nil {
			return val, nil
		}

		if isControlFlowException(err) || isCancelled(err) {
			return nil, err
		}

		// Once the evaluation is cancelled, nothing is rescued: whatever
		// failed (say, a call cut short) failed because of it, so report the
		// cancellation.
		if cancelled := evalCancelled(ctx); cancelled != nil {
			return nil, cancelled
		}

		// Resolve the error value once, and expose it to the arm's dynamic
		// extent so a raise during recovery records it as the cause.
		errVal := extractErrorValue(err)
		armFrame := deriveFrame(ctx)
		armFrame.inFlight = raisedErrorFor(err, errVal)
		armCtx := withFrame(ctx, armFrame)

		// Arms run in unsealed child scopes, like a block's, so they can
		// update enclosing bindings (e.g. a retry counter). An arm's error
		// binding is still bound privately in its own child scope.

		// Fallback form: any error yields the fallback value.
		if t.Fallback != nil {
			return EvalNode(armCtx, scope.Derive(false), t.Fallback)
		}

		// Dispatch through clauses using resolved types from inference.
		for _, clause := range t.Clauses {
			if clause.IsElse {
				childScope := scope.Derive(false)
				if clause.Binding != "" {
					childScope.Bind(clause.Binding, errVal, PrivateVisibility)
				}
				return EvalNode(armCtx, childScope, clause.Expr)
			}

			if clause.IsTypePattern() {
				if valueInhabits(errVal, clause.matchType, matchMode{}) {
					childScope := scope.Derive(false)
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
		if clause.TypePattern != nil && clause.PatternBase == nil {
			// Not for a qualified pattern: see Case.Walk.
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

		// An object of another shape says what it lacks to be an Error.
		if mod, ok := unwrapNonNull(valType).(*Type); ok && mod.Named != "" && (mod.Kind == ObjectKind || mod.Kind == InterfaceKind) {
			if reason := structuralMismatch(mod, ErrorType, hm.AssignableUncommitted); reason != "" {
				return nil, NewInferError(
					fmt.Errorf("raise requires a String! or Error!, got %s: %s", valType, reason),
					r.Value,
				)
			}
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

	inFlight := currentFrame(ctx).inFlight

	switch v := val.(type) {
	case StringValue:
		// A string raise mints a fresh BasicError, which can never be the
		// rescued value itself, so the implicit cause always applies.
		return nil, &RaisedError{
			Value:    newBasicError(v.Val),
			Location: r.Loc,
			Cause:    inFlight,
		}
	case *Object, GraphQLValue:
		if inFlight != nil && sameErrorValue(val, inFlight.Value) {
			// Plain re-raise of the rescued error: the error itself,
			// raised again here with all it had: its message, its causes
			// and the Go error behind it (see Origin).
			again := *inFlight
			again.Location = r.Loc
			return nil, &again
		}
		raised, err := raisedValue(ctx, scope, val, r.Loc)
		if err != nil {
			return nil, err
		}
		if obj, ok := val.(*Object); ok && hasExplicitCause(obj) {
			// The raised value carries its own cause; explicit wins.
			raised.explicitCause, err = explicitCauses(ctx, scope, obj, inFlight)
			if err != nil {
				return nil, err
			}
		} else {
			raised.Cause = inFlight
		}
		return nil, raised
	default:
		return nil, fmt.Errorf("raise: expected String or Error, got %T", val)
	}
}

// maxCauseDepth bounds the explicit `cause` chain a raise records, which a
// program can build.
const maxCauseDepth = 8

// raisedValue wraps val, an error object or a GraphQL object of a type
// satisfying Error (see structural.go), as raised at loc. Its message is
// read now where reading it takes a query or a call, which Error has no
// context to make. A message that fails to read doesn't fail the raise,
// which still raises val, so that a rescue can match it: the failure is
// the message. Only a cancellation stops it.
func raisedValue(ctx context.Context, scope ValueScope, val Value, loc *SourceLocation) (*RaisedError, error) {
	raised := &RaisedError{Value: val, Location: loc}
	var err error
	switch v := val.(type) {
	case *Object:
		raised.message, raised.messageRead, err = computedErrorMessage(ctx, scope, v)
	case GraphQLValue:
		raised.message, err = graphQLErrorMessage(ctx, scope, v)
		raised.messageRead = true
	}
	if err != nil {
		if isCancelled(err) || evalCancelled(ctx) != nil {
			return nil, err
		}
		raised.message, raised.messageRead = fmt.Sprintf("(its message failed: %s)", err), true
	}
	return raised, nil
}

// explicitCauses builds the chain of explicit `cause` fields from v, each
// raised with its message read (see raisedValue), so that the report shows
// every link though it can neither query nor call. A link that is the
// error being rescued, inFlight, is that very error, whose own causes
// follow it.
func explicitCauses(ctx context.Context, scope ValueScope, v *Object, inFlight *RaisedError) (*RaisedError, error) {
	var head, tail *RaisedError
	seen := map[*Object]bool{v: true}
	current := v
	for depth := 0; depth < maxCauseDepth; depth++ {
		cv, found := current.lookupValue("cause")
		if !found {
			break
		}
		obj, isObject := cv.(*Object)
		_, isGraphQL := cv.(GraphQLValue)
		if !isObject && !isGraphQL || isObject && seen[obj] {
			break
		}
		var link *RaisedError
		if inFlight != nil && sameErrorValue(cv, inFlight.Value) {
			link = inFlight
		} else {
			var err error
			link, err = raisedValue(ctx, scope, cv, nil)
			if err != nil {
				return nil, err
			}
		}
		if head == nil {
			head = link
		} else {
			tail.explicitCause = link
		}
		tail = link
		if link == inFlight || !isObject {
			break
		}
		seen[obj] = true
		current = obj
	}
	return head, nil
}

// sameErrorValue reports whether a and b are the same error: the same
// object, or handles on the same GraphQL object.
func sameErrorValue(a, b Value) bool {
	switch v := a.(type) {
	case *Object:
		obj, ok := b.(*Object)
		return ok && obj == v
	case GraphQLValue:
		return sameGraphQLObject(v, b)
	}
	return false
}

// computedErrorMessage reads the message of an error object whose message
// is a method rather than a stored field, for Error to report: it can't
// call one. read is false for a stored message.
func computedErrorMessage(ctx context.Context, scope ValueScope, v *Object) (message string, read bool, err error) {
	stored, found := v.lookupValue("message")
	if !found {
		return "", false, nil
	}
	fn, ok := stored.(FunctionValue)
	if !ok {
		return "", false, nil
	}
	called, err := autoCallFn(ctx, scope, BoundMethod{Method: fn, Receiver: v})
	if err != nil {
		return "", false, err
	}
	str, ok := called.(StringValue)
	if !ok {
		return "", false, fmt.Errorf("raise: the message of %s is %s, not a String", v.Mod, called)
	}
	return str.Val, true, nil
}

// graphQLErrorMessage reads the message of a GraphQL object raised as an
// error.
func graphQLErrorMessage(ctx context.Context, scope ValueScope, v GraphQLValue) (string, error) {
	field, err := v.SelectField(ctx, "message")
	if err != nil {
		return "", fmt.Errorf("raise: reading the message of %s: %w", v.TypeName, err)
	}
	message, err := autoCallFn(ctx, scope, field)
	if err != nil {
		return "", fmt.Errorf("raise: reading the message of %s: %w", v.TypeName, err)
	}
	str, ok := message.(StringValue)
	if !ok {
		return "", fmt.Errorf("raise: the message of %s is %s, not a String", v.TypeName, message)
	}
	return str.Val, nil
}

// sameGraphQLObject reports whether a and b are handles on the same GraphQL
// object: the same query, or the same ID.
func sameGraphQLObject(a GraphQLValue, b Value) bool {
	g, ok := b.(GraphQLValue)
	if !ok || a.TypeName != g.TypeName {
		return false
	}
	if a.KnownID != "" || g.KnownID != "" {
		return a.KnownID == g.KnownID
	}
	return a.QueryChain == g.QueryChain
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
