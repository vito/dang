package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/kr/pretty"

	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
	"github.com/vito/dang/v2/pkg/querybuilder"
)

// Context key for inference-time state
type contextKey string

// ContextWithBlock attaches val as the block argument for the next
// Callable invocation that consumes ctx. The wrapped value must
// implement Callable so the body of a function declaring &body can
// invoke it.
//
// Embedders (e.g. Booklit) use this when programmatically calling a
// Dang function that declares a block parameter — the surface syntax
// `Foo(...) { ... }` sets the same state during evaluation.
func ContextWithBlock(ctx context.Context, val Value) context.Context {
	f := deriveFrame(ctx)
	f.blockArg = val
	return withFrame(ctx, f)
}

// currentBlockArg returns the block argument attached to ctx (see
// ContextWithBlock), or nil if there is none.
func currentBlockArg(ctx context.Context) Value {
	return currentFrame(ctx).blockArg
}

// Value represents a runtime value in the Dang language
type Value interface {
	Type() hm.Type
	String() string
}

// Evaluator defines the interface for evaluating AST nodes
type Evaluator interface {
	Eval(ctx context.Context, scope ValueScope) (Value, error)
}

// Callable defines the interface for all function-like values that can be called
type Callable interface {
	Value
	Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error)
	ParameterNames() []string
	IsAutoCallable() bool
}

// ValueScope represents the evaluation environment
type ValueScope interface {
	Value
	// Lookup resolves a binding, forcing pending initializers on first read.
	Lookup(ctx context.Context, name string) (Value, bool, error)
	// Has reports whether name is bound (as a real value or a pending initializer).
	Has(name string) bool
	LookupLocal(name string) (Value, bool)
	Bindings(Visibility) []Keyed[Value]
	// Bind installs (or replaces) a binding in this scope.
	Bind(name string, value Value, visibility Visibility)
	// BindLazy installs a deferred initializer for name. The initializer
	// runs on first Lookup and the result replaces the pending entry.
	BindLazy(name string, init func(ctx context.Context) (Value, error), visibility Visibility)
	Update(name string, value Value)
	Visibility(name string) Visibility
	// Derive returns a fresh child scope. Local bindings shadow this scope.
	// If sealed, Update stays in the child rather than walking into parents.
	Derive(sealed bool) ValueScope
	// Dynamic scope support for 'self'.
	// Derive shares the DynamicScope cell so that closures (e.g. block
	// args passed to .each) see mutations to self from prior iterations.
	// Code that establishes a NEW self (e.g. BoundMethod.Call) must call
	// EnterSelf to create a fresh, unshared cell.
	Self() (Value, bool)
	MutateSelf(value Value)
	EnterSelf(value Value)
}

// InputObjectConstructor creates an Object from named arguments,
// used for GraphQL input types like UserSort(field: ..., direction: ...).
type InputObjectConstructor struct {
	TypeName  string
	TypeScope *Type
	FnType    *hm.FunctionType
}

func (c InputObjectConstructor) Type() hm.Type        { return c.FnType }
func (c InputObjectConstructor) String() string       { return fmt.Sprintf("input:%s", c.TypeName) }
func (c InputObjectConstructor) IsAutoCallable() bool { return false }

func (c InputObjectConstructor) ParameterNames() []string {
	rec := c.FnType.Arg().(*RecordType)
	names := make([]string, len(rec.Fields))
	for i, f := range rec.Fields {
		names[i] = f.Key
	}
	return names
}

func (c InputObjectConstructor) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	instance := NewObject(c.TypeScope)
	for name, val := range args {
		instance.Bind(name, val, PublicVisibility)
	}
	return instance, nil
}

// ScalarConstructor constructs a scalar value from a String by running the
// scalar's new() hook and wrapping the resulting canonical string. It is the
// value bound under the scalar's name when its declaration includes new()
// (Dang scalars) or when a builtin scalar registers a Go-native hook (Regexp),
// mirroring how a type's name doubles as its constructor.
type ScalarConstructor struct {
	ScalarType *Type
	FnType     *hm.FunctionType
	ArgName    string
}

func (c ScalarConstructor) Type() hm.Type        { return c.FnType }
func (c ScalarConstructor) String() string       { return fmt.Sprintf("scalar:%s", c.ScalarType.Named) }
func (c ScalarConstructor) IsAutoCallable() bool { return false }

func (c ScalarConstructor) ParameterNames() []string {
	return []string{c.ArgName}
}

func (c ScalarConstructor) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	raw, ok := args[c.ArgName].(StringValue)
	if !ok {
		return nil, fmt.Errorf("%s: argument %q must be String!, got %T", c.ScalarType.Named, c.ArgName, args[c.ArgName])
	}
	// The hook lives on the type (set before this constructor was built), so
	// this handles both Dang new() and Go-native hooks uniformly.
	v, hooked, err := applyScalarHook(ctx, scope, c.ScalarType, raw.Val)
	if err != nil {
		return nil, err
	}
	if !hooked {
		return ScalarValue{Val: raw.Val, ScalarType: c.ScalarType}, nil
	}
	return v, nil
}

// applyScalarHook materializes raw into scalarType through its new() hook —
// Go-native or Dang-defined — wrapping the canonical string into a
// ScalarValue. ok is false when the scalar has no hook and the caller should
// wrap raw unchanged.
func applyScalarHook(ctx context.Context, scope ValueScope, scalarType *Type, raw string) (v Value, ok bool, err error) {
	if hook := scalarType.GoScalarHook(); hook != nil {
		canonical, err := hook(raw)
		if err != nil {
			return nil, true, err
		}
		return ScalarValue{Val: canonical, ScalarType: scalarType}, true, nil
	}
	if hook, argName, ok := scalarType.ScalarHook(); ok {
		v, err := runScalarHook(ctx, scope, scalarType, hook, argName, raw)
		return v, true, err
	}
	return nil, false, nil
}

// runScalarHook runs a scalar's new() hook on raw and wraps the resulting
// canonical string into a ScalarValue. Shared by the derived constructor and
// materialization (literals, casts, decode).
func runScalarHook(ctx context.Context, scope ValueScope, scalarType *Type, hook FunctionValue, argName, raw string) (Value, error) {
	result, err := hook.Call(ctx, scope, map[string]Value{argName: StringValue{Val: raw}})
	if err != nil {
		return nil, err
	}
	str, ok := result.(StringValue)
	if !ok {
		return nil, fmt.Errorf("%s new() must produce String!, got %T", scalarType.Named, result)
	}
	return ScalarValue{Val: str.Val, ScalarType: scalarType}, nil
}

// GraphQLFunction represents a GraphQL API function that makes actual calls
type GraphQLFunction struct {
	Name       string
	TypeName   string
	Field      *introspection.Field
	FnType     *hm.FunctionType
	Client     graphql.Client
	Schema     *introspection.Schema
	TypeScope  TypeScope               // Type environment for looking up enum types
	QueryChain *querybuilder.Selection // Keep track of the query chain built so far
	IsMutation bool                    // True if this is a mutation field

	// MaybeNull is inherited from the receiver: the chain runs through a
	// nullable object field that was never probed, so the server may resolve
	// it, and thus anything selected off it, to null. See GraphQLValue.
	MaybeNull bool

	// selectedThrough is set when the call's result is immediately selected
	// from (`.field`, `.method(...)` or `.{{ }}`): whatever runs the chain
	// then reveals a null on its own, so probing for it here would be a
	// wasted round trip. See withSelectedThrough.
	selectedThrough bool
}

// withSelectedThrough marks a GraphQL field function as being called for a
// receiver that is immediately selected from (see GraphQLFunction.Call) when
// selected is set. Callers pass the flag of the node being evaluated, so it
// only reaches the one call whose result feeds the selection, never nested
// evaluation such as the receiver or argument expressions. Other values pass
// through unchanged.
func withSelectedThrough(val Value, selected bool) Value {
	if !selected {
		return val
	}
	if g, ok := val.(GraphQLFunction); ok {
		g.selectedThrough = true
		return g
	}
	return val
}

func (g GraphQLFunction) Type() hm.Type {
	return g.FnType
}

func (g GraphQLFunction) String() string {
	return fmt.Sprintf("gql:%s.%s", g.TypeName, g.Name)
}

func (g GraphQLFunction) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	// Build the GraphQL query using querybuilder
	var query *querybuilder.Selection

	if g.QueryChain != nil {
		// Use existing query chain - extract the field name from the full name
		parts := strings.Split(g.Name, ".")
		fieldName := parts[len(parts)-1] // Get the last part as the field name
		query = g.QueryChain.Select(fieldName)
	} else {
		// Build from scratch for top-level functions
		root := querybuilder.Query()
		if g.IsMutation {
			root = querybuilder.Mutation()
		}
		// Check if this is a method call (contains a dot) or a top-level function
		if strings.Contains(g.Name, ".") {
			// This is a method call like "container.from" - we need to build a nested query
			parts := strings.Split(g.Name, ".")
			query = root
			for _, part := range parts {
				query = query.Select(part)
			}
		} else {
			// This is a top-level function call
			query = root.Select(g.Name)
		}
	}

	// Add arguments to the query
	for _, arg := range g.Field.Args {
		if val, ok := args[arg.Name]; ok {
			// Convert Dang value to Go value for GraphQL
			goVal, err := dangValueToGo(val)
			if err != nil {
				return nil, fmt.Errorf("converting argument %s: %w", arg.Name, err)
			}
			query = query.Arg(arg.Name, goVal)
		}
	}

	// For functions that return scalar types, execute the query immediately
	if isScalarType(g.Field.TypeRef, g.Schema) {
		// Execute the query and return the scalar value
		var result any
		query = query.Bind(&result).Client(g.Client)
		if err := query.Execute(ctx); err != nil {
			return nil, fmt.Errorf("executing GraphQL query for %s.%s: %w", g.TypeName, g.Name, err)
		}

		// On a MaybeNull chain, a null that came back without an error is the
		// unprobed nullable field's null, passed up the path to this leaf by
		// the query builder: had the leaf itself resolved null in spite of
		// being non-null, the server would have reported an error and nulled
		// out the nearest nullable parent instead. So the leaf is null too,
		// as the type checker already assumes for a selection through a
		// nullable receiver.
		if result == nil && g.MaybeNull {
			return NullValue{}, nil
		}

		return graphQLResultToValue(result, g.Field.TypeRef, g.Field.Directives.ExpectedType(), g.Schema, g.TypeScope, g.Client)
	}

	// For non-scalar types, return a GraphQLValue that can be further selected
	val := GraphQLValue{
		Name:       g.Name,
		TypeName:   getTypeName(g.Field.TypeRef),
		Field:      g.Field,
		ValType:    g.FnType.Ret(false),
		Client:     g.Client,
		Schema:     g.Schema,
		TypeScope:  g.TypeScope,  // Pass along the type environment
		QueryChain: query,        // Pass the query chain for further building
		IsMutation: g.IsMutation, // Propagate mutation flag
		MaybeNull:  g.MaybeNull || isNullableObjectType(g.Field.TypeRef, g.Schema),
	}

	// A handle that may be null has to be resolved before it escapes the
	// chain, the way scalars are: a lazy handle is never null, so `== null`,
	// `??`, `!`, null patterns and flow narrowing would all see a value, and
	// the null would only surface later as an unrelated error. That covers a
	// nullable object, interface or union field, and anything selected off
	// an unprobed one, including lists. Everything else stays lazy; this
	// mirrors the Dagger SDKs, which execute nullable object fields eagerly
	// and keep the rest lazy.
	//
	// The probe is skipped while the handle is selected through, as in
	// `a.maybeB.c.name` or `a.maybeB.{{name}}`: the leaf or projection that
	// eventually runs the chain reveals the null itself, so the probe would
	// be a wasted round trip. The handle is marked MaybeNull instead, and the
	// first call off it that escapes the chain probes once, at its end.
	//
	// Known gap: fields reached through a mutation are not probed. Every
	// query built off a mutation chain re-sends the mutation, so a probe would
	// run it twice; such fields stay lazy and never compare equal to null.
	if !val.MaybeNull || g.selectedThrough || g.IsMutation {
		return val, nil
	}
	return val.resolveNull(ctx, g.TypeName+"."+g.Name)
}

func (g GraphQLFunction) ParameterNames() []string {
	if ft, ok := g.FnType.Arg().(*RecordType); ok {
		names := make([]string, len(ft.Fields))
		for i, field := range ft.Fields {
			names[i] = field.Key
		}
		return names
	}
	return nil
}

func (g GraphQLFunction) IsAutoCallable() bool {
	return hasZeroRequiredArgs(g.Field)
}

// GraphQLValue represents a GraphQL object/field value
type GraphQLValue struct {
	Name       string
	TypeName   string
	Field      *introspection.Field
	ValType    hm.Type
	Client     graphql.Client
	Schema     *introspection.Schema
	TypeScope  TypeScope               // Type environment for looking up enum types
	QueryChain *querybuilder.Selection // Keep track of the query chain built so far
	IsMutation bool                    // True if this value came from a mutation

	// KnownID is the object's ID when this value was built by loading that ID
	// (see loadObjectFromID). Selecting `id` on such a chain would just hand
	// back the same ID, so remembering it saves a round trip whenever the value
	// flows into an ID-typed argument.
	KnownID string

	// MaybeNull marks a handle whose chain runs through a nullable object
	// field that was left unprobed because the handle was selected through
	// (see GraphQLFunction.Call). The server may resolve it to null, so a
	// null leaf off this chain is null rather than an error, and a handle
	// that escapes the chain is probed first. Values selected off this one
	// inherit it; probing clears it.
	MaybeNull bool
}

func (g GraphQLValue) Type() hm.Type {
	return g.ValType
}

func (g GraphQLValue) String() string {
	return fmt.Sprintf("gql:Value:%s.%s", g.TypeName, g.Name)
}

func (g GraphQLValue) MarshalJSON() ([]byte, error) {
	id, err := g.ID(context.TODO())
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
}

// ID resolves the value's GraphQL ID, querying for it unless it is already
// known from having loaded the value by ID in the first place.
func (g GraphQLValue) ID(ctx context.Context) (string, error) {
	if g.KnownID != "" {
		return g.KnownID, nil
	}
	var id string
	err := g.QueryChain.Select("id").Client(g.Client).Bind(&id).Execute(ctx)
	if err != nil {
		return "", err
	}
	return id, nil
}

// SelectField handles field selection on a GraphQLValue
func (g GraphQLValue) SelectField(ctx context.Context, fieldName string) (Value, error) {
	// Find the type definition for this GraphQL value
	var objectType *introspection.Type
	for _, t := range g.Schema.Types {
		if t.Name == g.TypeName {
			objectType = t
			break
		}
	}

	if objectType == nil {
		return nil, fmt.Errorf("GraphQL type %s not found in schema", g.TypeName)
	}

	// Find the requested field in the object type
	var field *introspection.Field
	for _, f := range objectType.Fields {
		if f.Name == fieldName {
			field = f
			break
		}
	}

	if field == nil {
		return nil, fmt.Errorf("field %s not found on GraphQL type %s", fieldName, g.TypeName)
	}

	// Create a function type for this method call
	args := NewRecordType("")
	for _, arg := range field.Args {
		argType, err := gqlInputToTypeNode(g.TypeScope, arg)
		if err != nil {
			continue
		}
		args.Add(arg.Name, hm.NewScheme(nil, argType))
	}

	retType, err := gqlFieldToTypeNode(g.TypeScope, field)
	if err != nil {
		return nil, fmt.Errorf("failed to convert return type: %w", err)
	}

	fnType := hm.NewFnType(args, retType)

	return GraphQLFunction{
		Name:       fmt.Sprintf("%s.%s", g.Name, fieldName),
		TypeName:   g.TypeName,
		Field:      field,
		FnType:     fnType,
		Client:     g.Client,
		Schema:     g.Schema,
		TypeScope:  g.TypeScope,  // Pass along the type environment
		QueryChain: g.QueryChain, // Pass the current query chain
		IsMutation: g.IsMutation, // Propagate mutation flag
		MaybeNull:  g.MaybeNull,  // Propagate unprobed nullable steps
	}, nil
}

