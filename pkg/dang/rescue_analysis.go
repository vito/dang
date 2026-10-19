package dang

import (
	"context"
	"fmt"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
)

// Static laziness analysis for rescue expressions.
//
// GraphQL field access is lazy: selecting an object-typed field only extends
// a pending query chain; nothing is sent until a leaf field forces it — a
// field whose underlying schema type is a scalar or enum, including
// @expectedType-mapped fields like Dagger's `sync` — or a `.{{ }}` selection
// runs, or a handle that may be null escapes the chain and is probed for
// null (a nullable object field, or anything selected off one, that is not
// itself selected from). Failures therefore happen at those execution points,
// not where the chain is built. Two consequences worth diagnosing:
//
//   - a rescue whose operand only builds a chain can never fire, and
//   - a still-lazy handle that leaves a rescue carries its failures past the
//     handler to wherever it is eventually forced.
//
// Both are warnings, not errors, by design: the analysis is heuristic in
// the permissive direction (anything it cannot classify counts as
// fallible), and inert-but-harmless code — `123 rescue 456` in a demo or
// teaching snippet — should still run. It only speaks up when the operand
// is flagrantly disconnected from where failures happen.
//
// One subtlety behind the "dead" classification: a read of a
// forward-referenced binding can force an initializer that raises
// mid-rescue, and the rescue catches it — but the binding's own
// declaration-slot forcing then re-raises the memoized error uncaught, so
// such a program dies regardless. Reads count as infallible here without
// making the warning a lie worth erroring over.

// gqlFieldClass classifies how referencing a GraphQL schema field behaves at
// runtime.
type gqlFieldClass int

const (
	// notGQLField: not resolvable as a schema field.
	notGQLField gqlFieldClass = iota
	// gqlChainField extends the pending query chain; it cannot fail.
	gqlChainField
	// gqlLeafField sends the accumulated query; failures surface here.
	gqlLeafField
)

// checkRescueLaziness emits warnings for rescue expressions whose handler
// cannot observe the failures the author most plausibly meant to handle.
// Called after the operand (and the rest of the rescue) inferred cleanly.
func checkRescueLaziness(ctx context.Context, env hm.Env, rescue *RescueExpr, operandType hm.Type) {
	if !rescueOperandCanFail(env, rescue.Operand) {
		EmitInferWarning(ctx, rescue, deadRescueMessage(env, rescue.Operand))
		return
	}

	// The operand can fail, but if its result is a still-lazy handle the
	// pipeline behind it has not run: failures of that pipeline surface
	// wherever the handle is forced, outside this rescue.
	if schemaObjectModule(operandType) != nil {
		var tails []Node
		collectResultTails(rescue.Operand, &tails)
		for _, tail := range tails {
			if mod := tailLeavesLazyHandle(env, tail); mod != nil {
				EmitInferWarning(ctx, tail, lazyResultMessage(mod))
			}
		}
	}

	checkLazyEscapes(ctx, env, rescue.Operand)
}

func deadRescueMessage(env hm.Env, operand Node) string {
	msg := "this rescue can never fire: nothing inside it can fail"
	if field, mod := trailingChainField(env, operand); field != "" {
		msg = fmt.Sprintf(
			"this rescue can never fire: `%s` only builds a GraphQL query — no request is sent inside the rescue",
			field,
		)
		if hint := leafFieldHint(mod); hint != "" {
			msg += "; failures happen where the chain executes — rescue at a leaf field (" + hint + ") or where the value is used"
		}
	}
	return msg
}

func lazyResultMessage(mod *Type) string {
	msg := fmt.Sprintf(
		"a lazy %s leaves this rescue without executing: failures in its pipeline will surface where it is used, outside this rescue",
		mod.Named,
	)
	if hint := leafFieldHint(mod); hint != "" {
		msg += "; force it inside the rescue with a leaf field (" + hint + ")"
	}
	return msg
}

// trailingChainField names the chain-building field a dead operand ends in,
// along with the module it returns, for the specialized diagnostic.
func trailingChainField(env hm.Env, operand Node) (string, *Type) {
	var tails []Node
	collectResultTails(operand, &tails)
	for _, tail := range tails {
		var sel *Select
		var selectedThrough bool
		switch n := tail.(type) {
		case *Select:
			sel, selectedThrough = n, n.selectedThrough
		case *FunCall:
			if fn, ok := n.Fun.(*Select); ok {
				sel, selectedThrough = fn, n.selectedThrough
			} else if fn, ok := n.Fun.(*Symbol); ok {
				if classifyGQLSymbol(fn, n.GetInferredType(), n.selectedThrough) == gqlChainField {
					return fn.Name, schemaObjectModule(n.GetInferredType())
				}
			}
		case *Symbol:
			if classifyGQLSymbol(n, n.GetInferredType(), n.selectedThrough) == gqlChainField {
				return n.Name, schemaObjectModule(n.GetInferredType())
			}
		}
		if sel != nil && classifyGQLSelect(sel, selectedThrough) == gqlChainField {
			return sel.Field.Name, schemaObjectModule(sel.GetInferredType())
		}
	}
	return "", nil
}