// NewValueScope creates an evaluation environment with built-in functions
func NewValueScope(typeScope TypeScope) ValueScope {
	// Create an Object from the type environment
	env := NewObject(typeScope)

	// Add builtin functions
	addBuiltinFunctions(env)

	// Bind the Dang-source prelude's public declarations, builtin-style.
	loadPrelude()
	for _, kv := range preludeBindings {
		env.Bind(kv.Key, kv.Value, PublicVisibility)
	}

	return env
}

// ValueScopeFromSchema creates an evaluation environment populated with GraphQL API values
func ValueScopeFromSchema(typeScope TypeScope, client graphql.Client, schema *introspection.Schema) ValueScope {
	// Create an Object from the type environment
	env := NewObject(typeScope)

	// Populate with GraphQL functions from the schema
	populateSchemaFunctions(env, typeScope, client, schema)

	// Add builtin functions
	addBuiltinFunctions(env)

	// Match the type scope's public source prelude without replacing values
	// owned by the schema, such as an input constructor also named Path.
	loadPrelude()
	for _, binding := range preludeBindings {
		if _, exists := env.LookupLocal(binding.Key); !exists {
			env.Bind(binding.Key, binding.Value, PublicVisibility)
		}
	}

	return env
}

// BuildScopesFromImports creates type and eval environments from a set of import
// configurations. Each import's schema types and values are merged into the
// environments, with the first import taking precedence on name collisions.
func BuildScopesFromImports(name string, configs []ImportConfig) (TypeScope, ValueScope) {
	typeScope := NewPreludeTypeScope(name)
	valueAliases := make([]map[string]string, len(configs))
	for i, config := range configs {
		if config.Schema == nil {
			continue
		}
		schemaModule := TypeScopeFromSchema(config.Name, config.Schema)
		valueAliases[i] = aliasImportTypes(schemaModule, config.Schema, config.Aliases)
		installImportedTypeScope(typeScope, config.Name, schemaModule)
	}

	valueScope := NewValueScope(typeScope)
	for i, config := range configs {
		if config.Schema == nil {
			continue
		}
		schemaModule, found := typeScope.NamedType(config.Name)
		if !found {
			continue
		}
		importValueScope := ValueScopeFromSchema(schemaModule, config.Client, config.Schema)
		aliasImportValues(importValueScope, valueAliases[i])
		installImportedValueScope(valueScope, config.Name, importValueScope)
	}

	return typeScope, valueScope
}

// populateSchemaFunctions adds GraphQL functions from a schema to an environment
func populateSchemaFunctions(env *Object, typeScope TypeScope, client graphql.Client, schema *introspection.Schema) {
	for _, t := range schema.Types {
		// Add enum values as enum constants for enum types
		if t.Kind == introspection.TypeKindEnum && len(t.EnumValues) > 0 {
			// Get the enum type environment
			enumTypeScope, found := typeScope.NamedType(t.Name)
			if !found {
				continue
			}

			// Create a module for the enum type
			enumModuleVal := NewObject(enumTypeScope)

			// Set each enum value as an EnumValue constant
			enumValues := make([]Value, len(t.EnumValues))
			for i, enumVal := range t.EnumValues {
				ev := EnumValue{
					Val:      enumVal.Name,
					EnumType: enumTypeScope,
				}
				enumModuleVal.Bind(enumVal.Name, ev, PublicVisibility)
				enumValues[i] = ev
			}

			// Add the values() method that returns all enum values as a list
			enumModuleVal.Bind("values", ListValue{
				Elements: enumValues,
				ElemType: NonNull(enumTypeScope),
			}, PublicVisibility)

			// Add the enum module to the environment
			env.Bind(t.Name, enumModuleVal, PublicVisibility)
		}

		// Add scalar types as available values for custom scalars
		if t.Kind == introspection.TypeKindScalar {
			// Skip built-in scalars (String, Int, Float, Boolean, ID)
			if t.Name == "String" || t.Name == "Int" || t.Name == "Float" || t.Name == "Boolean" || t.Name == "ID" {
				continue
			}

			// Get the scalar type environment
			scalarTypeScope, found := typeScope.NamedType(t.Name)
			if !found {
				continue
			}

			// Create a module for the scalar type (just a type placeholder)
			scalarModuleVal := NewObject(scalarTypeScope)
			// A scalar whose name matches a builtin scalar (e.g. an imported JSON
			// scalar) doubles as that scalar's namespace.
			attachBuiltinMethods(scalarModuleVal, scalarTypeScope)

			// Add the scalar module to the environment
			env.Bind(t.Name, scalarModuleVal, PublicVisibility)
		}

		// Add interface types as available values
		if t.Kind == introspection.TypeKindInterface {
			// Get the interface type environment
			interfaceTypeScope, found := typeScope.NamedType(t.Name)
			if !found {
				continue
			}

			// Create a module for the interface type (just a type placeholder)
			interfaceModuleVal := NewObject(interfaceTypeScope)

			// Add the interface module to the environment
			env.Bind(t.Name, interfaceModuleVal, PublicVisibility)
		}

		// Add union types as available values
		if t.Kind == introspection.TypeKindUnion {
			unionTypeScope, found := typeScope.NamedType(t.Name)
			if !found {
				continue
			}

			unionModuleVal := NewObject(unionTypeScope)
			env.Bind(t.Name, unionModuleVal, PublicVisibility)
		}

		// Add input object constructors: UserSort(field: ..., direction: ...)
		if t.Kind == introspection.TypeKindInputObject {
			inputTypeScope, found := typeScope.NamedType(t.Name)
			if !found {
				continue
			}

			args := NewRecordType("")
			for _, f := range t.InputFields {
				argType, err := gqlInputToTypeNode(typeScope, f)
				if err != nil {
					continue
				}
				args.Add(f.Name, hm.NewScheme(nil, argType))
			}
			fnType := hm.NewFnType(args, NonNull(inputTypeScope))

			constructor := InputObjectConstructor{
				TypeName:  t.Name,
				TypeScope: inputTypeScope.(*Type),
				FnType:    fnType,
			}
			env.Bind(t.Name, constructor, PublicVisibility)
		}

		for _, f := range t.Fields {
			ret, err := gqlFieldToTypeNode(typeScope, f)
			if err != nil {
				continue
			}

			// This is a function - create a GraphQLFunction value
			args := NewRecordType("")
			for _, arg := range f.Args {
				argType, err := gqlInputToTypeNode(typeScope, arg)
				if err != nil {
					continue
				}
				args.Add(arg.Name, hm.NewScheme(nil, argType))
			}
			fnType := hm.NewFnType(args, ret)

			gqlFunc := GraphQLFunction{
				Name:       f.Name,
				TypeName:   t.Name,
				Field:      f,
				FnType:     fnType,
				Client:     client,
				Schema:     schema,
				TypeScope:  typeScope, // Pass the type environment
				QueryChain: nil,       // Top-level functions start with no query chain
			}

			// Add to environment if it's from the Query type
			if t.Name == schema.QueryType.Name {
				env.Bind(f.Name, gqlFunc, PublicVisibility)
			}
		}

		// Collect mutation fields into a Mutation module
		if schema.MutationType != nil && t.Name == schema.MutationType.Name {
			mutTypeScope, found := typeScope.NamedType(t.Name)
			if !found {
				continue
			}
			mutModule := NewObject(mutTypeScope)
			for _, f := range t.Fields {
				ret, err := gqlFieldToTypeNode(typeScope, f)
				if err != nil {
					continue
				}
				args := NewRecordType("")
				for _, arg := range f.Args {
					argType, err := gqlInputToTypeNode(typeScope, arg)
					if err != nil {
						continue
					}
					args.Add(arg.Name, hm.NewScheme(nil, argType))
				}
				fnType := hm.NewFnType(args, ret)
				mutFunc := GraphQLFunction{
					Name:       f.Name,
					TypeName:   t.Name,
					Field:      f,
					FnType:     fnType,
					Client:     client,
					Schema:     schema,
					TypeScope:  typeScope,
					QueryChain: nil,
					IsMutation: true,
				}
				mutModule.Bind(f.Name, mutFunc, PublicVisibility)
			}
			env.Bind("Mutation", mutModule, PublicVisibility)
		}
	}
}

// addBuiltinFunctions adds builtin functions like print to the evaluation environment
// allParamsDefaulted returns true if every parameter in the def has a default value.
func allParamsDefaulted(def BuiltinDef) bool {
	if len(def.ParamTypes) == 0 {
		return false // no params means zero-arg, handled by the len==0 check
	}
	for _, p := range def.ParamTypes {
		if p.DefaultValue == nil {
			return false
		}
	}
	return true
}

func addBuiltinFunctions(scope ValueScope) {
	// Register all builtin functions
	ForEachFunction(func(def BuiltinDef) {
		fnType := createFunctionTypeFromDef(def)
		builtinFn := BuiltinFunction{
			Name:         def.Name,
			FnType:       fnType,
			AllDefaulted: allParamsDefaulted(def),
			Deprecated:   def.Deprecated,
			CallFn: func(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
				// Apply defaults for missing arguments
				argsWithDefaults := applyDefaults(args, def)

				// Extract block from context if present
				var blockArg *FunctionValue
				if fnVal, ok := currentBlockArg(ctx).(FunctionValue); ok {
					blockArg = &fnVal
				}

				return def.Impl(ctx, nil, Args{Values: argsWithDefaults, Block: blockArg})
			},
		}
		scope.Bind(def.Name, builtinFn, PublicVisibility)
	})

	// Register all builtin methods with naming convention
	for _, receiverType := range MethodReceivers() {
		ForEachMethod(receiverType, func(def BuiltinDef) {
			fnType := createFunctionTypeFromDef(def)
			builtinFn := BuiltinFunction{
				Name:         def.Name,
				FnType:       fnType,
				AllDefaulted: allParamsDefaulted(def),
				Deprecated:   def.Deprecated,
				CallFn: func(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
					selfVal, _ := scope.Self()
					// Apply defaults for missing arguments
					argsWithDefaults := applyDefaults(args, def)

					// Extract block from context if present
					var blockArg *FunctionValue
					if fnVal, ok := currentBlockArg(ctx).(FunctionValue); ok {
						blockArg = &fnVal
					}

					return def.Impl(ctx, selfVal, Args{Values: argsWithDefaults, Block: blockArg})
				},
			}
			methodKey := GetMethodKey(receiverType, def.Name)
			scope.Bind(methodKey, builtinFn, PrivateVisibility)
		})
	}

	// Register static methods on their host modules
	for _, hostModule := range StaticModules() {
		modValue := NewObject(hostModule)
		ForEachStaticMethod(hostModule, func(def BuiltinDef) {
			fnType := createFunctionTypeFromDef(def)
			builtinFn := BuiltinFunction{
				Name:         def.Name,
				FnType:       fnType,
				AllDefaulted: allParamsDefaulted(def),
				Deprecated:   def.Deprecated,
				CallFn: func(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
					argsWithDefaults := applyDefaults(args, def)
					return def.Impl(ctx, nil, Args{Values: argsWithDefaults})
				},
			}
			modValue.Bind(def.Name, builtinFn, PublicVisibility)
		})

		// Populate nested enum types with their values
		for name, subScope := range hostModule.NamedTypes() {
			subType, ok := subScope.(*Type)
			if !ok || subType.Kind != EnumKind {
				continue
			}
			enumModValue := NewObject(subType)
			var enumValues []Value
			for varName, scheme := range subType.Bindings(PublicVisibility) {
				// Only include actual enum members (whose type is the
				// enum itself), not accessors like values().
				t, _ := scheme.Type()
				if nn, ok := t.(hm.NonNullType); !ok || nn.Type != hm.Type(subType) {
					continue
				}
				ev := EnumValue{Val: varName, EnumType: subType}
				enumModValue.Bind(varName, ev, PublicVisibility)
				enumValues = append(enumValues, ev)
			}
			enumModValue.Bind("values", ListValue{
				Elements: enumValues,
				ElemType: NonNull(subType),
			}, PublicVisibility)
			modValue.Bind(name, enumModValue, PublicVisibility)
		}

		scope.Bind(hostModule.Named, modValue, PublicVisibility)
	}

	// The Regexp(...) constructor: a builtin scalar with a Go-native hook,
	// bound like a Dang scalar's derived constructor. Its type-level scheme
	// lives in the Prelude (see env.go init).
	scope.Bind("Regexp", ScalarConstructor{
		ScalarType: RegexpType,
		FnType:     regexpConstructorType(),
		ArgName:    regexpConstructorArg,
	}, PublicVisibility)
}

// applyDefaults fills in default values for missing arguments
func applyDefaults(args map[string]Value, def BuiltinDef) map[string]Value {
	result := make(map[string]Value)
	maps.Copy(result, args)
	for _, param := range def.ParamTypes {
		if _, ok := result[param.Name]; !ok && param.DefaultValue != nil {
			result[param.Name] = param.DefaultValue
		}
	}
	return result
}

// isNullableObjectType reports whether typeRef is a nullable, non-list object,
// interface or union type: a field returning one may resolve to null, which a
// lazy handle can't represent.
func isNullableObjectType(typeRef *introspection.TypeRef, schema *introspection.Schema) bool {
	if typeRef == nil || schema == nil {
		return false
	}
	if typeRef.Kind == introspection.TypeKindNonNull || typeRef.Kind == introspection.TypeKindList {
		return false
	}
	t := schema.Types.Get(typeRef.Name)
	if t == nil {
		return false
	}
	switch t.Kind {
	case introspection.TypeKindObject, introspection.TypeKindInterface, introspection.TypeKindUnion:
		return true
	}
	return false
}

func isObjectLikeType(typeRef *introspection.TypeRef, schema *introspection.Schema) bool {
	currentType := typeRef
	if currentType.Kind == "NON_NULL" {
		currentType = currentType.OfType
	}
	if currentType.Kind == introspection.TypeKindObject || currentType.Kind == introspection.TypeKindInterface {
		return true
	}
	if schema == nil {
		return false
	}
	t := schema.Types.Get(currentType.Name)
	return t != nil && (t.Kind == introspection.TypeKindObject || t.Kind == introspection.TypeKindInterface)
}

// Helper function to determine if a GraphQL type is scalar
func isScalarType(typeRef *introspection.TypeRef, schema *introspection.Schema) bool {
	// Unwrap NonNull and List wrappers
	currentType := typeRef
	for currentType.Kind == "NON_NULL" || currentType.Kind == "LIST" {
		currentType = currentType.OfType
	}

	// Check if it's a built-in scalar
	switch currentType.Name {
	case "String", "Int", "Float", "Boolean", "ID":
		return true
	}

	// Check if it's a custom scalar or enum in the schema
	for _, t := range schema.Types {
		if t.Name == currentType.Name && (t.Kind == "SCALAR" || t.Kind == introspection.TypeKindEnum) {
			return true
		}
	}

	return false
}

// Helper function to get the type name from a TypeRef
func getTypeName(typeRef *introspection.TypeRef) string {
	// Unwrap NonNull and List wrappers to get the base type name
	currentType := typeRef
	for currentType.Kind == "NON_NULL" || currentType.Kind == "LIST" {
		currentType = currentType.OfType
	}
	return currentType.Name
}

// loadObjectFromID turns an ID into a lazy handle for the object or interface
// it identifies, so that a field typed as that object by @expectedType can be
// chained onto like any other object-returning field.
//
// Returns nil (and no error) if the schema has no root field to load the type
// with (see idLoaderFor), in which case the caller keeps the raw ID string.
func loadObjectFromID(id, typeName string, schema *introspection.Schema, typeScope TypeScope, client graphql.Client) (Value, error) {
	return loadObjectFromTypedID(id, "", typeName, schema, typeScope, client)
}

// loadObjectFromTypedID is loadObjectFromID for an ID read from an `id` field
// of type idType: only a loader whose `id` argument takes that type is used.
// An empty idType accepts any loader.
//
// The handle is typed as typeName itself, not whatever interface the loader
// returns, and its chain starts at the loader: with the Relay-style
// `node(id:)`, which returns an interface, the concrete type is reached with
// an inline fragment, as in node(id: "...") { ... on Container { ... } }.
func loadObjectFromTypedID(id, idType, typeName string, schema *introspection.Schema, typeScope TypeScope, client graphql.Client) (Value, error) {
	if schema == nil || typeScope == nil || client == nil {
		return nil, nil
	}
	loader, fragment := idLoaderFor(schema, typeName, idType)
	if loader == nil {
		return nil, nil
	}
	schemaType := schema.Types.Get(typeName)
	if schemaType == nil {
		return nil, fmt.Errorf("loading %s from ID: type not found in schema", typeName)
	}
	loadedType, found := typeScope.NamedType(typeName)
	if !found {
		return nil, fmt.Errorf("loading %s from ID: type not found", typeName)
	}
	chain := querybuilder.Query().Client(client).Select(loader.Name).Arg("id", id)
	if fragment {
		chain = chain.InlineFragment(typeName)
	}
	return GraphQLValue{
		Name:     loader.Name,
		TypeName: typeName,
		// Selections on this value resolve their types through Field, so it has
		// to describe the loaded type rather than `node`'s interface return
		// type, which is all the inline fragment leaves visible.
		Field:      concreteTypeField(loader.Name, schemaType),
		ValType:    hm.NonNullType{Type: loadedType},
		Client:     client,
		Schema:     schema,
		TypeScope:  typeScope,
		QueryChain: chain,
		KnownID:    id,
	}, nil
}

// concreteTypeField describes a field returning a non-null t, for handles
// whose type has been narrowed beyond the field that produced them.
func concreteTypeField(name string, t *introspection.Type) *introspection.Field {
	return &introspection.Field{
		Name: name,
		TypeRef: &introspection.TypeRef{
			Kind: introspection.TypeKindNonNull,
			OfType: &introspection.TypeRef{
				Kind: t.Kind,
				Name: t.Name,
			},
		},
	}
}

// idLoaderFor finds the root field that loads an object of typeName from its
// ID, and whether the loaded value needs an inline fragment to reach the type.
//
// The Relay-style `node(id:)` is preferred when it can return typeName; it is
// what current Dagger engines expose. Otherwise a per-type
// `load<T>FromID(id:)` field returning the type is used, the convention of
// older Dagger schemas (still served to older clients). When idType is
// non-empty, the loader's `id` argument must take that named type, so that an
// ID read from the object's `id` field is accepted as is.
func idLoaderFor(schema *introspection.Schema, typeName, idType string) (*introspection.Field, bool) {
	if node := nodeLoaderField(schema); node != nil &&
		loaderTakesIDType(node, idType) && nodeCanReturn(schema, node, typeName) {
		return node, true
	}
	queryType := schema.Types.Get(schema.QueryType.Name)
	if queryType == nil {
		return nil, false
	}
	name := "load" + typeName + "FromID"
	for _, field := range queryType.Fields {
		if field.Name != name || len(field.Args) != 1 || field.Args[0].Name != "id" {
			continue
		}
		if getTypeName(field.TypeRef) != typeName || !loaderTakesIDType(field, idType) {
			continue
		}
		return field, false
	}
	return nil, false
}

// loaderTakesIDType reports whether loader's `id` argument takes idType, or
// whether idType is empty (anything goes).
func loaderTakesIDType(loader *introspection.Field, idType string) bool {
	return idType == "" || getTypeName(loader.Args[0].TypeRef) == idType
}

// nodeCanReturn reports whether node, a `node(id:)` loader, can load
// typeName: it returns that type, or an interface or union the type belongs
// to. Abstract types are accepted as is, since an inline fragment on one is
// valid wherever it overlaps node's return type.
func nodeCanReturn(schema *introspection.Schema, node *introspection.Field, typeName string) bool {
	ret := getTypeName(node.TypeRef)
	if ret == typeName {
		return true
	}
	t := schema.Types.Get(typeName)
	if t == nil {
		return false
	}
	if t.Kind == introspection.TypeKindInterface || t.Kind == introspection.TypeKindUnion {
		return true
	}
	for _, iface := range t.Interfaces {
		if iface.Name == ret {
			return true
		}
	}
	if rt := schema.Types.Get(ret); rt != nil {
		for _, pt := range rt.PossibleTypes {
			if pt.Name == typeName {
				return true
			}
		}
	}
	return false
}

// nodeLoaderField returns the schema's `node(id:)` root field, the Relay-style
// convention for loading any object by its ID, or nil if the schema has none.
func nodeLoaderField(schema *introspection.Schema) *introspection.Field {
	queryType := schema.Types.Get(schema.QueryType.Name)
	if queryType == nil {
		return nil
	}
	for _, field := range queryType.Fields {
		if field.Name != "node" || len(field.Args) != 1 || field.Args[0].Name != "id" {
			continue
		}
		if !isObjectLikeType(field.TypeRef, schema) {
			continue
		}
		return field
	}
	return nil
}

// loadableIDField returns t's `id` field when it identifies the object for an
// ID loader: a non-null `ID!`, or a non-null custom scalar such as Dagger's
// legacy per-type `ContainerID!` (which goes with `load<T>FromID`), that takes
// no required arguments. Returns nil otherwise.
//
// Built-in scalars other than `ID` don't count, `String!` included: loading
// through `node(id:)` relies on the Relay convention that IDs are unique
// across every type, which only a schema declaring its IDs as `ID` promises.
// A `String!` id might be a per-type key that would load the wrong object.
func loadableIDField(t *introspection.Type) *introspection.Field {
	if t == nil {
		return nil
	}
	for _, f := range t.Fields {
		if f.Name != "id" {
			continue
		}
		if f.TypeRef == nil || f.TypeRef.Kind != introspection.TypeKindNonNull ||
			f.TypeRef.OfType == nil || f.TypeRef.OfType.Kind != introspection.TypeKindScalar {
			return nil
		}
		switch f.TypeRef.OfType.Name {
		case "String", "Int", "Float", "Boolean":
			return nil
		}
		if !hasZeroRequiredArgs(f) {
			return nil
		}
		return f
	}
	return nil
}

// possibleObjectTypes lists the object types an interface or union can
// resolve to, falling back to scanning for implementers when the schema
// carries no possibleTypes (as with some SDL-built schemas).
func possibleObjectTypes(schema *introspection.Schema, t *introspection.Type) []*introspection.Type {
	var types []*introspection.Type
	if len(t.PossibleTypes) > 0 {
		for _, pt := range t.PossibleTypes {
			if full := schema.Types.Get(pt.Name); full != nil {
				types = append(types, full)
			}
		}
		return types
	}
	if t.Kind != introspection.TypeKindInterface {
		return nil
	}
	for _, candidate := range schema.Types {
		if candidate.Kind != introspection.TypeKindObject {
			continue
		}
		for _, iface := range candidate.Interfaces {
			if iface.Name == t.Name {
				types = append(types, candidate)
				break
			}
		}
	}
	return types
}

// nullProbeSelection is the selection set that checks whether an object of
// type typeName is null: `__typename`, plus its `id` when it has a loadable
// one (see loadableIDField), so that a non-null result can be loaded by ID.
// For an interface without an `id` of its own, or a union, the IDs of the
// possible types that have one are fetched with inline fragments. idType is
// the named type of the IDs fetched, or "" when none are.
func nullProbeSelection(schema *introspection.Schema, typeName string) (fields []string, idType string) {
	t := schema.Types.Get(typeName)
	if t == nil {
		return []string{"__typename"}, ""
	}
	if f := loadableIDField(t); f != nil {
		return []string{"id", "__typename"}, getTypeName(f.TypeRef)
	}
	fields = []string{"__typename"}
	if t.Kind != introspection.TypeKindInterface && t.Kind != introspection.TypeKindUnion {
		return fields, ""
	}
	for _, pt := range possibleObjectTypes(schema, t) {
		f := loadableIDField(pt)
		if f == nil {
			continue
		}
		// Every `id` shares one response key, so they must share a type to
		// be selectable side by side.
		ptIDType := getTypeName(f.TypeRef)
		if idType == "" {
			idType = ptIDType
		} else if ptIDType != idType {
			continue
		}
		fields = append(fields, "... on "+pt.Name+" { id }")
	}
	return fields, idType
}

// resolveNull probes a handle that may be null (see GraphQLFunction.Call)
// with a single query against the end of its chain, returning null or a
// handle known to be non-null. what names the field for error messages.
//
// An object, interface or union handle is probed with resolveObject, so it
// comes back loaded by ID and narrowed to its concrete type.
func (g GraphQLValue) resolveNull(ctx context.Context, what string) (Value, error) {
	if g.Field != nil && isListTypeRef(g.Field.TypeRef) {
		// Lists are only probed for a null they inherit from an unprobed
		// nullable step; each element's __typename is the cheapest valid
		// selection.
		var result any
		if err := g.QueryChain.Select("__typename").Bind(&result).Client(g.Client).Execute(ctx); err != nil {
			return nil, fmt.Errorf("executing GraphQL query for %s: %w", what, err)
		}
		if result == nil {
			return NullValue{}, nil
		}
		g.MaybeNull = false
		return g, nil
	}
	return g.resolveObject(ctx, what)
}

// isAbstract reports whether the handle is typed as a GraphQL interface or
// union, so its concrete type is not known without asking the server. List
// handles never are: their elements are reached through a projection, which
// reports each element's type itself.
func (g GraphQLValue) isAbstract() bool {
	if g.Schema == nil || (g.Field != nil && isListTypeRef(g.Field.TypeRef)) {
		return false
	}
	t := g.Schema.Types.Get(g.TypeName)
	return t != nil && (t.Kind == introspection.TypeKindInterface || t.Kind == introspection.TypeKindUnion)
}

// concreteObjectTypeName returns the handle's type name when it is already
// known to be a concrete object type, with a chain scoped to it: a single
// (non-list) handle of an object type that can't still turn out null. Inline
// fragments on such a handle are matched without asking for __typename.
func (g GraphQLValue) concreteObjectTypeName() (string, bool) {
	if g.MaybeNull || g.Schema == nil || (g.Field != nil && isListTypeRef(g.Field.TypeRef)) {
		return "", false
	}
	t := g.Schema.Types.Get(g.TypeName)
	if t == nil || t.Kind != introspection.TypeKindObject {
		return "", false
	}
	return g.TypeName, true
}

// resolveObject runs a single query against the end of the handle's chain
// for the object's `__typename`, and its `id` when it has a loadable one (see
// nullProbeSelection). It returns null if the server resolved the object to
// null, or else a non-null handle of the object's concrete type. It backs
// both the null probe (resolveNull) and `case` on an interface or union
// handle (see Case.Eval). what names the field for error messages.
//
// Like the Dagger Go SDK, the object is then loaded from its ID, so later
// selections start from the loader instead of re-running the whole chain, and
// passing the object as an ID-typed argument costs nothing. Without a loader
// the original chain is kept, still with its ID known. Either way the handle
// is narrowed to its concrete type, so an interface- or union-typed handle
// matches `case` type patterns by what it actually is.
func (g GraphQLValue) resolveObject(ctx context.Context, what string) (Value, error) {
	fields, idType := nullProbeSelection(g.Schema, g.TypeName)
	var result map[string]any
	probe := g.QueryChain.SelectMultiple(fields...).Bind(&result).Client(g.Client)
	if err := probe.Execute(ctx); err != nil {
		return nil, fmt.Errorf("executing GraphQL query for %s: %w", what, err)
	}
	if result == nil {
		return NullValue{}, nil
	}
	g.MaybeNull = false

	typeName, _ := result["__typename"].(string)
	if !g.knowsType(typeName) {
		typeName = g.TypeName
	}
	if id, ok := result["id"].(string); ok && id != "" {
		loaded, err := loadObjectFromTypedID(id, idType, typeName, g.Schema, g.TypeScope, g.Client)
		if err != nil {
			return nil, err
		}
		if loaded != nil {
			return loaded, nil
		}
		g.KnownID = id
	}
	if typeName != g.TypeName {
		g = g.narrowTo(typeName)
	}
	return g, nil
}

// knowsType reports whether typeName is a type the handle can be retyped as:
// one present in both the schema and the type environment.
func (g GraphQLValue) knowsType(typeName string) bool {
	if typeName == "" || g.Schema == nil || g.TypeScope == nil || g.Schema.Types.Get(typeName) == nil {
		return false
	}
	_, found := g.TypeScope.NamedType(typeName)
	return found
}

// narrowTo retypes an interface- or union-typed handle as the concrete type
// typeName it was found to have, selecting further fields through an inline
// fragment. The handle is returned unchanged if the type is unknown.
func (g GraphQLValue) narrowTo(typeName string) GraphQLValue {
	if !g.knowsType(typeName) {
		return g
	}
	concrete, _ := g.TypeScope.NamedType(typeName)
	name := g.Name
	if g.Field != nil {
		name = g.Field.Name
	}
	g.TypeName = typeName
	g.Field = concreteTypeField(name, g.Schema.Types.Get(typeName))
	g.ValType = hm.NonNullType{Type: concrete}
	g.QueryChain = g.QueryChain.InlineFragment(typeName)
	return g
}

// isListTypeRef reports whether typeRef is a (possibly non-null) list.
func isListTypeRef(typeRef *introspection.TypeRef) bool {
	if typeRef != nil && typeRef.Kind == introspection.TypeKindNonNull {
		typeRef = typeRef.OfType
	}
	return typeRef != nil && typeRef.Kind == introspection.TypeKindList
}