// leafFieldHint lists a few of the module's executing leaf fields, preferring
// `sync` when the schema has it (the conventional "run it" field).
func leafFieldHint(mod *Type) string {
	if mod == nil || mod.SourceSchema == nil {
		return ""
	}
	it := mod.SourceSchema.Types.Get(mod.Named)
	if it == nil {
		return ""
	}
	var names []string
	for _, f := range it.Fields {
		if !isScalarType(f.TypeRef, mod.SourceSchema) {
			continue
		}
		if f.Name == "sync" {
			names = append([]string{"sync"}, names...)
			continue
		}
		if f.Name == "id" {
			// `id` serializes the chain without running it on hosts like
			// Dagger; a misleading suggestion.
			continue
		}
		names = append(names, f.Name)
	}
	if len(names) > 3 {
		names = names[:3]
	}
	for i, n := range names {
		names[i] = "`." + n + "`"
	}
	return strings.Join(names, ", ")
}

// schemaObjectModule returns the first schema-imported object, interface, or
// union module mentioned by t, or nil. The generic Types() child walk is
// lossy for module and union types, so this is an explicit traversal.
func schemaObjectModule(t hm.Type) *Type {
	switch tt := t.(type) {
	case hm.NonNullType:
		return schemaObjectModule(tt.Type)
	case ListType:
		return schemaObjectModule(tt.Type)
	case GraphQLListType:
		return schemaObjectModule(tt.Type)
	case MapType:
		return schemaObjectModule(tt.Type)
	case *hm.UnionType:
		for _, opt := range tt.Options {
			if m := schemaObjectModule(opt); m != nil {
				return m
			}
		}
	case *Type:
		mod := tt
		if mod.Canonical != nil {
			mod = mod.Canonical
		}
		if mod.SourceSchema != nil &&
			(mod.Kind == ObjectKind || mod.Kind == InterfaceKind || mod.Kind == UnionKind) {
			return mod
		}
	}
	return nil
}

// classifyField consults the introspection schema for a field's underlying
// return type — the same test eval uses to decide whether calling the field
// executes the accumulated query (scalar/enum leaf) or extends it (object).
//
// An object field executes too when its handle may be null and escapes the
// chain: eval probes it for null on the spot (see GraphQLFunction.Call), so
// failures in the chain surface there, and the handle it returns has already
// run its pipeline. That is a nullable object, interface or union field, or
// any object field selected off one (chainMaybeNull), called where it is not
// itself selected from (selectedThrough), since a selection defers the probe
// to whatever runs the chain. Root mutation fields are never probed, so the
// mutation isn't sent twice. (Fields further down a mutation chain aren't
// probed at runtime either, but are classified as executing here; that only
// errs toward fewer warnings.)
func classifyField(schema *introspection.Schema, typeName, fieldName string, selectedThrough, chainMaybeNull bool) gqlFieldClass {
	f := schemaField(schema, typeName, fieldName)
	if f == nil {
		return notGQLField
	}
	if isScalarType(f.TypeRef, schema) {
		return gqlLeafField
	}
	if selectedThrough {
		return gqlChainField
	}
	if schema.MutationType != nil && schema.MutationType.Name == typeName {
		return gqlChainField
	}
	if chainMaybeNull || isNullableObjectType(f.TypeRef, schema) {
		return gqlLeafField
	}
	return gqlChainField
}

// schemaField looks up a field of a schema type, or returns nil.
func schemaField(schema *introspection.Schema, typeName, fieldName string) *introspection.Field {
	it := schema.Types.Get(typeName)
	if it == nil {
		return nil
	}
	for _, f := range it.Fields {
		if f.Name == fieldName {
			return f
		}
	}
	return nil
}