func graphQLResultToValue(raw any, typeRef *introspection.TypeRef, expectedType string, schema *introspection.Schema, typeScope TypeScope, client graphql.Client) (Value, error) {
	if typeRef == nil {
		return ToValue(raw)
	}

	switch typeRef.Kind {
	case introspection.TypeKindNonNull:
		if raw == nil {
			return nil, fmt.Errorf("null is not allowed for %s", getTypeName(typeRef))
		}
		return graphQLResultToValue(raw, typeRef.OfType, expectedType, schema, typeScope, client)
	case introspection.TypeKindList:
		if raw == nil {
			return NullValue{}, nil
		}
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("expected list for GraphQL %s, got %T", getTypeName(typeRef), raw)
		}
		values := make([]Value, len(items))
		for i, item := range items {
			converted, err := graphQLResultToValue(item, typeRef.OfType, expectedType, schema, typeScope, client)
			if err != nil {
				return nil, fmt.Errorf("converting list element %d: %w", i, err)
			}
			values[i] = converted
		}
		list := ListValue{Elements: values}
		if typeScope != nil {
			if elemType, err := gqlOutputTypeRefToTypeNode(typeScope, typeRef.OfType, expectedType); err == nil {
				list.ElemType = elemType
			}
		}
		return list, nil
	}

	if raw == nil {
		return NullValue{}, nil
	}

	switch typeRef.Name {
	case "String", "ID":
		str, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected string for GraphQL %s, got %T", typeRef.Name, raw)
		}
		if typeRef.Name == "ID" && expectedType != "" {
			// @expectedType makes an ID-returning field infer as the object it
			// identifies (see gqlOutputTypeRefToTypeNode), so the runtime has to
			// hand back something you can actually select fields on, not the raw
			// ID string. Load the ID back into a lazy handle for that type; the
			// value keeps behaving as an ID wherever one is expected, since
			// arguments marshal it back through GraphQLValue.ID.
			val, err := loadObjectFromID(str, expectedType, schema, typeScope, client)
			if err != nil {
				return nil, err
			}
			if val != nil {
				return val, nil
			}
		}
		return StringValue{Val: str}, nil
	case "Int":
		return graphQLIntResultToValue(raw)
	case "Float":
		return graphQLFloatResultToValue(raw)
	case "Boolean":
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("expected boolean for GraphQL Boolean, got %T", raw)
		}
		return BoolValue{Val: b}, nil
	}

	if schema != nil {
		if schemaType := schema.Types.Get(typeRef.Name); schemaType != nil {
			switch schemaType.Kind {
			case introspection.TypeKindEnum:
				str, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("expected string for enum %s, got %T", typeRef.Name, raw)
				}
				if typeScope == nil {
					return nil, fmt.Errorf("enum type %s not found", typeRef.Name)
				}
				enumType, found := typeScope.NamedType(typeRef.Name)
				if !found {
					return nil, fmt.Errorf("enum type %s not found", typeRef.Name)
				}
				return EnumValue{Val: str, EnumType: enumType}, nil
			case introspection.TypeKindScalar:
				str, ok := raw.(string)
				if ok {
					if typeScope == nil {
						return nil, fmt.Errorf("scalar type %s not found", typeRef.Name)
					}
					scalarType, found := typeScope.NamedType(typeRef.Name)
					if !found {
						return nil, fmt.Errorf("scalar type %s not found", typeRef.Name)
					}
					return ScalarValue{Val: str, ScalarType: scalarType}, nil
				}
			}
		}
	}

	return ToValue(raw)
}

// graphQLIntResultToValue and graphQLFloatResultToValue convert a GraphQL
// number to a Dang value. GraphQL responses are decoded with UseNumber (see
// querybuilder.unpack), so numbers arrive as json.Number — the same shape the
// codec path produces — letting both paths share decodedNumberToInt.
func graphQLIntResultToValue(raw any) (Value, error) {
	num, ok := raw.(json.Number)
	if !ok {
		return nil, fmt.Errorf("expected number for GraphQL Int, got %T", raw)
	}
	val, err := decodedNumberToInt(num)
	if err != nil {
		return nil, fmt.Errorf("expected integral number for GraphQL Int, got %q", num.String())
	}
	return IntValue{Val: val}, nil
}

func graphQLFloatResultToValue(raw any) (Value, error) {
	num, ok := raw.(json.Number)
	if !ok {
		return nil, fmt.Errorf("expected number for GraphQL Float, got %T", raw)
	}
	val, err := num.Float64()
	if err != nil {
		return nil, fmt.Errorf("invalid number %q", num.String())
	}
	return FloatValue{Val: val}, nil
}

// Helper function to convert Dang values to Go values for GraphQL
func dangValueToGo(val Value) (any, error) {
	switch v := val.(type) {
	case StringValue:
		return v.Val, nil
	case EnumValue:
		return gqlEnumValue{val: v.Val}, nil
	case ScalarValue:
		// Scalar values are represented as strings in GraphQL
		return v.Val, nil
	case IntValue:
		return v.Val, nil
	case FloatValue:
		return v.Val, nil
	case BoolValue:
		return v.Val, nil
	case NullValue:
		return nil, nil
	case MapValue:
		// Convert map to a Go map (used for input objects / map-shaped scalars)
		result := make(map[string]any, len(v.Entries))
		for key, fieldVal := range v.Entries {
			goVal, err := dangValueToGo(fieldVal)
			if err != nil {
				return nil, fmt.Errorf("converting map entry %q: %w", key, err)
			}
			result[key] = goVal
		}
		return result, nil
	case ListValue:
		// Convert list elements to Go slice
		result := make([]any, len(v.Elements))
		for i, elem := range v.Elements {
			goVal, err := dangValueToGo(elem)
			if err != nil {
				return nil, fmt.Errorf("converting list element %d: %w", i, err)
			}
			result[i] = goVal
		}
		return result, nil
	case *Object:
		// Convert module value to map (used for input objects)
		result := make(map[string]any)
		for name, fieldVal := range v.Values {
			goVal, err := dangValueToGo(fieldVal)
			if err != nil {
				return nil, fmt.Errorf("converting field %s: %w", name, err)
			}
			result[name] = goVal
		}
		return result, nil
	case GraphQLValue:
		if isObjectLikeType(v.Field.TypeRef, v.Schema) {
			return gqlObjectMarshaller{val: v}, nil
		}
		return nil, fmt.Errorf("unsupported value type (%T): %s", val, pretty.Sprint(v))
	default:
		return nil, fmt.Errorf("unsupported value type: %T", val)
	}
}

// gqlEnumValue implements the querybuilder enum interface so enum values
// are serialized as bare identifiers (REPOSITORY) rather than strings ("REPOSITORY").
type gqlEnumValue struct {
	val string
}

func (e gqlEnumValue) IsEnum()        {}
func (e gqlEnumValue) Name() string   { return e.val }
func (e gqlEnumValue) Value() string  { return e.val }
func (e gqlEnumValue) String() string { return e.val }

type gqlObjectMarshaller struct {
	val GraphQLValue
}

func (m gqlObjectMarshaller) XXX_GraphQLType() string {
	return m.val.TypeName
}

func (m gqlObjectMarshaller) XXX_GraphQLIDType() string {
	return "ID"
}

// XXX_GraphqlID is an internal function. It returns the underlying type ID
func (m gqlObjectMarshaller) XXX_GraphQLID(ctx context.Context) (string, error) {
	return m.val.ID(ctx)
}

func (m gqlObjectMarshaller) MarshalJSON() ([]byte, error) {
	id, err := m.XXX_GraphQLID(context.TODO())
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
}

// Runtime value implementations

// DeferredValue is an opaque decoded data value returned by deserializers like
// JSON.decode and YAML.decode. It is materialized only when an expected type is
// available. Codec records which format produced it, so materialization can
// honor that format's @FORMAT.field key renames.
type DeferredValue struct {
	Raw   any
	Codec Codec
}

var _ Value = DeferredValue{}

func (d DeferredValue) Type() hm.Type {
	return hm.TypeVariable('d')
}

func (d DeferredValue) String() string {
	bytes, err := json.Marshal(d.Raw)
	if err != nil {
		return fmt.Sprintf("deferred:%v", d.Raw)
	}
	return string(bytes)
}

func (d DeferredValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Raw)
}

// StringValue represents a string value
type StringValue struct {
	Val string
}

func (s StringValue) Type() hm.Type {
	return hm.NonNullType{Type: StringType}
}

func (s StringValue) String() string {
	return s.Val
}

func (s StringValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Val)
}

// EnumValue represents an enum value with a specific enum type
type EnumValue struct {
	Val      string
	EnumType hm.Type
}

var _ Value = EnumValue{}

func (e EnumValue) Type() hm.Type {
	return hm.NonNullType{Type: e.EnumType}
}

func (e EnumValue) String() string {
	return e.Val
}

func (e EnumValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.Val)
}

// ScalarValue represents a custom scalar value
type ScalarValue struct {
	Val        string
	ScalarType hm.Type
}

var _ Value = ScalarValue{}

func (s ScalarValue) Type() hm.Type {
	return hm.NonNullType{Type: s.ScalarType}
}

func (s ScalarValue) String() string {
	return s.Val
}

func (s ScalarValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Val)
}

// IntValue represents an integer value
type IntValue struct {
	Val int
}

func (i IntValue) Type() hm.Type {
	return hm.NonNullType{Type: IntType}
}

func (i IntValue) String() string {
	return fmt.Sprintf("%d", i.Val)
}

func (i IntValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Val)
}

// FloatValue represents a floating-point value
type FloatValue struct {
	Val float64
}

func (f FloatValue) Type() hm.Type {
	return hm.NonNullType{Type: FloatType}
}

func (f FloatValue) String() string {
	return fmt.Sprintf("%g", f.Val)
}

func (f FloatValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(f.Val)
}

// BoolValue represents a boolean value
type BoolValue struct {
	Val bool
}

func (b BoolValue) Type() hm.Type {
	return hm.NonNullType{Type: BooleanType}
}

func (b BoolValue) String() string {
	return fmt.Sprintf("%t", b.Val)
}

func (b BoolValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.Val)
}

// NullValue represents a null value
type NullValue struct{}

func (n NullValue) Type() hm.Type {
	// Null has no specific type, return a fresh type variable
	return hm.TypeVariable('n')
}

func (n NullValue) String() string {
	return "null"
}

func (n NullValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(nil)
}

// ListValue represents a list value
type ListValue struct {
	Elements []Value
	ElemType hm.Type
}

func (l ListValue) Type() hm.Type {
	return hm.NonNullType{Type: ListType{l.ElemType}}
}

func (l ListValue) String() string {
	var result strings.Builder
	result.WriteString("[")
	for i, elem := range l.Elements {
		if i > 0 {
			result.WriteString(", ")
		}
		result.WriteString(elem.String())
	}
	return result.String() + "]"
}

func (l ListValue) MarshalJSON() ([]byte, error) {
	if l.Elements == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(l.Elements)
}

// MapValue is an immutable string-keyed map. Keys are tracked in insertion
// order so iteration, printing, and JSON marshalling are deterministic.
type MapValue struct {
	Keys    []string
	Entries map[string]Value
	ValType hm.Type
}

func (m MapValue) Type() hm.Type {
	return hm.NonNullType{Type: MapType{m.ValType}}
}

// Get returns the value for a key, and whether it was present.
func (m MapValue) Get(key string) (Value, bool) {
	v, ok := m.Entries[key]
	return v, ok
}

// With returns a copy of the map with key set to value (insertion order
// preserved; existing keys keep their position).
func (m MapValue) With(key string, value Value) MapValue {
	entries := make(map[string]Value, len(m.Entries)+1)
	for k, v := range m.Entries {
		entries[k] = v
	}
	keys := append([]string{}, m.Keys...)
	if _, exists := entries[key]; !exists {
		keys = append(keys, key)
	}
	entries[key] = value
	return MapValue{Keys: keys, Entries: entries, ValType: m.ValType}
}

// Without returns a copy of the map with key removed.
func (m MapValue) Without(key string) MapValue {
	if _, exists := m.Entries[key]; !exists {
		return m
	}
	entries := make(map[string]Value, len(m.Entries))
	keys := make([]string, 0, len(m.Keys))
	for _, k := range m.Keys {
		if k == key {
			continue
		}
		keys = append(keys, k)
		entries[k] = m.Entries[k]
	}
	return MapValue{Keys: keys, Entries: entries, ValType: m.ValType}
}

func (m MapValue) String() string {
	if len(m.Keys) == 0 {
		return "[:]"
	}
	var result strings.Builder
	result.WriteString("[")
	for i, k := range m.Keys {
		if i > 0 {
			result.WriteString(", ")
		}
		result.WriteString(strconv.Quote(k))
		result.WriteString(": ")
		result.WriteString(m.Entries[k].String())
	}
	return result.String() + "]"
}

func (m MapValue) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.Keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(m.Entries[k])
		if err != nil {
			return nil, err
		}
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// FunctionValue represents a function value
type FunctionValue struct {
	Args           []string
	Body           Node
	Closure        ValueScope
	FnType         *hm.FunctionType
	Defaults       map[string]Node // Map of argument name to default value expression
	ArgDecls       []*FieldDecl    // Original argument declarations with directives
	BlockParamName string          // Name of the block parameter, if any
	Directives     []*DirectiveApplication
	IsDynamic      bool // True if this function has access to dynamic scope

	// SelfScope is the scope a dynamic (method-like) function was referenced
	// from by bare name (`helper(1)`, `&helper`); see bindLexicalSelf. Its
	// `self` is the receiver the function runs against, wherever the value
	// ends up being called. Nil until referenced.
	SelfScope ValueScope

	// Nested marks a function declared inside a method or new() body. It is
	// a closure, like a block: its body runs on its own Closure, so the
	// method's args and locals shadow fields as they do anywhere else in the
	// method, and it shares the method's `self` cell, so its field writes
	// accumulate into the method's fork; see FunctionValue.Call.
	Nested bool

	IsBlockArg          bool
	CapturedReturnFrame *ControlFrame
	CapturedBreakFrame  *ControlFrame
}

func (f FunctionValue) Type() hm.Type {
	return f.FnType
}

func (f FunctionValue) String() string {
	return fmt.Sprintf("function(%v)", f.Args)
}

func (f FunctionValue) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("cannot marshal function value")
}

// bindLexicalSelf records scope as the place a dynamic function was referenced
// by bare name, so that calls run against that scope's `self` (read when the
// call happens, like any closure variable) rather than the caller's. Blocks
// already close over their scope, and an already-bound value keeps its
// original binding however far it travels.
func (f FunctionValue) bindLexicalSelf(scope ValueScope) FunctionValue {
	if !f.IsDynamic || f.IsBlockArg || f.SelfScope != nil {
		return f
	}
	if _, ok := scope.Self(); ok {
		f.SelfScope = scope
	}
	return f
}

func (f FunctionValue) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	// A naked self-call to a sibling method or computed field (`foo()` rather
	// than `self.foo()`, recursion included) must behave exactly like
	// `self.foo()`: each call forks `self` into its own sealed receiver, so the
	// body's field writes stay isolated copy-on-write generations. Dispatch it
	// through the same path as an explicit method call. Running the body against
	// the closure scope instead (the fall-through below) shares one `self` cell
	// across recursive calls and lets bare field writes leak out to the original
	// instance through the lexical parent chain — scrambling state. Block
	// arguments are excluded: they intentionally share the enclosing method's
	// `self` (so `xs.each { self.x += ... }` accumulates), and BoundMethod.Call
	// would re-dispatch a block straight back here.
	//
	// The receiver is the `self` of the scope the function was referenced
	// from (SelfScope), not of the scope calling it: a reference that escapes
	// (`b.hook = &helper`, then `b.fire` calls `hook(x)`) must keep running
	// against the object it was written in, never whichever object happens to
	// invoke it. The caller's `self` is only a fallback for values that were
	// never bound.
	//
	// Nested functions (declared inside a method body) are closures and skip
	// this dispatch; see below.
	selfScope := scope
	if f.SelfScope != nil {
		selfScope = f.SelfScope
	}
	if f.IsDynamic && !f.IsBlockArg && !f.Nested {
		if self, ok := selfScope.Self(); ok {
			method := f
			method.SelfScope = nil // the receiver below is the binding now
			if recv, ok := self.(ValueScope); ok {
				return BoundMethod{Method: method, Receiver: recv}.Call(ctx, scope, args)
			}
			// A scalar method's self is a plain value, not a scope: a bare
			// sibling call inside a scalar body re-dispatches with a fresh
			// self cell the same way (falling through would MutateSelf a
			// scope derived from the shared closure).
			if sv, ok := self.(ScalarValue); ok {
				return BoundScalarMethod{Method: method, Receiver: sv}.Call(ctx, scope, args)
			}
		}
	}

	frame, err := enterCall(ctx)
	if err != nil {
		return nil, err
	}

	fnScope := f.Closure.Derive(false)

	switch {
	case f.Nested:
		// A nested function is a closure, like a block: it runs on a derive
		// of its closure, so the enclosing method's args and locals resolve
		// lexically (shadowing fields), and it shares the method's `self`
		// cell, so its field writes (bare or `self.`) accumulate into the
		// method's fork. That is what makes it usable as an inline helper,
		// e.g. `let bump: Int! { n += 1; n }` called repeatedly.
	case f.IsDynamic && !f.IsBlockArg:
		// Fallback for a receiver that is neither a scope nor a scalar. Enter
		// a fresh cell: MutateSelf would overwrite the cell fnScope shares
		// with the closure, rebinding `self` for everything else closed over
		// it.
		if self, ok := selfScope.Self(); ok {
			fnScope.EnterSelf(self)
		}
	}

	if f.IsBlockArg {
		invocationFrame := NewControlFrame(BlockInvocationFrame)

		frame.returnFrame = f.CapturedReturnFrame
		frame.breakFrame = f.CapturedBreakFrame
		frame.continueFrame = invocationFrame
		blockCtx := withFrame(ctx, frame)

		if err := f.BindArgs(blockCtx, fnScope, args); err != nil {
			return nil, err
		}

		val, err := EvalNode(blockCtx, fnScope, f.Body)
		if err != nil {
			var continueEx *ContinueException
			if errors.As(err, &continueEx) && controlFrameMatches(continueEx.Target, invocationFrame) {
				if continueEx.HasValue && continueEx.Value != nil {
					return continueEx.Value, nil
				}
				return NullValue{}, nil
			}
			return nil, err
		}
		return val, nil
	}

	returnFrame := NewControlFrame(ReturnFrame)
	frame.enterFunctionControlBoundary()
	frame.returnFrame = returnFrame
	fnCtx := withFrame(ctx, frame)

	if err := f.BindArgs(fnCtx, fnScope, args); err != nil {
		return nil, err
	}

	val, err := EvalNode(fnCtx, fnScope, f.Body)
	if err != nil {
		if returnVal, ok := returnValueFromError(err, returnFrame); ok {
			return returnVal, nil
		}
		return nil, err
	}
	return val, nil
}

func (f FunctionValue) BindArgs(ctx context.Context, fnScope ValueScope, args map[string]Value) error {
	// Bind arguments to the function environment
	for _, argName := range f.Args {
		if val, exists := args[argName]; exists {
			// Handle null values with defaults
			if _, isNull := val.(NullValue); isNull {
				if defaultExpr, hasDefault := f.Defaults[argName]; hasDefault {
					// Evaluate in fnScope so earlier args are visible to the default expression
					defaultVal, err := EvalNode(ctx, fnScope, defaultExpr)
					if err != nil {
						return fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
					}
					fnScope.Bind(argName, defaultVal, PrivateVisibility)
				} else {
					fnScope.Bind(argName, val, PrivateVisibility)
				}
			} else {
				fnScope.Bind(argName, val, PrivateVisibility)
			}
		} else if defaultExpr, hasDefault := f.Defaults[argName]; hasDefault {
			// Use default value when argument not provided.
			// Evaluate in fnScope so earlier args are visible to the default expression.
			defaultVal, err := EvalNode(ctx, fnScope, defaultExpr)
			if err != nil {
				return fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
			}
			fnScope.Bind(argName, defaultVal, PrivateVisibility)
		} else {
			fnScope.Bind(argName, NullValue{}, PrivateVisibility)
		}
	}

	// Bind block parameter if the function has one
	if f.FnType.Block() != nil {
		// Get block parameter name from function type
		// We need to store this when creating the FunctionValue
		blockParamName := f.BlockParamName
		if blockParamName != "" {
			// Extract block arg from context
			if blockVal := currentBlockArg(ctx); blockVal != nil {
				fnScope.Bind(blockParamName, blockVal, PrivateVisibility)
			}
		}
	}

	return nil
}

func (f FunctionValue) ParameterNames() []string {
	return f.Args
}

func (f FunctionValue) IsAutoCallable() bool {
	if f.BlockParamName != "" || (f.FnType != nil && f.FnType.Block() != nil) {
		return false
	}

	for _, argName := range f.Args {
		// If this argument has a default value, it's optional
		if _, hasDefault := f.Defaults[argName]; hasDefault {
			continue
		}

		// If no default, check if the type is nullable (optional)
		if rt, ok := f.FnType.Arg().(*RecordType); ok {
			scheme, found := rt.SchemeOf(argName)
			if found {
				if fieldType, _ := scheme.Type(); fieldType != nil {
					if _, isNonNull := fieldType.(hm.NonNullType); isNonNull {
						// This is a required argument with no default value
						return false
					}
				}
			}
		}
	}
	// All arguments are either optional or have defaults, so this function can be auto-called
	return true
}

// pendingInit is a deferred initializer registered via BindLazy. It runs at
// most once: on the first Lookup for its name. The result replaces the
// pending entry in Values, so subsequent reads bypass the initializer.
//
// mu serializes forcing so concurrent readers (e.g. independent object-literal
// fields that reference the same lazy binding) run the initializer once and
// share the result, rather than racing on the owning Object's maps. The
// initializer runs while holding mu, so an unrelated concurrent reader of the
// same binding blocks until it completes. Re-entry by the *same* evaluation
// chain (a real initialization cycle) is detected via the force chain carried
// on the context before mu is taken, so it errors instead of deadlocking.
type pendingInit struct {
	Init       func(ctx context.Context) (Value, error)
	Visibility Visibility

	mu   sync.Mutex
	done bool
	val  Value
	err  error
}

// forceChain is a linked list, carried on the evaluation frame, of the pending
// initializers currently being forced in one evaluation chain. It lets force
// distinguish a genuine self-referential cycle (the same chain re-enters a
// pending it is already forcing) from ordinary concurrent access by an
// unrelated goroutine (which must wait, not error).
type forceChain struct {
	p      *pendingInit
	parent *forceChain
}

// forcingInChain reports whether p is already being forced earlier in ctx's
// evaluation chain.
func forcingInChain(ctx context.Context, p *pendingInit) bool {
	for fc := currentFrame(ctx).forceChain; fc != nil; fc = fc.parent {
		if fc.p == p {
			return true
		}
	}
	return false
}

// withForceChain returns ctx extended with p as the innermost initializer
// being forced.
func withForceChain(ctx context.Context, p *pendingInit) context.Context {
	f := deriveFrame(ctx)
	f.forceChain = &forceChain{p: p, parent: f.forceChain}
	return withFrame(ctx, f)
}

// evalParallel runs fn for each index in [0, n) concurrently and returns the
// results in index order. It is the shared concurrency contract behind every
// `{{ }}`: independent work runs in parallel and evaluation fails fast — the
// first error cancels the context handed to the still-running tasks — but every
// task is awaited and a single error is chosen deterministically regardless of
// completion order. Single-item (and empty) work runs inline to avoid goroutine
// and context overhead.
func evalParallel[T any](ctx context.Context, n int, fn func(ctx context.Context, i int) (T, error)) ([]T, error) {
	results := make([]T, n)
	switch n {
	case 0:
		return results, nil
	case 1:
		v, err := fn(ctx, 0)
		if err != nil {
			return nil, err
		}
		results[0] = v
		return results, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			v, err := fn(ctx, i)
			if err != nil {
				errs[i] = err
				cancel()
				return
			}
			results[i] = v
		}(i)
	}
	wg.Wait()

	// Report the lowest-index failure, but prefer a genuine error over the
	// context.Canceled errors our own fail-fast cancellation may have induced in
	// still-running siblings — otherwise the actual cause could be masked by
	// cancellation noise from a lower index (e.g. a list of GraphQL queries
	// where one fails and the rest are cancelled mid-flight). Completed
	// sibling failures ride along on a parallelFailure wrapper so the
	// boundary report can show every parallel failure that actually
	// happened; the wrapper unwraps to the primary, so rescue dispatch and
	// errors.Is/As are unaffected.
	var canceled error
	var genuine []error
	for i := range errs {
		switch {
		case errs[i] == nil:
		case errors.Is(errs[i], context.Canceled):
			if canceled == nil {
				canceled = errs[i]
			}
		default:
			genuine = append(genuine, errs[i])
		}
	}
	switch {
	case len(genuine) == 1:
		return nil, genuine[0]
	case len(genuine) > 1:
		return nil, &parallelFailure{Primary: genuine[0], Also: genuine[1:]}
	case canceled != nil:
		return nil, canceled
	}
	return results, nil
}

// forkTaskScope derives the scope one concurrent `{{ }}` task evaluates in.
//
// Sealing (Derive(true)) keeps the task's writes to outer locals private, but
// Derive shares the dynamic-scope cell, so a field write inside the task
// (`self.field = ...`, or a bare one, which forks `self` the same way) would
// write the enclosing method's cell: racing with sibling tasks and leaking into
// the method's `self` after the `{{ }}`. Each task therefore gets its own cell,
// seeded with the enclosing `self`. The task frame starts name resolution with
// that cell, so its bare field reads and writes go through the task's `self`
// generation too (see nextFrame), and the copy-on-write forks they create stay
// task-local.
func forkTaskScope(scope ValueScope) ValueScope {
	task := scope.Derive(true)
	if self, ok := scope.Self(); ok {
		task.EnterSelf(self)
	}
	return task
}

// DynamicScope is a shared mutable cell for the 'self' value.
// Cloned environments share the same cell so that mutations to self
// inside closures (e.g. inside .each blocks) are visible to the
// enclosing scope.
//
// The cell is guarded by mu because a closure can write it from several
// goroutines at once: a block created in a method and invoked from the
// concurrent tasks of a `{{ }}` writes the method's `self` (writes made
// lexically inside a task go to that task's private cell, see forkTaskScope).
// The order of such writes is unspecified; the lock only keeps them memory-safe
// and stops a fork from being lost (see swap). mu is a leaf lock: nothing else
// is acquired while it is held.
type DynamicScope struct {
	mu    sync.Mutex
	value Value
}

func newDynamicScope(v Value) *DynamicScope {
	return &DynamicScope{value: v}
}

// get returns the current `self`.
func (d *DynamicScope) get() Value {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.value
}

// set replaces the current `self`.
func (d *DynamicScope) set(v Value) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.value = v
}

// swap replaces `self` with next only if it is still old, reporting whether it
// did. Callers derive next from old outside the lock and retry on failure, so
// a concurrent fork of `self` is built on rather than overwritten. old is an
// *Object so the identity comparison can never hit an uncomparable Value.
func (d *DynamicScope) swap(old *Object, next Value) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cur, ok := d.value.(*Object); !ok || cur != old {
		return false
	}
	d.value = next
	return true
}

// Object represents an instance of a Type; it implements ValueScope
type Object struct {
	// mu guards Values, Visibilities, and Pending so a scope can be read
	// concurrently while lazy initializers are forced — e.g. when independent
	// object-literal fields are evaluated in parallel and read shared lexical
	// bindings.
	mu           sync.Mutex
	Mod          TypeScope
	Values       map[string]Value
	Visibilities map[string]Visibility   // Track visibility of each field
	Pending      map[string]*pendingInit // Deferred initializers; entries move to Values on force
	Parent       *Object                 // For hierarchical scoping
	IsForked     bool                    // Sealed scopes keep Update local (no walk into Parent)
	dynamicScope *DynamicScope           // Shared cell for 'self' in this scope

	// selfParent marks a method call's outermost frame (see BoundMethod.Call).
	// Its effective parent is not Parent but the current value of its `self`
	// cell, so bare field reads see the latest copy-on-write generation of
	// `self` (including `self.x = v` writes), and bare field writes fork `self`
	// instead of mutating a receiver frame in place. See nextFrame and Update.
	selfParent bool
}

// nextFrame returns the frame name resolution continues in after m, and
// whether that frame is `self` (see selfParent). It reads only fields that
// are fixed once a frame is built, plus the `self` cell, so it is safe with or
// without m.mu held.
//
// cell is the `self` cell of the frame the resolution started from. A
// selfParent frame continues into that cell's `self` rather than its own, so
// a frame that entered a fresh `self` over a method's scope (a `{{ }}` task,
// see forkTaskScope) resolves the method's locals and args lexically but its
// fields through its own `self`. Every other frame shares its method's cell,
// making the two the same; nil falls back to m's.
func (m *Object) nextFrame(cell *DynamicScope) (*Object, bool) {
	if m.selfParent {
		if cell == nil {
			cell = m.dynamicScope
		}
		if cell != nil {
			if self, ok := cell.get().(*Object); ok {
				return self, true
			}
		}
	}
	return m.Parent, false
}

// NewObject creates a new Object with an empty values map
func NewObject(mod TypeScope) *Object {
	return &Object{
		Mod:          mod,
		Values:       make(map[string]Value),
		Visibilities: make(map[string]Visibility),
		Parent:       nil,
	}
}

func (m *Object) Type() hm.Type {
	return hm.NonNullType{Type: m.Mod}
}

func (m *Object) String() string {
	return fmt.Sprintf("module %s", m.Mod)
}

// The frame-chain walks below (lookupFrom, hasFrom, lookupValueFrom,
// visibilityFrom, bindingsFrom, updateFrom, Self) are loops rather than
// recursion, so a deep chain costs heap, not Go stack. Each follows nextFrame
// with the `cell` its resolution started from (Self follows Parent) and locks
// one frame at a time, child before parent, releasing each before moving on.
// The exception is updateFrom's writes: it calls visibilityLocked with the
// written frame's lock still held, which walks on through visibilityFrom,
// locking each further frame in turn while the written frame stays locked —
// two locks at once, still in child -> parent order. The `self` cell's lock
// (taken by nextFrame) is a leaf lock.

func (m *Object) Lookup(ctx context.Context, name string) (Value, bool, error) {
	return m.lookupFrom(ctx, name, m.dynamicScope)
}

// lookupFrom is Lookup for a resolution that started at a frame whose `self`
// cell is cell (see nextFrame).
func (m *Object) lookupFrom(ctx context.Context, name string, cell *DynamicScope) (Value, bool, error) {
	for o := m; o != nil; {
		o.mu.Lock()
		p, pending := o.Pending[name]
		val, hasVal := o.Values[name]
		parent, _ := o.nextFrame(cell)
		o.mu.Unlock()
		if pending {
			return o.force(ctx, name, p)
		}
		if hasVal {
			return val, true, nil
		}
		o = parent
	}
	return nil, false, nil
}

func (m *Object) Has(name string) bool {
	return m.hasFrom(name, m.dynamicScope)
}

func (m *Object) hasFrom(name string, cell *DynamicScope) bool {
	for o := m; o != nil; {
		o.mu.Lock()
		_, pending := o.Pending[name]
		_, hasVal := o.Values[name]
		parent, isSelf := o.nextFrame(cell)
		o.mu.Unlock()
		if pending || hasVal {
			return true
		}
		// A field `self`'s type declares is there even before it has a
		// value (a required field new() has yet to assign), so a bare write
		// to it is a field write, not a write to some outer binding.
		if parent != nil && isSelf && declaresField(parent, name) {
			return true
		}
		o = parent
	}
	return false
}