// classifyGQLSelect classifies a field selection against its receiver's
// inferred type. selectedThrough is the flag of the node that calls the
// field: the Select itself when auto-called, or the FunCall applying it.
func classifyGQLSelect(sel *Select, selectedThrough bool) gqlFieldClass {
	if sel.Receiver == nil {
		return notGQLField
	}
	mod := selectReceiverModule(sel.Receiver)
	if mod == nil {
		return notGQLField
	}
	return classifyField(mod.SourceSchema, mod.Named, sel.Field.Name, selectedThrough, chainMayBeNull(sel.Receiver))
}

// selectReceiverModule returns the schema type a selection's field resolves
// against: the receiver's schema object module, or the Query root type when
// the receiver is a GraphQL import itself, as in `Test.user(id: ...)`. Root
// fields read that way are the same root GraphQLFunctions bare names reach,
// so they execute (or not) exactly like their unqualified spelling.
func selectReceiverModule(recv Node) *Type {
	t := inferredTypeOf(recv)
	if mod := schemaObjectModule(t); mod != nil {
		return mod
	}
	return importQueryRoot(t)
}

// importQueryRoot returns the Query type of a GraphQL import module, or nil
// when t is not one. TypeScopeFromSchema builds the module as an overlay
// whose lexical layer is the Query type, which is how `Test.field` resolves
// root fields. (`Test.Mutation` is an ordinary value of the Mutation type,
// so qualified mutations go through schemaObjectModule like bare ones.)
func importQueryRoot(t hm.Type) *Type {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	scope, ok := t.(*OverlayTypeScope)
	if !ok {
		return nil
	}
	lexical, ok := scope.lexical.(*OverlayTypeScope)
	if !ok {
		return nil
	}
	query, ok := lexical.primary.(*Type)
	if !ok || query.SourceSchema == nil || query.Kind != ObjectKind ||
		query.Named != query.SourceSchema.QueryType.Name {
		return nil
	}
	return query
}

// classifyGQLSymbol classifies a bare name that may resolve to a root Query
// field spliced into the lexical scope. t is the type of the field's result:
// the symbol's own inferred type when it is auto-called, or the type of the
// FunCall applying it (the symbol itself then has a function type). Only
// object-returning root fields are recognizable this way — their result is
// a schema module whose schema's Query type carries the field. (A local
// binding that happens to shadow a root field with the same name and type
// classifies the same way; accepted imprecision.)
func classifyGQLSymbol(sym *Symbol, t hm.Type, selectedThrough bool) gqlFieldClass {
	mod := schemaObjectModule(t)
	if mod == nil || mod.SourceSchema.QueryType.Name == "" {
		return notGQLField
	}
	return classifyField(mod.SourceSchema, mod.SourceSchema.QueryType.Name, sym.Name, selectedThrough, false)
}

// chainMayBeNull reports whether a receiver chain runs through a nullable
// object field. Receivers are always selected through, so such a field is
// left unprobed and anything selected off it may be null at runtime.
func chainMayBeNull(node Node) bool {
	switch n := node.(type) {
	case *Grouped:
		return chainMayBeNull(n.Expr)
	case *Select:
		return selectMayBeNull(n)
	case *FunCall:
		switch fn := n.Fun.(type) {
		case *Select:
			return selectMayBeNull(fn)
		case *Symbol:
			return rootFieldMayBeNull(fn.Name, n.GetInferredType())
		}
	case *Symbol:
		return rootFieldMayBeNull(n.Name, n.GetInferredType())
	}
	return false
}

func selectMayBeNull(sel *Select) bool {
	if sel.Receiver == nil {
		return false
	}
	mod := selectReceiverModule(sel.Receiver)
	if mod == nil {
		return false
	}
	if f := schemaField(mod.SourceSchema, mod.Named, sel.Field.Name); f != nil &&
		isNullableObjectType(f.TypeRef, mod.SourceSchema) {
		return true
	}
	return chainMayBeNull(sel.Receiver)
}

// rootFieldMayBeNull reports whether name, called with result type t, is a
// nullable object-returning root Query field.
func rootFieldMayBeNull(name string, t hm.Type) bool {
	mod := schemaObjectModule(t)
	if mod == nil || mod.SourceSchema.QueryType.Name == "" {
		return false
	}
	f := schemaField(mod.SourceSchema, mod.SourceSchema.QueryType.Name, name)
	return f != nil && isNullableObjectType(f.TypeRef, mod.SourceSchema)
}

// inferredTypeOf returns the node's cached inferred type, or nil when the
// node kind doesn't record one.
func inferredTypeOf(node Node) hm.Type {
	if h, ok := node.(interface{ GetInferredType() hm.Type }); ok {
		return h.GetInferredType()
	}
	return nil
}

// moduleOf unwraps non-null to the named module type, schema-imported or not.
func moduleOf(t hm.Type) *Type {
	switch tt := t.(type) {
	case hm.NonNullType:
		return moduleOf(tt.Type)
	case *Type:
		return tt
	}
	return nil
}

// rescueOperandCanFail reports whether evaluating the operand can raise an
// error catchable by the enclosing rescue. Conservative in the permissive
// direction: any node kind not explicitly known safe counts as fallible, so
// the dead-rescue warning only fires on operands that flagrantly cannot fail
// — literals, plain reads, and GraphQL chain-building.
func rescueOperandCanFail(env hm.Env, node Node) bool {
	switch n := node.(type) {
	case *Null, *Boolean, *Int, *Float, *String, *SelfKeyword:
		return false
	case *Template:
		for _, part := range n.Parts {
			if part.Expr != nil && rescueOperandCanFail(env, part.Expr) {
				return true
			}
		}
		return false
	case *Grouped:
		return rescueOperandCanFail(env, n.Expr)
	case *BlockArg, *FunctionRef:
		// Constructs a closure/function value; the body does not run here.
		return false
	case *List:
		for _, el := range n.Elements {
			if rescueOperandCanFail(env, el) {
				return true
			}
		}
		return false
	case *MapLiteral:
		for _, entry := range n.Entries {
			if rescueOperandCanFail(env, entry.Key) || rescueOperandCanFail(env, entry.Value) {
				return true
			}
		}
		return false
	case *Block:
		for _, form := range n.Forms {
			if rescueOperandCanFail(env, form) {
				return true
			}
		}
		return false
	case *FieldDecl:
		if n.Value != nil {
			return rescueOperandCanFail(env, n.Value)
		}
		return false
	case *Conditional:
		if rescueOperandCanFail(env, n.Condition) {
			return true
		}
		if n.Then != nil && rescueOperandCanFail(env, n.Then) {
			return true
		}
		if n.Else != nil && rescueOperandCanFail(env, n.Else) {
			return true
		}
		return false
	case *Return:
		// return/break/continue unwind as control flow, which rescue
		// deliberately does not catch; only their value expression matters.
		if n.Value != nil {
			return rescueOperandCanFail(env, n.Value)
		}
		return false
	case *Break:
		if n.Value != nil {
			return rescueOperandCanFail(env, n.Value)
		}
		return false
	case *Continue:
		return false
	case *Symbol:
		switch classifyGQLSymbol(n, n.GetInferredType(), n.selectedThrough) {
		case gqlChainField:
			return false
		case gqlLeafField:
			return true
		}
		if scheme, found := env.SchemeOf(n.Name); found {
			if t, mono := scheme.Type(); mono {
				if _, isFn := t.(*hm.FunctionType); !isFn {
					// A plain value read. (A forced-on-read initializer of a
					// forward-referenced binding can technically raise here;
					// accepted imprecision — see package comment.)
					return false
				}
			}
		}
		// Unknown binding (e.g. declared inside a nested operand block) or
		// an auto-callable function: assume it can fail.
		return true
	case *Select:
		switch classifyGQLSelect(n, n.selectedThrough) {
		case gqlChainField:
			return rescueOperandCanFail(env, n.Receiver)
		case gqlLeafField:
			return true
		}
		// Non-schema receiver: a stored (non-function) field read cannot
		// fail; computed fields and methods run code.
		if n.Receiver == nil {
			return true
		}
		if recvMod := moduleOf(inferredTypeOf(n.Receiver)); recvMod != nil {
			if scheme, found := recvMod.SchemeOf(n.Field.Name); found {
				if t, mono := scheme.Type(); mono {
					if _, isFn := t.(*hm.FunctionType); !isFn {
						return rescueOperandCanFail(env, n.Receiver)
					}
				}
			}
		}
		return true
	case *FunCall:
		switch fn := n.Fun.(type) {
		case *Select:
			if classifyGQLSelect(fn, n.selectedThrough) == gqlChainField {
				// Building the query cannot fail; the receiver and argument
				// values still evaluate here. Object-handle arguments are
				// serialized only when the parent chain executes, so they
				// don't make the call itself an execution point.
				if fn.Receiver != nil && rescueOperandCanFail(env, fn.Receiver) {
					return true
				}
				return callArgsCanFail(env, n)
			}
		case *Symbol:
			if classifyGQLSymbol(fn, n.GetInferredType(), n.selectedThrough) == gqlChainField {
				return callArgsCanFail(env, n)
			}
		}
		return true
	default:
		return true
	}
}