// BindLazy records a deferred initializer. The first Lookup for name will run
// init and replace the pending entry with the resulting value. BindLazy is a
// no-op if name is already bound locally — letting earlier bindings
// (constructor args, prior installs) shadow the placeholder.
func (m *Object) BindLazy(name string, init func(ctx context.Context) (Value, error), visibility Visibility) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, alreadyReal := m.Values[name]; alreadyReal {
		return
	}
	if _, alreadyPending := m.Pending[name]; alreadyPending {
		return
	}
	if m.Pending == nil {
		m.Pending = make(map[string]*pendingInit)
	}
	m.Pending[name] = &pendingInit{
		Init:       init,
		Visibility: visibility,
	}
	m.Visibilities[name] = visibility
}

func (m *Object) force(ctx context.Context, name string, p *pendingInit) (Value, bool, error) {
	// A genuine initialization cycle: this evaluation chain is already forcing
	// p. Detect it before taking p.mu so we report an error rather than
	// deadlocking on our own lock. Concurrent forcing by an unrelated goroutine
	// is not a cycle — it falls through and waits on p.mu below.
	if forcingInChain(ctx, p) {
		return nil, true, fmt.Errorf("initialization cycle while evaluating variable %q", name)
	}

	// Serialize forcing of this binding. Holding p.mu across Init means a
	// concurrent reader of the same binding blocks until the result is ready and
	// then sees it cached, so the initializer runs exactly once — value or
	// error. Distinct bindings have distinct locks, so independent fields still
	// force in parallel.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return p.val, true, p.err
	}

	val, err := p.Init(withForceChain(ctx, p))
	if err == nil && val == nil {
		err = fmt.Errorf("initializer for variable %q returned nil", name)
	}
	p.val, p.err, p.done = val, err, true
	if err != nil {
		return nil, true, err
	}

	m.mu.Lock()
	delete(m.Pending, name)
	m.Values[name] = val
	m.mu.Unlock()

	return val, true, nil
}

func (m *Object) LookupLocal(name string) (Value, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	val, ok := m.Values[name]
	return val, ok
}

// lookupValue walks Values + the parent chain (without forcing pending
// initializers). Use this only when pending has already been forced —
// otherwise Lookup is the right call.
func (m *Object) lookupValue(name string) (Value, bool) {
	return m.lookupValueFrom(name, m.dynamicScope)
}

func (m *Object) lookupValueFrom(name string, cell *DynamicScope) (Value, bool) {
	for o := m; o != nil; {
		o.mu.Lock()
		val, ok := o.Values[name]
		parent, _ := o.nextFrame(cell)
		o.mu.Unlock()
		if ok {
			return val, true
		}
		o = parent
	}
	return nil, false
}

func (m *Object) Bind(name string, value Value, visibility Visibility) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Values[name] = value
	m.Visibilities[name] = visibility
}

func (m *Object) Visibility(name string) Visibility {
	return m.visibilityFrom(name, m.dynamicScope)
}

func (m *Object) visibilityFrom(name string, cell *DynamicScope) Visibility {
	for o := m; o != nil; {
		o.mu.Lock()
		vis, ok := o.Visibilities[name]
		parent, _ := o.nextFrame(cell)
		o.mu.Unlock()
		if ok {
			return vis
		}
		o = parent
	}
	return PrivateVisibility
}

// visibilityLocked resolves a visibility while the caller already holds m.mu.
// Local lookups stay under the held lock; the fallback walks on from m's next
// frame with visibilityFrom, taking each frame's own lock in turn while m.mu
// stays held (child -> parent ordering, never the reverse).
func (m *Object) visibilityLocked(name string, cell *DynamicScope) Visibility {
	if vis, ok := m.Visibilities[name]; ok {
		return vis
	}
	if parent, _ := m.nextFrame(cell); parent != nil {
		return parent.visibilityFrom(name, cell)
	}
	return PrivateVisibility
}

func (m *Object) Bindings(vis Visibility) []Keyed[Value] {
	return m.bindingsFrom(vis, m.dynamicScope)
}

func (m *Object) bindingsFrom(vis Visibility, cell *DynamicScope) []Keyed[Value] {
	var bindings []Keyed[Value]

	// Collect bindings frame by frame, nearest first, recording every key
	// seen (whatever its visibility) so bindings it shadows further along the
	// chain are filtered out.
	shadowed := map[string]struct{}{}
	for o := m; o != nil; {
		o.mu.Lock()
		for name, value := range o.Values {
			if _, isShadowed := shadowed[name]; isShadowed {
				continue
			}
			shadowed[name] = struct{}{}
			if o.Visibilities[name] >= vis {
				bindings = append(bindings, Keyed[Value]{
					Key:        name,
					Value:      value,
					Positional: false, // Type bindings are never positional
				})
			}
		}
		parent, _ := o.nextFrame(cell)
		o.mu.Unlock()
		o = parent
	}

	return bindings
}

func (m *Object) Derive(sealed bool) ValueScope {
	return &Object{
		Mod:          m.Mod,
		Values:       make(map[string]Value),
		Visibilities: make(map[string]Visibility),
		Parent:       m,
		IsForked:     sealed,         // Sealed scopes keep Update local
		dynamicScope: m.dynamicScope, // Share cell so closures see mutations
	}
}

// snapshotReceiver returns a sealed receiver frame for a method call that is
// immune to later in-place writes on m's chain.
//
// Field writes inside a method or new() never touch existing frames — bare or
// not, they fork `self` (see Object.Update, newSelfFrame and
// ConstructorScope.Update) — so a receiver is normally never written in place
// after the call starts. The snapshot is defensive against any remaining
// in-place writer of a receiver's chain, and it keeps receiver chains shallow,
// since every copy-on-write write adds a frame.
// Only frames sharing m's Mod are flattened (an instance's copy-on-write
// generations); the root and anything beyond stay shared. Those frames hold
// only accumulated writes, so this copies little.
func (m *Object) snapshotReceiver() *Object {
	var frames []*Object
	base := m
	for base.Parent != nil && base.Mod == m.Mod {
		frames = append(frames, base)
		base = base.Parent
	}
	if len(frames) == 0 {
		return m.Derive(true).(*Object)
	}

	snap := &Object{
		Mod:          m.Mod,
		Values:       make(map[string]Value),
		Visibilities: make(map[string]Visibility),
		Parent:       base,
		IsForked:     true,
		dynamicScope: m.dynamicScope,
	}
	// Apply the farthest frame first so nearer frames shadow it.
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		f.mu.Lock()
		for name, val := range f.Values {
			snap.Values[name] = val
			delete(snap.Pending, name)
		}
		for name, p := range f.Pending {
			if snap.Pending == nil {
				snap.Pending = make(map[string]*pendingInit)
			}
			snap.Pending[name] = p
			delete(snap.Values, name)
		}
		for name, vis := range f.Visibilities {
			snap.Visibilities[name] = vis
		}
		f.mu.Unlock()
	}
	return snap
}

// Update overwrites the nearest existing binding following proper scoping rules:
// - If the variable exists locally, update it locally
// - If the variable doesn't exist locally but exists in parent, update parent (unless forked)
// - If the variable doesn't exist anywhere, set it locally
func (m *Object) Update(name string, value Value) {
	m.updateFrom(name, value, m.dynamicScope)
}

func (m *Object) updateFrom(name string, value Value, cell *DynamicScope) {
	// inChain records that o's chain is known to bind name (the previous
	// step's parent.hasFrom, or by induction its own inChain), sparing a full
	// re-walk at every step: if o holds neither a value nor a pending
	// initializer for name and its next frame is not `self`, hasFrom(o) can
	// only have been true because hasFrom(parent) is, which is exactly the
	// check below. A frame with a pending initializer for name, or a `self`
	// link (whose cell may hold a newer generation by now, and where
	// declaresField also counts), re-checks from scratch.
	inChain := false
	for o := m; ; {
		o.mu.Lock()
		if _, existsLocally := o.Values[name]; existsLocally {
			// Variable exists locally, update it locally
			o.Values[name] = value
			o.Visibilities[name] = o.visibilityLocked(name, cell)
			o.mu.Unlock()
			return
		}
		_, pendingLocally := o.Pending[name]
		parent, isSelf := o.nextFrame(cell)
		forked := o.IsForked
		o.mu.Unlock()

		known := inChain && !pendingLocally && !isSelf
		inChain = false
		if parent != nil && !forked && (known || parent.hasFrom(name, cell) || isSelf && declaresField(parent, name)) {
			// A bare field write (`total += x`) inside a method: values are
			// immutable, so fork `self` exactly like `self.total += x` does
			// rather than writing into the receiver in place, which would
			// show through every alias of it (`let r = self`, a stored
			// `self`, a returned snapshot). The fork goes into the cell
			// nextFrame resolved `self` from.
			//
			// The cell can be written concurrently (a block invoked from
			// several `{{ }}` tasks), so install the fork only if `self` is
			// still the generation it was built from; otherwise fork the newer
			// generation, so neither write is lost.
			if isSelf && cell == nil {
				cell = o.dynamicScope
			}
			if !isSelf {
				// parent's chain binds name (see inChain).
				inChain = true
			}
			for isSelf {
				if cell.swap(parent, forkWithField(parent, name, value)) {
					return
				}
				parent, isSelf = o.nextFrame(cell)
			}
			// Variable exists in parent, update parent (only if not forked)
			o = parent
			continue
		}

		if forked && parent != nil {
			// A sealed frame (a `{{ }}` task, see forkTaskScope) keeps writes to
			// outer locals and args private by setting them locally, below. A
			// write to a field of `self` must still fork `self`, though — into
			// this frame's own cell, which for a task is private — or the task's
			// `self.field` reads would miss its bare `field = v` writes.
			if cell == nil {
				cell = o.dynamicScope
			}
			if cell != nil && parent.resolvesToSelfField(name, cell) {
				for {
					self, ok := cell.get().(*Object)
					if !ok {
						break
					}
					if cell.swap(self, forkWithField(self, name, value)) {
						return
					}
				}
			}
		}

		// Variable doesn't exist anywhere (or we're a forked boundary): set it
		// locally.
		o.mu.Lock()
		o.Values[name] = value
		o.Visibilities[name] = o.visibilityLocked(name, cell)
		o.mu.Unlock()
		return
	}
}

// forkWithField returns a copy-on-write fork of obj with name set to value,
// leaving obj itself untouched — the same result `self.x = v` produces in
// evalFieldAssignment. The caller installs the result as the new `self`.
func forkWithField(obj *Object, name string, value Value) *Object {
	vis := obj.Visibility(name)
	next := obj.cowCopy()
	next.bindOverPending(name, value, vis)
	return next
}

// bindOverPending binds name on m, superseding any pending initializer m holds
// for it (Lookup prefers a pending entry over a value). Use it to write a field
// on a frame fresh from cowCopy, whose pending entries are copies of its
// source's.
func (m *Object) bindOverPending(name string, value Value, vis Visibility) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Pending, name)
	m.Values[name] = value
	m.Visibilities[name] = vis
}

// cowCopy returns a fresh copy of m for a copy-on-write write to bind on,
// leaving m itself untouched. Every field write — bare (forkWithField) or
// through a path (`a.x = v`, `self.a.b = v`, see evalFieldAssignment) — forks
// through here.
//
// Deriving a child frame per write would make a loop of writes
// (`xs.each { x => total += x }`, or `a.x += 1` in a `loop`) build a chain as
// deep as its iteration count, and every read of a field the loop doesn't
// write — and every Bindings/MarshalJSON/equality walk — would walk all of it:
// quadratic, and every superseded generation would stay reachable. So when m
// is itself a copy-on-write frame (it has a parent), its frame is copied
// instead, with the new generation as m's sibling: chains stay as deep as
// they were, and a write costs one copy of the fields written so far.
func (m *Object) cowCopy() *Object {
	if m.Parent == nil {
		return m.Derive(false).(*Object)
	}
	m.mu.Lock()
	next := &Object{
		Mod:          m.Mod,
		Values:       maps.Clone(m.Values),
		Visibilities: maps.Clone(m.Visibilities),
		Pending:      maps.Clone(m.Pending),
		Parent:       m.Parent,
		IsForked:     m.IsForked,
		dynamicScope: m.dynamicScope,
	}
	m.mu.Unlock()
	if next.Values == nil {
		next.Values = make(map[string]Value)
	}
	if next.Visibilities == nil {
		next.Visibilities = make(map[string]Visibility)
	}
	return next
}

// resolvesToSelfField reports whether name, resolved starting at m, is a field
// of the `self` held in cell — reached through a selfParent frame (see
// nextFrame) — rather than a local or argument bound in a frame before it.
func (m *Object) resolvesToSelfField(name string, cell *DynamicScope) bool {
	for f := m; f != nil; {
		f.mu.Lock()
		_, has := f.Values[name]
		_, pending := f.Pending[name]
		f.mu.Unlock()
		if has || pending {
			return false
		}
		next, isSelf := f.nextFrame(cell)
		if isSelf {
			return next.hasFrom(name, cell) || declaresField(next, name)
		}
		f = next
	}
	return false
}

// newSelfFrame returns a frame for a body executing with self as its receiver
// (a method call, see BoundMethod.Call): a fresh, unshared `self` cell holding
// self, and a selfParent link so that bare names resolve through whatever
// generation of `self` the cell holds. Bind the body's arguments in it and
// Derive the body's scopes from it.
func newSelfFrame(self *Object) *Object {
	frame := self.Derive(false).(*Object)
	frame.selfParent = true
	frame.EnterSelf(self)
	return frame
}

// inMethodBody reports whether scope lies inside a method or new() body, i.e.
// its frames reach a method's newSelfFrame (or a constructor scope) before any
// `self` value. Functions declared there are nested functions: they close over
// the body's locals and `self` (see FunctionValue.Nested).
func inMethodBody(scope ValueScope) bool {
	switch s := scope.(type) {
	case *ConstructorScope:
		return true
	case OverlayValueScope:
		return inMethodBody(s.primary)
	case recordFieldScope:
		// A record literal field: the record itself is never a method body,
		// but the scope it was written in may be.
		return inMethodBody(s.lexical)
	case *Object:
		for f := s; f != nil; f = f.Parent {
			if f.selfParent {
				return true
			}
		}
	}
	return false
}

// Self returns the dynamic scope value ('self')
func (m *Object) Self() (Value, bool) {
	for o := m; o != nil; o = o.Parent {
		if o.dynamicScope != nil {
			if v := o.dynamicScope.get(); v != nil {
				return v, true
			}
		}
	}
	return nil, false
}

// MutateSelf updates the dynamic scope value ('self') in the
// existing shared cell, or creates a new cell if none exists.
func (m *Object) MutateSelf(value Value) {
	if m.dynamicScope != nil {
		m.dynamicScope.set(value)
	} else {
		m.dynamicScope = newDynamicScope(value)
	}
}

// EnterSelf creates a fresh, unshared dynamic scope cell.
// Use this when establishing a new self (e.g. entering a method body)
// rather than updating self within the current scope.
func (m *Object) EnterSelf(value Value) {
	m.dynamicScope = newDynamicScope(value)
}

// MarshalJSON implements json.Marshaler for Object
// Includes private fields, so that state can be retained
func (m *Object) MarshalJSON() ([]byte, error) {
	result := make(map[string]Value)
	for _, kv := range m.Bindings(PrivateVisibility) {
		if _, isFn := kv.Value.(FunctionValue); isFn {
			continue
		}
		result[kv.Key] = kv.Value
	}
	return json.Marshal(result)
}

// BoundMethod represents a method bound to a specific receiver
type BoundMethod struct {
	Method   FunctionValue
	Receiver ValueScope
}

func (b BoundMethod) Type() hm.Type {
	return b.Method.Type()
}

func (b BoundMethod) String() string {
	return fmt.Sprintf("bound_method(%s)", b.Method.String())
}

func (b BoundMethod) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("cannot marshal bound method value")
}

func (b BoundMethod) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	// A block keeps its enclosing method's `self`; a function referenced by
	// bare name (`b.hook = &helper`, later called as `b.hook(x)`) keeps the
	// `self` it was referenced under, and a nested function the `self` of the
	// method it was declared in. None of them rebinds to this receiver.
	if b.Method.IsBlockArg || b.Method.Nested || b.Method.SelfScope != nil {
		return b.Method.Call(ctx, scope, args)
	}

	frame, err := enterCall(ctx)
	if err != nil {
		return nil, err
	}

	// Create a composite environment that includes both the receiver and the method's closure
	var fnScope OverlayValueScope
	if obj, ok := b.Receiver.(*Object); ok {
		// Bare field reads and writes resolve through the method's `self`
		// cell (see newSelfFrame), so they track self's copy-on-write
		// generations exactly like `self.x`.
		fnScope = CreateOverlayValueScope(newSelfFrame(obj.snapshotReceiver()), b.Method.Closure)
	} else {
		recv := b.Receiver.Derive(true)
		fnScope = CreateOverlayValueScope(recv.Derive(false), b.Method.Closure)
		fnScope.EnterSelf(recv)
	}

	returnFrame := NewControlFrame(ReturnFrame)
	frame.returnFrame = returnFrame
	methodCtx := withFrame(ctx, frame)

	if err := b.Method.BindArgs(methodCtx, fnScope, args); err != nil {
		return nil, err
	}

	val, err := EvalNode(methodCtx, fnScope, b.Method.Body)
	if err != nil {
		if returnVal, ok := returnValueFromError(err, returnFrame); ok {
			return returnVal, nil
		}
		return nil, err
	}
	return val, nil
}

func (b BoundMethod) ParameterNames() []string {
	return b.Method.Args
}

func (b BoundMethod) IsAutoCallable() bool {
	return b.Method.IsAutoCallable()
}

// BoundScalarMethod is a Dang-defined scalar method bound to its receiver
// value. Scalar receivers are plain values, not scopes, so unlike BoundMethod
// the receiver enters the body only as `self`; bare names resolve through the
// method's closure, which overlays the scalar's sibling methods.
type BoundScalarMethod struct {
	Method   FunctionValue
	Receiver Value
}

func (b BoundScalarMethod) Type() hm.Type {
	return b.Method.Type()
}

func (b BoundScalarMethod) String() string {
	return fmt.Sprintf("bound_scalar_method(%s)", b.Method.String())
}

func (b BoundScalarMethod) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("cannot marshal bound method value")
}

func (b BoundScalarMethod) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	frame, err := enterCall(ctx)
	if err != nil {
		return nil, err
	}

	// EnterSelf installs a fresh, unshared cell on the derived scope, so the
	// (possibly process-shared) closure's own cell is never touched.
	fnScope := b.Method.Closure.Derive(false)
	fnScope.EnterSelf(b.Receiver)

	returnFrame := NewControlFrame(ReturnFrame)
	frame.returnFrame = returnFrame
	methodCtx := withFrame(ctx, frame)

	if err := b.Method.BindArgs(methodCtx, fnScope, args); err != nil {
		return nil, err
	}

	val, err := EvalNode(methodCtx, fnScope, b.Method.Body)
	if err != nil {
		if returnVal, ok := returnValueFromError(err, returnFrame); ok {
			return returnVal, nil
		}
		return nil, err
	}
	return val, nil
}

func (b BoundScalarMethod) ParameterNames() []string {
	return b.Method.Args
}

func (b BoundScalarMethod) IsAutoCallable() bool {
	return b.Method.IsAutoCallable()
}

// BoundBuiltinMethod represents a builtin method bound to a primitive value (like StringValue)
type BoundBuiltinMethod struct {
	Method   BuiltinFunction
	Receiver Value
}

func (b BoundBuiltinMethod) Type() hm.Type {
	return b.Method.Type()
}

func (b BoundBuiltinMethod) String() string {
	return fmt.Sprintf("bound_builtin_method(%s)", b.Method.String())
}

func (b BoundBuiltinMethod) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("cannot marshal bound builtin method value")
}

func (b BoundBuiltinMethod) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	// Create a temporary environment with the receiver as dynamic scope
	tempType := NewType("_temp_", ObjectKind)
	tempScope := NewObject(tempType)
	tempScope.EnterSelf(b.Receiver)

	// Call the builtin function with the receiver context
	return b.Method.Call(ctx, tempScope, args)
}

func (b BoundBuiltinMethod) ParameterNames() []string {
	return b.Method.ParameterNames()
}

func (b BoundBuiltinMethod) IsAutoCallable() bool {
	return b.Method.IsAutoCallable()
}

// BuiltinFunction represents a builtin function like print
type BuiltinFunction struct {
	Name         string
	FnType       *hm.FunctionType
	CallFn       func(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error)
	AllDefaulted bool // true when every parameter has a default value
	// Deprecated, when non-empty, is the reason this builtin is deprecated.
	// FunCall.Eval emits a warning naming the call site whenever it is called.
	Deprecated string
}

func (b BuiltinFunction) Type() hm.Type {
	return b.FnType
}

func (b BuiltinFunction) String() string {
	return fmt.Sprintf("builtin:%s", b.Name)
}

func (b BuiltinFunction) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	return b.CallFn(ctx, scope, args)
}

func (b BuiltinFunction) ParameterNames() []string {
	if ft, ok := b.FnType.Arg().(*RecordType); ok {
		names := make([]string, len(ft.Fields))
		for i, field := range ft.Fields {
			names[i] = field.Key
		}
		return names
	}
	return nil
}

func (b BuiltinFunction) IsAutoCallable() bool {
	if b.FnType != nil && b.FnType.Block() != nil {
		return false
	}
	if b.AllDefaulted {
		return true
	}
	if rt, ok := b.FnType.Arg().(*RecordType); ok {
		return len(rt.Fields) == 0
	}
	return false
}

// ConstructorFunction represents a object constructor that evaluates the object body when called
type ConstructorFunction struct {
	Closure         ValueScope
	ObjectName      string
	Parameters      []*FieldDecl
	BlockParamName  string
	ObjectType      *Type
	FnType          *hm.FunctionType
	ObjectBodyForms []Node // Field declarations to evaluate (excluding NewConstructorDecl)
	NewBody         *Block // Explicit new() body, if present
}

func (c *ConstructorFunction) Type() hm.Type {
	return c.FnType
}

func (c *ConstructorFunction) String() string {
	return fmt.Sprintf("constructor:%s", c.ObjectName)
}

func (c *ConstructorFunction) Call(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
	frame, err := enterCall(ctx)
	if err != nil {
		return nil, err
	}
	frame.enterFunctionControlBoundary()
	ctx = withFrame(ctx, frame)

	// Create a new instance of the object
	instance := NewObject(c.ObjectType)

	instanceScope := CreateOverlayValueScope(instance, c.Closure)

	// Set dynamic scope to the instance so self is available
	instanceScope.EnterSelf(instance)

	if c.NewBody != nil {
		// Explicit new() constructor: evaluate field declarations that have
		// defaults, then execute the new() body (which must assign required fields).

		// Filter to only include FieldDecls with defaults and methods
		var formsWithDefaults []Node
		for _, form := range c.ObjectBodyForms {
			if field, ok := form.(*FieldDecl); ok {
				// Skip required fields without defaults — new() will set them
				if field.Value == nil {
					continue
				}
			}
			formsWithDefaults = append(formsWithDefaults, form)
		}

		_, err := EvaluateFormsWithPhases(ctx, formsWithDefaults, instanceScope)
		if err != nil {
			return nil, fmt.Errorf("evaluating object body for %s: %w", c.ObjectName, err)
		}

		// Bind constructor args so they shadow both instance fields and
		// the outer closure (see #23). Args go in a separate env that is
		// only consulted for reads (Lookup), while writes (Bind/Update) go
		// to the instance as before.
		argScope := NewObject(NewType("_constructor_args_", ObjectKind))
		// Parameter defaults are scoped like field defaults: they read the
		// fields as the field defaults left them, and the earlier parameters,
		// which shadow fields. Each parameter gets its own layer over the
		// instance (and the closure behind it), so a closure created in a
		// default sees exactly the parameters before it, as it was checked.
		var defaultScope ValueScope = instanceScope
		for _, param := range c.Parameters {
			var val Value
			arg, found := args[param.Name.Name]
			if _, isNull := arg.(NullValue); found && isNull && param.Value != nil {
				// An explicit null falls back to the default, as for function
				// calls (FunctionValue.BindArgs).
				found = false
			}
			if found {
				val = arg
			} else if param.Value != nil {
				// Evaluate in defaultScope so earlier args are visible.
				defaultVal, err := EvalNode(ctx, defaultScope, param.Value)
				if err != nil {
					return nil, fmt.Errorf("evaluating default for constructor arg %q: %w", param.Name.Name, err)
				}
				val = defaultVal
			} else {
				val = NullValue{}
			}
			argScope.Bind(param.Name.Name, val, PrivateVisibility)
			layer := NewObject(NewType("_constructor_default_", ObjectKind))
			layer.Bind(param.Name.Name, val, PrivateVisibility)
			defaultScope = CreateOverlayValueScope(layer, defaultScope)
		}
		if c.BlockParamName != "" {
			blockVal := currentBlockArg(ctx)
			if blockVal == nil {
				return nil, fmt.Errorf("missing block argument for constructor %s", c.ObjectName)
			}
			argScope.Bind(c.BlockParamName, blockVal, PrivateVisibility)
		}

		// Execute the new() body with access to self and constructor args.
		// We evaluate forms directly (not via Block.Eval) so constructor-body
		// `let`s stay in newBodyScope's locals. The instance itself lives in
		// newBodyScope's `self` cell: every field write in the body (bare or
		// `self.field = ...`, at any nesting depth) installs a new
		// copy-on-write generation there, so `instance` — whose field
		// defaults were evaluated above — is never written in place.
		newBodyScope := CreateConstructorScope(instance, argScope, c.Closure)
		returnFrame := NewControlFrame(ReturnFrame)
		frame.returnFrame = returnFrame
		newBodyCtx := withFrame(ctx, frame)

		var lastVal Value
		for _, form := range c.NewBody.Forms {
			lastVal, err = EvalNode(newBodyCtx, newBodyScope, form)
			if err != nil {
				if returnVal, ok := returnValueFromError(err, returnFrame); ok {
					lastVal = returnVal
					break
				}
				return nil, fmt.Errorf("evaluating new() for %s: %w", c.ObjectName, err)
			}
		}

		// The latest generation of self is the instance by default.
		if updatedInstance, found := newBodyScope.Self(); found {
			if obj, ok := updatedInstance.(*Object); ok {
				instance = obj
			}
		}

		// The new() body returns self as its last expression, just like a
		// normal function. This is enforced at inference time. Using the
		// last value (rather than the tracked instance) ensures method
		// chains like self.withFoo() propagate their changes (see #24).
		if mv, ok := lastVal.(*Object); ok {
			instance = mv
		}

		// Check that all non-null fields have been assigned
		if err := c.checkRequiredFields(ctx, instance); err != nil {
			return nil, err
		}
	} else {
		// Field-derived constructor: bind provided arguments on the instance,
		// then evaluate field declarations.
		for _, param := range c.Parameters {
			if arg, found := args[param.Name.Name]; found {
				instanceScope.Bind(param.Name.Name, arg, param.Visibility)
			}
		}

		_, err := EvaluateFormsWithPhases(ctx, c.ObjectBodyForms, instanceScope)
		if err != nil {
			return nil, fmt.Errorf("evaluating object body for %s: %w", c.ObjectName, err)
		}
	}

	return instance, nil
}

// checkRequiredFields verifies that all non-null fields have been assigned.
// By the time this runs, the object body's pending initializers have all been
// forced; Get walks the instance's parent chain to cover fields placed on
// earlier copy-on-write generations (each `self.f = ...` clones the instance).
func (c *ConstructorFunction) checkRequiredFields(ctx context.Context, instance *Object) error {
	for name, scheme := range c.ObjectType.Bindings(PrivateVisibility) {
		fieldType, _ := scheme.Type()
		if _, isNonNull := fieldType.(hm.NonNullType); isNonNull {
			val, found, err := instance.Lookup(ctx, name)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("new() for %s: required field %q was not assigned", c.ObjectName, name)
			}
			if _, isNull := val.(NullValue); isNull {
				return fmt.Errorf("new() for %s: required field %q was not assigned", c.ObjectName, name)
			}
		}
	}
	return nil
}

func (c *ConstructorFunction) ParameterNames() []string {
	names := make([]string, len(c.Parameters))
	for i, param := range c.Parameters {
		names[i] = param.Name.Name
	}
	return names
}

func (c *ConstructorFunction) IsAutoCallable() bool {
	if c.BlockParamName != "" || (c.FnType != nil && c.FnType.Block() != nil) {
		return false
	}

	for _, param := range c.Parameters {
		if param.Value == nil {
			// No default value, so this is a required parameter
			return false
		}
	}
	// All parameters have default values, so constructor can be auto-called
	return true
}

func RunFile(ctx context.Context, filePath string, debug bool) error {
	// Ensure service registry exists for cleanup
	ctx, services := ensureServiceRegistry(ctx)
	if services != nil {
		defer services.StopAll()
	}

	// Load project config (dang.toml) if not already in context
	ctx, err := ensureProjectImports(ctx, filepath.Dir(filePath))
	if err != nil {
		// Don't wrap SourceErrors — they already carry full context
		var sourceErr *SourceError
		if errors.As(err, &sourceErr) {
			return err
		}
		return fmt.Errorf("loading project config: %w", err)
	}

	// Read the source file for error reporting
	sourceBytes, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}
	source := string(sourceBytes)

	// Create evaluation context for enhanced error reporting
	evalCtx := NewEvalContext(filePath, source)

	parsed, err := ParseFileWithRecovery(filePath, GlobalStore("filePath", filePath))
	if err != nil {
		return err
	}

	node := parsed.(*FileBlock)

	// Inject auto-imports for any import configs in context
	node.Forms = InjectAutoImports(ctx, node.Forms)

	if debug {
		_, _ = pretty.Println(node)
	}

	typeScope := NewPreludeTypeScope("")

	// Tag this program as its own module so `let` members declared in it are
	// private to it (and prelude `let` members stay invisible here). See
	// moduleScope.
	ctx = withModuleScope(ctx, &moduleScope{label: "file:" + filePath})

	inferred, err := Infer(ctx, typeScope, node, true)
	if err != nil {
		// Convert InferError to SourceError with full context
		return ConvertInferError(err)
	}

	slog.Debug("type inference completed", "type", inferred)

	// Now evaluate the program
	valueScope := NewValueScope(typeScope)

	result, err := EvalNodeWithContext(ctx, valueScope, node, evalCtx)
	if err != nil {
		if translated, ok := translateBoundaryEvalError(err, evalCtx); ok {
			return translated
		}
		return fmt.Errorf("evaluation error: %w", err)
	}

	slog.Debug("evaluation completed", "result", result.String())

	return nil
}