func callArgsCanFail(env hm.Env, call *FunCall) bool {
	for _, arg := range call.Args {
		if rescueOperandCanFail(env, arg.Value) {
			return true
		}
	}
	// A block argument's body runs under the callee's control; for a
	// chain-building GraphQL field there is none, but stay conservative.
	return call.BlockArg != nil
}

// collectResultTails appends the expressions whose value becomes the
// operand's result, looking through grouping, blocks, and branching.
func collectResultTails(node Node, tails *[]Node) {
	switch n := node.(type) {
	case *Grouped:
		collectResultTails(n.Expr, tails)
	case *Block:
		if len(n.Forms) > 0 {
			collectResultTails(n.Forms[len(n.Forms)-1], tails)
		}
	case *Conditional:
		if n.Then != nil {
			collectResultTails(n.Then, tails)
		}
		if n.Else != nil {
			collectResultTails(n.Else, tails)
		}
	case *Case:
		for _, clause := range n.Clauses {
			collectResultTails(clause.Expr, tails)
		}
	case *RescueExpr:
		collectResultTails(n.Operand, tails)
		if n.Fallback != nil {
			collectResultTails(n.Fallback, tails)
		}
		for _, clause := range n.Clauses {
			collectResultTails(clause.Expr, tails)
		}
	case *Default:
		collectResultTails(n.Left, tails)
		collectResultTails(n.Right, tails)
	case *TypeHint:
		collectResultTails(n.Expr, tails)
	case *Coerce:
		collectResultTails(n.Expr, tails)
	case *Raise, *Return, *Break, *Continue:
		// Diverges; contributes no result value.
	default:
		*tails = append(*tails, node)
	}
}

// tailLeavesLazyHandle reports the schema module when the tail expression's
// value is a still-lazy GraphQL handle — its type mentions a schema object
// and the tail itself is not an execution point.
func tailLeavesLazyHandle(env hm.Env, tail Node) *Type {
	// Look through inference-injected coercion wrappers and grouping: they
	// don't carry their own inferred type and never execute anything on
	// their own.
	for {
		if c, ok := tail.(*Coerce); ok {
			tail = c.Expr
			continue
		}
		if g, ok := tail.(*Grouped); ok {
			tail = g.Expr
			continue
		}
		break
	}
	mod := schemaObjectModule(inferredTypeOf(tail))
	if mod == nil {
		return nil
	}
	switch n := tail.(type) {
	case *ObjectSelection:
		// `.{{ }}` executes where written.
		return nil
	case *Select:
		if classifyGQLSelect(n, n.selectedThrough) == gqlLeafField {
			// Executes here even when the Dang-visible type is an object
			// (@expectedType-mapped leaves like `sync`, or a probed handle).
			return nil
		}
	case *FunCall:
		switch fn := n.Fun.(type) {
		case *Select:
			if classifyGQLSelect(fn, n.selectedThrough) == gqlLeafField {
				return nil
			}
		case *Symbol:
			if classifyGQLSymbol(fn, n.GetInferredType(), n.selectedThrough) == gqlLeafField {
				return nil
			}
		}
	case *Symbol:
		if classifyGQLSymbol(n, n.GetInferredType(), n.selectedThrough) == gqlLeafField {
			return nil
		}
	}
	return mod
}

// checkLazyEscapes warns when the operand assigns a lazy handle to a binding
// declared outside the rescue: the handle's failures will surface at its
// eventual use site, not in this handler.
func checkLazyEscapes(ctx context.Context, env hm.Env, operand Node) {
	declared := map[string]bool{}
	operand.Walk(func(n Node) bool {
		switch n.(type) {
		case *FunDecl, *NewConstructorDecl, *ObjectDecl:
			return false
		}
		for _, name := range n.DeclaredSymbols() {
			declared[name] = true
		}
		return true
	})
	operand.Walk(func(n Node) bool {
		switch r := n.(type) {
		case *FunDecl, *NewConstructorDecl, *ObjectDecl:
			return false
		case *Reassignment:
			sym, ok := r.Target.(*Symbol)
			if !ok || declared[sym.Name] {
				return true
			}
			if mod := tailLeavesLazyHandle(env, r.Value); mod != nil {
				EmitInferWarning(ctx, r, fmt.Sprintf(
					"a lazy %s is assigned to `%s`, declared outside this rescue: its failures will surface where it is used, not here",
					mod.Named, sym.Name,
				))
			}
		}
		return true
	})
}