func translateBoundaryEvalError(err error, evalCtx *EvalContext) (error, bool) {
	// If it's already a SourceError, don't wrap it again.
	var sourceErr *SourceError
	if errors.As(err, &sourceErr) {
		return err, true
	}

	source := ""
	if evalCtx != nil {
		source = evalCtx.Source
	}

	var returned *ReturnException
	if errors.As(err, &returned) {
		return NewSourceError(
			errors.New(returned.Error()),
			returned.Location,
			source,
		), true
	}

	var broken *BreakException
	if errors.As(err, &broken) {
		return NewSourceError(
			errors.New(broken.Error()),
			broken.Location,
			source,
		), true
	}

	var continued *ContinueException
	if errors.As(err, &continued) {
		return NewSourceError(
			errors.New(continued.Error()),
			continued.Location,
			source,
		), true
	}

	// Surface uncaught raise errors with the full report: raise site,
	// public data fields, cause chain, and concurrent sibling failures.
	var raised *RaisedError
	if errors.As(err, &raised) {
		report := &uncaughtErrorReport{Raised: raised, Source: source}
		var parallel *parallelFailure
		if errors.As(err, &parallel) {
			report.Also = parallel.Also
		}
		return report, true
	}

	return nil, false
}

// ensureServiceRegistry adds a ServiceRegistry to the context if one isn't
// already present. Returns the new context and the registry (nil if one was
// already present, meaning the caller shouldn't defer StopAll).
func ensureServiceRegistry(ctx context.Context) (context.Context, *ServiceRegistry) {
	if servicesFromContext(ctx) != nil {
		return ctx, nil // already have one, caller is not responsible
	}
	services := &ServiceRegistry{}
	return ContextWithServices(ctx, services), services
}

// ensureProjectImports discovers dang.toml and merges its import configs
// into the context, without overriding any configs already set (e.g. by
// the Dagger SDK entrypoint).
func ensureProjectImports(ctx context.Context, dir string) (context.Context, error) {
	// Skip if project config or import configs are already loaded
	if _, cfg := projectConfigFromContext(ctx); cfg != nil {
		return ctx, nil
	}
	if len(importConfigsFromContext(ctx)) > 0 {
		return ctx, nil
	}

	configPath, config, err := FindProjectConfig(dir)
	if err != nil {
		return ctx, fmt.Errorf("finding dang.toml: %w", err)
	}
	if config == nil {
		return ctx, nil
	}

	configDir := filepath.Dir(configPath)
	ctx = ContextWithProjectConfig(ctx, configPath, config)

	resolved, err := ResolveImportConfigs(ctx, config, configDir)
	if err != nil {
		return ctx, err
	}

	// Resolve Dagger import (from dang.toml or auto-detected from Dagger module config)
	resolved = ResolveDaggerImport(ctx, resolved, dir)

	// Merge: project configs go first, then any existing context configs
	// (existing configs take priority by name in loadImportConfig)
	existing := importConfigsFromContext(ctx)

	// Deduplicate: existing configs override project configs
	existingNames := make(map[string]bool)
	for _, c := range existing {
		existingNames[c.Name] = true
	}
	var merged []ImportConfig
	for _, c := range resolved {
		if !existingNames[c.Name] {
			merged = append(merged, c)
		}
	}
	merged = append(merged, existing...)

	if len(merged) > 0 {
		ctx = ContextWithImportConfigs(ctx, merged...)
	}
	return ctx, nil
}

// InjectAutoImports prepends synthetic ImportDecl nodes for any import configs
// in the context that aren't already explicitly imported by the user's code.
func InjectAutoImports(ctx context.Context, forms []Node) []Node {
	configs := importConfigsFromContext(ctx)
	if len(configs) == 0 {
		return forms
	}

	// Collect names that are already imported
	imported := make(map[string]bool)
	for _, form := range forms {
		if imp, ok := form.(*ImportDecl); ok && imp.Name != nil {
			imported[imp.Name.Name] = true
		}
	}

	// Prepend synthetic imports for any auto-import configs not already present
	var injected []Node
	for _, config := range configs {
		if config.AutoImport && !imported[config.Name] {
			injected = append(injected, &ImportDecl{
				Name: &Symbol{Name: config.Name},
			})
		}
	}

	if len(injected) == 0 {
		return forms
	}

	return append(injected, forms...)
}

// parseDirBlocks parses every .dang file in dirPath into a *FileBlock and
// returns them in deterministic order. Files keep their own forms so that
// downstream callers can apply file-scoped policy (e.g. file-local imports).
func parseDirBlocks(ctx context.Context, dirPath string) (context.Context, []*FileBlock, error) {
	ctx, err := ensureProjectImports(ctx, dirPath)
	if err != nil {
		return ctx, nil, fmt.Errorf("loading project config: %w", err)
	}

	dangFiles, err := filepath.Glob(filepath.Join(dirPath, "*.dang"))
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to find .dang files in directory %s: %w", dirPath, err)
	}

	if len(dangFiles) == 0 {
		return ctx, nil, nil
	}

	sort.Strings(dangFiles)

	blocks := make([]*FileBlock, 0, len(dangFiles))
	for _, filePath := range dangFiles {
		parsed, err := parseFileWithSyntaxCache(ctx, filePath)
		if err != nil {
			return ctx, nil, fmt.Errorf("failed to parse file %s: %w", filePath, err)
		}
		block, ok := parsed.(*FileBlock)
		if !ok {
			return ctx, nil, fmt.Errorf("parsed result for %s is not a FileBlock", filePath)
		}
		blocks = append(blocks, block)
	}

	return ctx, blocks, nil
}

// DeclareDir declares all .dang files in a directory as a single module and
// returns an evaluation environment containing only declared API-shape values.
// It resolves imports and signatures, but it does not infer or evaluate
// function bodies, computed variables, or non-declaration expressions. Each
// file's imports are file-local: a sibling file's `import X` does not make X
// visible here.
func DeclareDir(ctx context.Context, dirPath string, isDebug bool) (ValueScope, error) {
	// Ensure service registry exists for cleanup.
	ctx, services := ensureServiceRegistry(ctx)
	if services != nil {
		defer services.StopAll()
	}

	ctx, blocks, err := parseDirBlocks(ctx, dirPath)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return NewValueScope(NewPreludeTypeScope("")), nil
	}

	if isDebug {
		var totalForms int
		for _, b := range blocks {
			totalForms += len(b.Forms)
		}
		fmt.Printf("Declaring directory: %s\n", dirPath)
		fmt.Printf("Found %d .dang files with %d total forms\n", len(blocks), totalForms)
	}

	typeScope := NewPreludeTypeScope("")
	fresh := hm.NewSimpleFresher()
	if err := declareDirectoryFiles(ctx, blocks, typeScope, fresh); err != nil {
		return nil, ConvertInferError(err)
	}

	valueScope := NewValueScope(typeScope)
	for _, block := range blocks {
		if _, err := EvaluateDeclaredFormsWithPhases(ctx, block.Forms, valueScope); err != nil {
			return nil, err
		}
	}

	return valueScope, nil
}

// declareDirectoryFiles runs only the declaration phases across files (no body
// inference). It is the DeclareDir counterpart to InferDirectoryFiles. Mutates
// block.Forms to prepend auto-imports so later evaluation reuses the same
// *ImportDecl nodes inferred here.
func declareDirectoryFiles(ctx context.Context, files []*FileBlock, dirScope TypeScope, fresh hm.Fresher) error {
	overall := &InferenceErrors{}
	scopes := prepareFileScopes(ctx, files, dirScope, fresh, overall)
	runDirectoryPhases(ctx, scopes, fresh, overall, declarationPhases)

	if overall.HasErrors() {
		return overall
	}
	return nil
}

// RunDir evaluates all .dang files in a directory as a single module. Each
// file's imports are file-local during type inference: a sibling file's
// `import X` does not make X visible here.
func RunDir(ctx context.Context, dirPath string, isDebug bool) (ValueScope, error) {
	// Ensure service registry exists for cleanup
	ctx, services := ensureServiceRegistry(ctx)
	if services != nil {
		defer services.StopAll()
	}

	ctx, blocks, err := parseDirBlocks(ctx, dirPath)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no .dang files found in directory: %s", dirPath)
	}

	if isDebug {
		var totalForms int
		for _, b := range blocks {
			totalForms += len(b.Forms)
		}
		fmt.Printf("Evaluating directory: %s\n", dirPath)
		fmt.Printf("Found %d .dang files with %d total forms\n", len(blocks), totalForms)
	}

	typeScope := NewPreludeTypeScope("")
	fresh := hm.NewSimpleFresher()

	// All files in the directory share one module identity, so a `let` member
	// declared in any file is reachable from its siblings but not from another
	// module (or from user code touching a prelude type). See moduleScope.
	ctx = withModuleScope(ctx, &moduleScope{label: "dir:" + dirPath})

	if isDebug {
		fmt.Println("Running phased inference...")
	}

	if err := InferDirectoryFiles(ctx, blocks, typeScope, fresh); err != nil {
		return nil, ConvertInferError(err)
	}

	slog.Debug("directory type inference completed", "dir", dirPath)

	valueScope := NewValueScope(typeScope)

	if isDebug {
		fmt.Println("Running phased evaluation...")
	}

	evalCtx := NewEvalContext(dirPath, "")
	ctx = WithEvalContext(ctx, evalCtx)

	// Per-file evaluation matches type inference: each file's imports populate
	// a fresh per-file env, composed with the shared valueScope so cross-file
	// declarations stay visible to siblings while imported names don't leak.
	if err := evaluateDirectoryFiles(ctx, blocks, valueScope); err != nil {
		if translated, ok := translateBoundaryEvalError(err, evalCtx); ok {
			return nil, translated
		}
		return nil, err
	}

	slog.Debug("directory evaluation completed", "dir", dirPath)

	return valueScope, nil
}

// evaluateDirectoryFiles evaluates each file's forms with file-local imports
// composed over the shared valueScope, mirroring InferDirectoryFiles' per-file
// scoping. Phases run in lockstep across files so cross-file references resolve
// regardless of file order.
//
// Imports are file-scoped regardless of file count: even a single-file directory
// installs its imports into a per-file env rather than the returned valueScope,
// so `import` consistently means "this file imports X" and imported symbols never
// leak into the module scope. Each declaration still captures its file's composite
// scope (e.g. via ConstructorFunction.Closure), so directive arguments and method
// bodies that reference imported symbols continue to resolve.
func evaluateDirectoryFiles(ctx context.Context, blocks []*FileBlock, valueScope ValueScope) error {
	type fileEvalScope struct {
		classified ClassifiedForms
		scope      ValueScope
	}

	fileScopes := make([]fileEvalScope, 0, len(blocks))
	for _, block := range blocks {
		imports, rest := partitionImports(block.Forms)

		// Install imports into a fresh per-file env. Using Derive (rather than
		// a sibling Object) keeps the prelude reachable for any lookups
		// the imports phase performs during installation.
		importsScope := valueScope.Derive(true)
		for _, imp := range imports {
			if _, err := EvalNode(ctx, importsScope, imp); err != nil {
				return err
			}
		}

		composite := CreateOverlayValueScope(valueScope, importsScope)
		fileScopes = append(fileScopes, fileEvalScope{
			classified: classifyForms(rest),
			scope:      composite,
		})
	}

	// Phase order matches EvaluateFormsWithPhases (minus imports, which we
	// already ran per file above): directives, constants, types, functions,
	// variables (as lazy fields), then non-declarations.
	for _, fs := range fileScopes {
		for _, form := range fs.classified.Directives {
			if _, err := EvalNode(ctx, fs.scope, form); err != nil {
				return fmt.Errorf("directive evaluation failed: %w", err)
			}
		}
	}
	for _, fs := range fileScopes {
		for _, form := range fs.classified.Constants {
			if _, err := EvalNode(ctx, fs.scope, form); err != nil {
				return fmt.Errorf("constant evaluation failed: %w", err)
			}
		}
	}
	for _, fs := range fileScopes {
		for _, form := range fs.classified.Types {
			if _, err := EvalNode(ctx, fs.scope, form); err != nil {
				return fmt.Errorf("type evaluation failed: %w", err)
			}
		}
	}
	for _, fs := range fileScopes {
		for _, form := range fs.classified.Functions {
			if _, err := EvalNode(ctx, fs.scope, form); err != nil {
				return fmt.Errorf("function evaluation failed: %w", err)
			}
		}
	}
	// Variables phase across files: install all lazy fields first, then force.
	// Without the split, file order would dictate eval order — a consumer file
	// would force its body before a producer's `pub make = ...` had even been
	// bound, so cross-file `make` lookups would fail.
	for _, fs := range fileScopes {
		scope := fs.scope
		for _, form := range fs.classified.Variables {
			field, ok := form.(*FieldDecl)
			if !ok {
				continue
			}
			scope.BindLazy(field.Name.Name, func(ctx context.Context) (Value, error) {
				return EvalNode(ctx, scope, field.Value)
			}, field.Visibility)
		}
	}
	for _, fs := range fileScopes {
		for _, form := range fs.classified.Variables {
			field, ok := form.(*FieldDecl)
			if !ok {
				continue
			}
			if _, _, err := fs.scope.Lookup(ctx, field.Name.Name); err != nil {
				return fmt.Errorf("variable evaluation failed: %w", err)
			}
		}
	}
	for _, fs := range fileScopes {
		for _, form := range fs.classified.NonDeclarations {
			if _, err := EvalNode(ctx, fs.scope, form); err != nil {
				return fmt.Errorf("non-declaration evaluation failed: %w", err)
			}
		}
	}

	return nil
}

// EvalNode evaluates any AST node (legacy interface)
func EvalNode(ctx context.Context, scope ValueScope, node Node) (Value, error) {
	ctx, frame := ensureFrame(ctx)
	return evalNode(ctx, scope, node, frame.evalCtx)
}

// EvalNodeWithContext evaluates any AST node with enhanced error reporting
func EvalNodeWithContext(ctx context.Context, scope ValueScope, node Node, evalCtx *EvalContext) (Value, error) {
	// Store the eval context in the Go context for evaluators to access
	if evalCtx != nil {
		ctx = WithEvalContext(ctx, evalCtx)
	} else {
		ctx, _ = ensureFrame(ctx)
	}
	return evalNode(ctx, scope, node, evalCtx)
}

// evalNode evaluates node under ctx's frame, reporting errors against evalCtx.
// It never changes the frame itself: evaluating a node costs no context layer.
func evalNode(ctx context.Context, scope ValueScope, node Node, evalCtx *EvalContext) (Value, error) {
	if evaluator, ok := node.(Evaluator); ok {
		val, err := evaluator.Eval(ctx, scope)
		if err != nil {
			// Let control-flow sentinel errors propagate unwrapped so their
			// nearest runtime boundary can intercept them cleanly.
			var raised *RaisedError
			if errors.As(err, &raised) || isControlFlowException(err) {
				return nil, err
			}
			if evalCtx != nil {
				return nil, evalCtx.CreateSourceError(err, node)
			} else {
				return nil, err
			}
		}
		if val == nil {
			return nil, fmt.Errorf("Evaluator(%T) returned nil", node)
		}
		return val, nil
	}

	// Fallback for nodes that don't implement Evaluator directly
	switch n := node.(type) {
	case *String:
		return StringValue{Val: n.Value}, nil
	case *Int:
		return IntValue{Val: int(n.Value)}, nil
	case *Boolean:
		return BoolValue{Val: n.Value}, nil
	case *Null:
		return NullValue{}, nil
	default:
		err := fmt.Errorf("evaluation not implemented for node type %T", node)
		if evalCtx != nil {
			return nil, evalCtx.CreateSourceError(err, node)
		}
		return nil, err
	}
}
