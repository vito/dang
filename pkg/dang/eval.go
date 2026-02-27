package dang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/kr/pretty"

	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/querybuilder"
)

// Context key for passing block arguments
type contextKey string

const blockArgContextKey contextKey = "blockArg"

// Value represents a runtime value in the Dang language
type Value interface {
	Type() hm.Type
	String() string
}

// Evaluator defines the interface for evaluating AST nodes
type Evaluator interface {
	Eval(ctx context.Context, env EvalEnv) (Value, error)
}

// Callable defines the interface for all function-like values that can be called
type Callable interface {
	Value
	Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error)
	ParameterNames() []string
	IsAutoCallable() bool
}

// EvalEnv represents the evaluation environment
type EvalEnv interface {
	Value
	Get(name string) (Value, bool)
	GetLocal(name string) (Value, bool)
	Bindings(Visibility) []Keyed[Value]
	Set(name string, value Value) EvalEnv
	SetWithVisibility(name string, value Value, visibility Visibility)
	Reassign(name string, value Value)
	Visibility(name string) Visibility
	Clone() EvalEnv
	Fork() EvalEnv
	// Dynamic scope support for 'self'
	GetDynamicScope() (Value, bool)
	SetDynamicScope(value Value)
}

// InputObjectConstructor creates a ModuleValue from named arguments,
// used for GraphQL input types like UserSort(field: ..., direction: ...).
type InputObjectConstructor struct {
	TypeName string
	TypeEnv  *Module
	FnType   *hm.FunctionType
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

func (c InputObjectConstructor) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	instance := NewModuleValue(c.TypeEnv)
	for name, val := range args {
		instance.Set(name, val)
	}
	return instance, nil
}

// GraphQLFunction represents a GraphQL API function that makes actual calls
type GraphQLFunction struct {
	Name       string
	TypeName   string
	Field      *introspection.Field
	FnType     *hm.FunctionType
	Client     graphql.Client
	Schema     *introspection.Schema
	TypeEnv    Env                     // Type environment for looking up enum types
	QueryChain *querybuilder.Selection // Keep track of the query chain built so far
	IsMutation bool                    // True if this is a mutation field
}

func (g GraphQLFunction) Type() hm.Type {
	return g.FnType
}

func (g GraphQLFunction) String() string {
	return fmt.Sprintf("gql:%s.%s", g.TypeName, g.Name)
}

func (g GraphQLFunction) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
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

		// Check if the return type is an enum
		if isEnumType(g.Field.TypeRef, g.Schema) {
			// Convert string result to EnumValue
			if strVal, ok := result.(string); ok {
				enumTypeName := getTypeName(g.Field.TypeRef)
				// Get the enum type from the type environment
				enumType, found := g.TypeEnv.NamedType(enumTypeName)
				if !found {
					return nil, fmt.Errorf("enum type %s not found", enumTypeName)
				}
				return EnumValue{
					Val:      strVal,
					EnumType: enumType,
				}, nil
			}
		}

		// Check if the return type is a custom scalar
		if isCustomScalarType(g.Field.TypeRef, g.Schema) {
			// Convert string result to ScalarValue
			if strVal, ok := result.(string); ok {
				scalarTypeName := getTypeName(g.Field.TypeRef)
				// Get the scalar type from the type environment
				scalarType, found := g.TypeEnv.NamedType(scalarTypeName)
				if !found {
					return nil, fmt.Errorf("scalar type %s not found", scalarTypeName)
				}
				return ScalarValue{
					Val:        strVal,
					ScalarType: scalarType,
				}, nil
			}
		}

		return ToValue(result)
	}

	// For non-scalar types, return a GraphQLValue that can be further selected
	return GraphQLValue{
		Name:       g.Name,
		TypeName:   getTypeName(g.Field.TypeRef),
		Field:      g.Field,
		ValType:    g.FnType.Ret(false),
		Client:     g.Client,
		Schema:     g.Schema,
		TypeEnv:    g.TypeEnv,    // Pass along the type environment
		QueryChain: query,        // Pass the query chain for further building
		IsMutation: g.IsMutation, // Propagate mutation flag
	}, nil
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
	TypeEnv    Env                     // Type environment for looking up enum types
	QueryChain *querybuilder.Selection // Keep track of the query chain built so far
	IsMutation bool                    // True if this value came from a mutation
}

func (g GraphQLValue) Type() hm.Type {
	return g.ValType
}

func (g GraphQLValue) String() string {
	return fmt.Sprintf("gql:Value:%s.%s", g.TypeName, g.Name)
}

func (g GraphQLValue) MarshalJSON() ([]byte, error) {
	var id string
	err := g.QueryChain.Select("id").Client(g.Client).Bind(&id).Execute(context.TODO())
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
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
		argType, err := gqlToTypeNode(NewEnv(g.Schema), arg.TypeRef) // TOOD: NewEnv here is sus
		if err != nil {
			continue
		}
		args.Add(arg.Name, hm.NewScheme(nil, argType))
	}

	retType, err := gqlToTypeNode(NewEnv(g.Schema), field.TypeRef) // TOOD: NewEnv here is sus
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
		TypeEnv:    g.TypeEnv,    // Pass along the type environment
		QueryChain: g.QueryChain, // Pass the current query chain
		IsMutation: g.IsMutation, // Propagate mutation flag
	}, nil
}

// NewEvalEnv creates an evaluation environment with built-in functions
func NewEvalEnv(typeEnv Env) EvalEnv {
	// Create a ModuleValue from the type environment
	env := NewModuleValue(typeEnv)

	// Add builtin functions
	addBuiltinFunctions(env)

	return env
}

// NewEvalEnvWithSchema creates an evaluation environment populated with GraphQL API values
func NewEvalEnvWithSchema(typeEnv Env, client graphql.Client, schema *introspection.Schema) EvalEnv {
	// Create a ModuleValue from the type environment
	env := NewModuleValue(typeEnv)

	// Populate with GraphQL functions from the schema
	populateSchemaFunctions(env, typeEnv, client, schema)

	// Add builtin functions
	addBuiltinFunctions(env)

	return env
}

// populateSchemaFunctions adds GraphQL functions from a schema to an environment
func populateSchemaFunctions(env *ModuleValue, typeEnv Env, client graphql.Client, schema *introspection.Schema) {
	for _, t := range schema.Types {
		// Add enum values as enum constants for enum types
		if t.Kind == introspection.TypeKindEnum && len(t.EnumValues) > 0 {
			// Get the enum type environment
			enumTypeEnv, found := typeEnv.NamedType(t.Name)
			if !found {
				continue
			}

			// Create a module for the enum type
			enumModuleVal := NewModuleValue(enumTypeEnv)

			// Set each enum value as an EnumValue constant
			enumValues := make([]Value, len(t.EnumValues))
			for i, enumVal := range t.EnumValues {
				ev := EnumValue{
					Val:      enumVal.Name,
					EnumType: enumTypeEnv,
				}
				enumModuleVal.Set(enumVal.Name, ev)
				enumModuleVal.SetWithVisibility(enumVal.Name, ev, PublicVisibility)
				enumValues[i] = ev
			}

			// Add the values() method that returns all enum values as a list
			enumModuleVal.Set("values", ListValue{
				Elements: enumValues,
				ElemType: NonNull(enumTypeEnv),
			})

			// Add the enum module to the environment
			env.SetWithVisibility(t.Name, enumModuleVal, PublicVisibility)
		}

		// Add scalar types as available values for custom scalars
		if t.Kind == introspection.TypeKindScalar {
			// Skip built-in scalars (String, Int, Float, Boolean, ID)
			if t.Name == "String" || t.Name == "Int" || t.Name == "Float" || t.Name == "Boolean" || t.Name == "ID" {
				continue
			}

			// Get the scalar type environment
			scalarTypeEnv, found := typeEnv.NamedType(t.Name)
			if !found {
				continue
			}

			// Create a module for the scalar type (just a type placeholder)
			scalarModuleVal := NewModuleValue(scalarTypeEnv)

			// Add the scalar module to the environment
			env.SetWithVisibility(t.Name, scalarModuleVal, PublicVisibility)
		}

		// Add interface types as available values
		if t.Kind == introspection.TypeKindInterface {
			// Get the interface type environment
			interfaceTypeEnv, found := typeEnv.NamedType(t.Name)
			if !found {
				continue
			}

			// Create a module for the interface type (just a type placeholder)
			interfaceModuleVal := NewModuleValue(interfaceTypeEnv)

			// Add the interface module to the environment
			env.SetWithVisibility(t.Name, interfaceModuleVal, PublicVisibility)
		}

		// Add union types as available values
		if t.Kind == introspection.TypeKindUnion {
			unionTypeEnv, found := typeEnv.NamedType(t.Name)
			if !found {
				continue
			}

			unionModuleVal := NewModuleValue(unionTypeEnv)
			env.SetWithVisibility(t.Name, unionModuleVal, PublicVisibility)
		}

		// Add input object constructors: UserSort(field: ..., direction: ...)
		if t.Kind == introspection.TypeKindInputObject {
			inputTypeEnv, found := typeEnv.NamedType(t.Name)
			if !found {
				continue
			}

			args := NewRecordType("")
			for _, f := range t.InputFields {
				argType, err := gqlToTypeNode(typeEnv, f.TypeRef)
				if err != nil {
					continue
				}
				args.Add(f.Name, hm.NewScheme(nil, argType))
			}
			fnType := hm.NewFnType(args, NonNull(inputTypeEnv))

			constructor := InputObjectConstructor{
				TypeName: t.Name,
				TypeEnv:  inputTypeEnv.(*Module),
				FnType:   fnType,
			}
			env.SetWithVisibility(t.Name, constructor, PublicVisibility)
		}

		for _, f := range t.Fields {
			ret, err := gqlToTypeNode(typeEnv, f.TypeRef)
			if err != nil {
				continue
			}

			// This is a function - create a GraphQLFunction value
			args := NewRecordType("")
			for _, arg := range f.Args {
				argType, err := gqlToTypeNode(typeEnv, arg.TypeRef)
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
				TypeEnv:    typeEnv, // Pass the type environment
				QueryChain: nil,     // Top-level functions start with no query chain
			}

			// Add to environment if it's from the Query type
			if t.Name == schema.QueryType.Name {
				env.SetWithVisibility(f.Name, gqlFunc, PublicVisibility)
			}
		}

		// Collect mutation fields into a Mutation module
		if schema.MutationType != nil && t.Name == schema.MutationType.Name {
			mutTypeEnv, found := typeEnv.NamedType(t.Name)
			if !found {
				continue
			}
			mutModule := NewModuleValue(mutTypeEnv)
			for _, f := range t.Fields {
				ret, err := gqlToTypeNode(typeEnv, f.TypeRef)
				if err != nil {
					continue
				}
				args := NewRecordType("")
				for _, arg := range f.Args {
					argType, err := gqlToTypeNode(typeEnv, arg.TypeRef)
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
					TypeEnv:    typeEnv,
					QueryChain: nil,
					IsMutation: true,
				}
				mutModule.SetWithVisibility(f.Name, mutFunc, PublicVisibility)
			}
			env.SetWithVisibility("Mutation", mutModule, PublicVisibility)
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

func addBuiltinFunctions(env EvalEnv) {
	// Register all builtin functions
	ForEachFunction(func(def BuiltinDef) {
		fnType := createFunctionTypeFromDef(def)
		builtinFn := BuiltinFunction{
			Name:         def.Name,
			FnType:       fnType,
			AllDefaulted: allParamsDefaulted(def),
			CallFn: func(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
				// Apply defaults for missing arguments
				argsWithDefaults := applyDefaults(args, def)

				// Extract block from context if present
				var blockArg *FunctionValue
				if blockVal := ctx.Value(blockArgContextKey); blockVal != nil {
					if fnVal, ok := blockVal.(FunctionValue); ok {
						blockArg = &fnVal
					}
				}

				return def.Impl(ctx, nil, Args{Values: argsWithDefaults, Block: blockArg})
			},
		}
		env.Set(def.Name, builtinFn)
	})

	// Register all builtin methods with naming convention
	for _, receiverType := range []*Module{StringType, IntType, FloatType, BooleanType, ListTypeModule} {
		ForEachMethod(receiverType, func(def BuiltinDef) {
			fnType := createFunctionTypeFromDef(def)
			builtinFn := BuiltinFunction{
				Name:         def.Name,
				FnType:       fnType,
				AllDefaulted: allParamsDefaulted(def),
				CallFn: func(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
					selfVal, _ := env.GetDynamicScope()
					// Apply defaults for missing arguments
					argsWithDefaults := applyDefaults(args, def)

					// Extract block from context if present
					var blockArg *FunctionValue
					if blockVal := ctx.Value(blockArgContextKey); blockVal != nil {
						if fnVal, ok := blockVal.(FunctionValue); ok {
							blockArg = &fnVal
						}
					}

					return def.Impl(ctx, selfVal, Args{Values: argsWithDefaults, Block: blockArg})
				},
			}
			methodKey := GetMethodKey(receiverType, def.Name)
			env.Set(methodKey, builtinFn)
		})
	}

	// Register static methods on their host modules
	for _, hostModule := range StaticModules() {
		modValue := NewModuleValue(hostModule)
		ForEachStaticMethod(hostModule, func(def BuiltinDef) {
			fnType := createFunctionTypeFromDef(def)
			builtinFn := BuiltinFunction{
				Name:         def.Name,
				FnType:       fnType,
				AllDefaulted: allParamsDefaulted(def),
				CallFn: func(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
					argsWithDefaults := applyDefaults(args, def)
					return def.Impl(ctx, nil, Args{Values: argsWithDefaults})
				},
			}
			modValue.SetWithVisibility(def.Name, builtinFn, PublicVisibility)
		})

		// Populate nested enum types with their values
		for name, subEnv := range hostModule.NamedTypes() {
			subMod, ok := subEnv.(*Module)
			if !ok || subMod.Kind != EnumKind {
				continue
			}
			enumModValue := NewModuleValue(subMod)
			var enumValues []Value
			for varName, _ := range subMod.Bindings(PublicVisibility) {
				if varName == "values" {
					continue
				}
				ev := EnumValue{Val: varName, EnumType: subMod}
				enumModValue.SetWithVisibility(varName, ev, PublicVisibility)
				enumValues = append(enumValues, ev)
			}
			enumModValue.SetWithVisibility("values", ListValue{
				Elements: enumValues,
				ElemType: NonNull(subMod),
			}, PublicVisibility)
			modValue.SetWithVisibility(name, enumModValue, PublicVisibility)
		}

		env.Set(hostModule.Named, modValue)
	}
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

// Helper function to determine if a GraphQL type is an enum
func isEnumType(typeRef *introspection.TypeRef, schema *introspection.Schema) bool {
	// Unwrap NonNull and List wrappers
	currentType := typeRef
	for currentType.Kind == "NON_NULL" || currentType.Kind == "LIST" {
		currentType = currentType.OfType
	}

	// Check if it's an enum in the schema
	for _, t := range schema.Types {
		if t.Name == currentType.Name && t.Kind == introspection.TypeKindEnum {
			return true
		}
	}

	return false
}

// Helper function to determine if a GraphQL type is a custom scalar
func isCustomScalarType(typeRef *introspection.TypeRef, schema *introspection.Schema) bool {
	// Unwrap NonNull and List wrappers
	currentType := typeRef
	for currentType.Kind == "NON_NULL" || currentType.Kind == "LIST" {
		currentType = currentType.OfType
	}

	// Skip built-in scalars
	switch currentType.Name {
	case "String", "Int", "Float", "Boolean", "ID":
		return false
	}

	// Check if it's a custom scalar in the schema
	for _, t := range schema.Types {
		if t.Name == currentType.Name && t.Kind == introspection.TypeKindScalar {
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
	case BoolValue:
		return v.Val, nil
	case NullValue:
		return nil, nil
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
	case *ModuleValue:
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
		if v.Field.TypeRef.IsObject() {
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
	return m.val.TypeName + "ID"
}

// XXX_GraphqlID is an internal function. It returns the underlying type ID
func (m gqlObjectMarshaller) XXX_GraphQLID(ctx context.Context) (string, error) {
	var res string
	query := m.val.QueryChain.Select("id").Bind(&res).Client(m.val.Client)
	if err := query.Execute(ctx); err != nil {
		return "", err
	}
	return res, nil
}

func (m gqlObjectMarshaller) MarshalJSON() ([]byte, error) {
	id, err := m.XXX_GraphQLID(context.TODO())
	if err != nil {
		return nil, err
	}
	return json.Marshal(id)
}

// Runtime value implementations

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
	return json.Marshal(l.Elements)
}

// FunctionValue represents a function value
type FunctionValue struct {
	Args           []string
	Body           Node
	Closure        EvalEnv
	FnType         *hm.FunctionType
	Defaults       map[string]Node // Map of argument name to default value expression
	ArgDecls       []*SlotDecl     // Original argument declarations with directives
	BlockParamName string          // Name of the block parameter, if any
	Directives     []*DirectiveApplication
	IsDynamic      bool // True if this function has access to dynamic scope
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

func (f FunctionValue) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	fnEnv := f.Closure.Clone()

	if f.IsDynamic {
		// Propagate dynamic scope from calling environment
		//
		// A dynamic FunctionValue being called will ALWAYS mean we're coming from a
		// 'naked' self-call (foo() instead of self.foo()) from a sibling method, so
		// we can inherit the caller's `self`.
		if dynScope, hasDynScope := env.GetDynamicScope(); hasDynScope {
			fnEnv.SetDynamicScope(dynScope)
		}
	}

	if err := f.BindArgs(ctx, fnEnv, args); err != nil {
		return nil, err
	}

	return EvalNode(ctx, fnEnv, f.Body)
}

func (f FunctionValue) BindArgs(ctx context.Context, fnEnv EvalEnv, args map[string]Value) error {
	// Bind arguments to the function environment
	for _, argName := range f.Args {
		if val, exists := args[argName]; exists {
			// Handle null values with defaults
			if _, isNull := val.(NullValue); isNull {
				if defaultExpr, hasDefault := f.Defaults[argName]; hasDefault {
					// Evaluate in fnEnv so earlier args are visible to the default expression
					defaultVal, err := EvalNode(ctx, fnEnv, defaultExpr)
					if err != nil {
						return fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
					}
					fnEnv.Set(argName, defaultVal)
				} else {
					fnEnv.Set(argName, val)
				}
			} else {
				fnEnv.Set(argName, val)
			}
		} else if defaultExpr, hasDefault := f.Defaults[argName]; hasDefault {
			// Use default value when argument not provided.
			// Evaluate in fnEnv so earlier args are visible to the default expression.
			defaultVal, err := EvalNode(ctx, fnEnv, defaultExpr)
			if err != nil {
				return fmt.Errorf("evaluating default value for argument %q: %w", argName, err)
			}
			fnEnv.Set(argName, defaultVal)
		} else {
			fnEnv.Set(argName, NullValue{})
		}
	}

	// Bind block parameter if the function has one
	if f.FnType.Block() != nil {
		// Get block parameter name from function type
		// We need to store this when creating the FunctionValue
		blockParamName := f.BlockParamName
		if blockParamName != "" {
			// Extract block arg from context
			if blockVal := ctx.Value(blockArgContextKey); blockVal != nil {
				fnEnv.Set(blockParamName, blockVal.(Value))
			}
		}
	}

	return nil
}

func (f FunctionValue) ParameterNames() []string {
	return f.Args
}

func (f FunctionValue) IsAutoCallable() bool {
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

// ModuleValue represents a module value that implements EvalEnv
type ModuleValue struct {
	Mod          Env
	Values       map[string]Value
	Visibilities map[string]Visibility // Track visibility of each field
	Parent       *ModuleValue          // For hierarchical scoping
	IsForked     bool                  // Prevents SetInScope from traversing to parent
	dynamicScope Value                 // The value of 'self' in this scope
}

// NewModuleValue creates a new ModuleValue with an empty values map
func NewModuleValue(mod Env) *ModuleValue {
	return &ModuleValue{
		Mod:          mod,
		Values:       make(map[string]Value),
		Visibilities: make(map[string]Visibility),
		Parent:       nil,
	}
}

func (m *ModuleValue) Type() hm.Type {
	return hm.NonNullType{Type: m.Mod}
}

func (m *ModuleValue) String() string {
	return fmt.Sprintf("module %s", m.Mod)
}

func (m *ModuleValue) Get(name string) (Value, bool) {
	if val, ok := m.Values[name]; ok {
		return val, true
	}
	if m.Parent != nil {
		return m.Parent.Get(name)
	}
	return nil, false
}

func (m *ModuleValue) GetLocal(name string) (Value, bool) {
	val, ok := m.Values[name]
	return val, ok
}

func (m *ModuleValue) Set(name string, value Value) EvalEnv {
	// TODO: check the type, set it if not present?
	m.Values[name] = value
	m.Visibilities[name] = m.Visibility(name)
	return m
}

func (m *ModuleValue) Visibility(name string) Visibility {
	if vis, ok := m.Visibilities[name]; ok {
		return vis
	}
	if m.Parent != nil {
		return m.Parent.Visibility(name)
	}
	return PrivateVisibility
}

func (m *ModuleValue) Bindings(vis Visibility) []Keyed[Value] {
	var bindings []Keyed[Value]

	// Collect bindings from this module
	for name, value := range m.Values {
		if m.Visibilities[name] >= vis {
			bindings = append(bindings, Keyed[Value]{
				Key:        name,
				Value:      value,
				Positional: false, // Module bindings are never positional
			})
		}
	}

	// Collect bindings from parent (if any) - avoiding duplicates
	if m.Parent != nil {
		for _, binding := range m.Parent.Bindings(vis) {
			// Only include if not shadowed by current module
			if _, shadowed := m.Values[binding.Key]; !shadowed {
				bindings = append(bindings, binding)
			}
		}
	}

	return bindings
}

func (m *ModuleValue) Clone() EvalEnv {
	newValues := make(map[string]Value)
	newVisibilities := make(map[string]Visibility)
	return &ModuleValue{
		Mod:          m.Mod,
		Values:       newValues,
		Visibilities: newVisibilities,
		Parent:       m,
		dynamicScope: m.dynamicScope, // Preserve dynamic scope
	}
}

func (m *ModuleValue) Fork() EvalEnv {
	// Create shallow copy with fork boundary marker
	newValues := make(map[string]Value)
	newVisibilities := make(map[string]Visibility)
	return &ModuleValue{
		Mod:          m.Mod,
		Values:       newValues,
		Visibilities: newVisibilities,
		Parent:       m,
		IsForked:     true,           // This prevents SetInScope from traversing to parent
		dynamicScope: m.dynamicScope, // Preserve dynamic scope
	}
}

// SetWithVisibility sets a value with explicit visibility information
func (m *ModuleValue) SetWithVisibility(name string, value Value, visibility Visibility) {
	m.Values[name] = value
	m.Visibilities[name] = visibility
}

// Reassign reassigns a value following proper scoping rules:
// - If the variable exists locally, update it locally
// - If the variable doesn't exist locally but exists in parent, update parent (unless forked)
// - If the variable doesn't exist anywhere, set it locally
func (m *ModuleValue) Reassign(name string, value Value) {
	if _, existsLocally := m.Values[name]; existsLocally {
		// Variable exists locally, update it locally
		m.Values[name] = value
		m.Visibilities[name] = m.Visibility(name)
	} else if m.Parent != nil && !m.IsForked {
		if _, existsInParent := m.Parent.Get(name); existsInParent {
			// Variable exists in parent, update parent (only if not forked)
			m.Parent.Reassign(name, value)
		} else {
			// Variable doesn't exist anywhere, set it locally
			m.Values[name] = value
			m.Visibilities[name] = m.Visibility(name)
		}
	} else {
		// No parent or forked boundary, set it locally
		m.Values[name] = value
		m.Visibilities[name] = m.Visibility(name)
	}
}

// GetDynamicScope returns the dynamic scope value ('self')
func (m *ModuleValue) GetDynamicScope() (Value, bool) {
	if m.dynamicScope != nil {
		return m.dynamicScope, true
	}
	if m.Parent != nil {
		return m.Parent.GetDynamicScope()
	}
	return nil, false
}

// SetDynamicScope sets the dynamic scope value ('self')
func (m *ModuleValue) SetDynamicScope(value Value) {
	m.dynamicScope = value
}

// MarshalJSON implements json.Marshaler for ModuleValue
// Includes private fields, so that state can be retained
func (m *ModuleValue) MarshalJSON() ([]byte, error) {
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
	Receiver EvalEnv
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

func (b BoundMethod) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	// Create a composite environment that includes both the receiver and the method's closure
	recv := b.Receiver.Fork()
	fnEnv := CreateCompositeEnv(recv.Clone(), b.Method.Closure)
	fnEnv.SetDynamicScope(recv)

	if err := b.Method.BindArgs(ctx, fnEnv, args); err != nil {
		return nil, err
	}

	return EvalNode(ctx, fnEnv, b.Method.Body)
}

func (b BoundMethod) ParameterNames() []string {
	return b.Method.Args
}

func (b BoundMethod) IsAutoCallable() bool {
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

func (b BoundBuiltinMethod) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	// Create a temporary environment with the receiver as dynamic scope
	tempMod := NewModule("_temp_", ObjectKind)
	tempEnv := NewModuleValue(tempMod)
	tempEnv.SetDynamicScope(b.Receiver)

	// Call the builtin function with the receiver context
	return b.Method.Call(ctx, tempEnv, args)
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
	CallFn       func(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error)
	AllDefaulted bool // true when every parameter has a default value
}

func (b BuiltinFunction) Type() hm.Type {
	return b.FnType
}

func (b BuiltinFunction) String() string {
	return fmt.Sprintf("builtin:%s", b.Name)
}

func (b BuiltinFunction) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	return b.CallFn(ctx, env, args)
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
	if b.AllDefaulted {
		return true
	}
	if rt, ok := b.FnType.Arg().(*RecordType); ok {
		return len(rt.Fields) == 0
	}
	return false
}

// ConstructorFunction represents a class constructor that evaluates the class body when called
type ConstructorFunction struct {
	Closure        EvalEnv
	ClassName      string
	Parameters     []*SlotDecl
	ClassType      *Module
	FnType         *hm.FunctionType
	ClassBodyForms []Node // Field declarations to evaluate (excluding NewConstructorDecl)
	NewBody        *Block // Explicit new() body, if present
}

func (c *ConstructorFunction) Type() hm.Type {
	return c.FnType
}

func (c *ConstructorFunction) String() string {
	return fmt.Sprintf("constructor:%s", c.ClassName)
}

func (c *ConstructorFunction) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	// Create a new instance of the class
	instance := NewModuleValue(c.ClassType)

	instanceEnv := CreateCompositeEnv(instance, c.Closure)

	// Set dynamic scope to the instance so self is available
	instanceEnv.SetDynamicScope(instance)

	if c.NewBody != nil {
		// Explicit new() constructor: evaluate field declarations that have
		// defaults, then execute the new() body (which must assign required fields).

		// Filter to only include SlotDecls with defaults and methods
		var formsWithDefaults []Node
		for _, form := range c.ClassBodyForms {
			if slot, ok := form.(*SlotDecl); ok {
				// Skip required fields without defaults — new() will set them
				if slot.Value == nil {
					continue
				}
			}
			formsWithDefaults = append(formsWithDefaults, form)
		}

		_, err := EvaluateFormsWithPhases(ctx, formsWithDefaults, instanceEnv)
		if err != nil {
			return nil, fmt.Errorf("evaluating class body for %s: %w", c.ClassName, err)
		}

		// Bind constructor args so they shadow both instance fields and
		// the outer closure (see #23). Args go in a separate env that is
		// only consulted for reads (Get), while writes (Set/Reassign) go
		// to the instance as before.
		argEnv := NewModuleValue(NewModule("_constructor_args_", ObjectKind))
		// Build a temporary env layered on the closure so that default
		// expressions for later parameters can see earlier parameters.
		defaultEvalEnv := c.Closure.Clone()
		for _, param := range c.Parameters {
			if arg, found := args[param.Name.Name]; found {
				argEnv.Set(param.Name.Name, arg)
				defaultEvalEnv.Set(param.Name.Name, arg)
			} else if param.Value != nil {
				// Evaluate in defaultEvalEnv so earlier args are visible.
				defaultVal, err := EvalNode(ctx, defaultEvalEnv, param.Value)
				if err != nil {
					return nil, fmt.Errorf("evaluating default for constructor arg %q: %w", param.Name.Name, err)
				}
				argEnv.Set(param.Name.Name, defaultVal)
				defaultEvalEnv.Set(param.Name.Name, defaultVal)
			}
		}

		// Execute the new() body with access to self and constructor args.
		// We evaluate forms directly (not via Block.Eval) to avoid cloning
		// the env, which would lose dynamic scope updates for self assignments.
		newBodyEnv := CreateConstructorEnv(instance, argEnv, c.Closure)
		newBodyEnv.SetDynamicScope(instance)

		var lastVal Value
		for _, form := range c.NewBody.Forms {
			lastVal, err = EvalNode(ctx, newBodyEnv, form)
			if err != nil {
				return nil, fmt.Errorf("evaluating new() for %s: %w", c.ClassName, err)
			}
			// After each form, update the instance from the dynamic scope
			// (copy-on-write may have replaced it via self.field = value)
			if updatedInstance, found := newBodyEnv.GetDynamicScope(); found {
				instance = updatedInstance.(*ModuleValue)
				// Update newBodyEnv to use the new instance
				newBodyEnv = CreateConstructorEnv(instance, argEnv, c.Closure)
				newBodyEnv.SetDynamicScope(instance)
			}
		}

		// The new() body returns self as its last expression, just like a
		// normal function. This is enforced at inference time. Using the
		// last value (rather than the tracked instance) ensures method
		// chains like self.withFoo() propagate their changes (see #24).
		if mv, ok := lastVal.(*ModuleValue); ok {
			instance = mv
		}

		// Check that all non-null fields have been assigned
		if err := c.checkRequiredFields(instance); err != nil {
			return nil, err
		}
	} else {
		// Field-derived constructor: bind provided arguments on the instance,
		// then evaluate field declarations.
		for _, param := range c.Parameters {
			if arg, found := args[param.Name.Name]; found {
				instanceEnv.SetWithVisibility(param.Name.Name, arg, param.Visibility)
			}
		}

		_, err := EvaluateFormsWithPhases(ctx, c.ClassBodyForms, instanceEnv)
		if err != nil {
			return nil, fmt.Errorf("evaluating class body for %s: %w", c.ClassName, err)
		}
	}

	return instance, nil
}

// checkRequiredFields verifies that all non-null fields have been assigned
func (c *ConstructorFunction) checkRequiredFields(instance *ModuleValue) error {
	for name, scheme := range c.ClassType.Bindings(PrivateVisibility) {
		fieldType, _ := scheme.Type()
		if _, isNonNull := fieldType.(hm.NonNullType); isNonNull {
			// Check if this field has a value on the instance
			val, found := instance.Get(name)
			if !found {
				return fmt.Errorf("new() for %s: required field %q was not assigned", c.ClassName, name)
			}
			if _, isNull := val.(NullValue); isNull {
				return fmt.Errorf("new() for %s: required field %q was not assigned", c.ClassName, name)
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

	node := parsed.(*ModuleBlock)

	// Inject auto-imports for any import configs in context
	node.Forms = InjectAutoImports(ctx, node.Forms)

	if debug {
		_, _ = pretty.Println(node)
	}

	typeEnv := NewPreludeEnv()

	inferred, err := Infer(ctx, typeEnv, node, true)
	if err != nil {
		// Convert InferError to SourceError with full context
		return ConvertInferError(err)
	}

	slog.Debug("type inference completed", "type", inferred)

	// Now evaluate the program
	evalEnv := NewEvalEnv(typeEnv)

	result, err := EvalNodeWithContext(ctx, evalEnv, node, evalCtx)
	if err != nil {
		// If it's already a SourceError, don't wrap it again
		var sourceErr *SourceError
		if errors.As(err, &sourceErr) {
			return err
		}
		// Surface uncaught raise errors with source highlighting.
		var raised *RaisedError
		if errors.As(err, &raised) {
			return NewSourceError(
				fmt.Errorf("uncaught error: %s", raised.Error()),
				raised.Location,
				evalCtx.Source,
			)
		}
		return fmt.Errorf("evaluation error: %w", err)
	}

	slog.Debug("evaluation completed", "result", result.String())
	slog.Debug("final program result", "result", result.String())

	return nil
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

// RunDir evaluates all .dang files in a directory as a single module
func RunDir(ctx context.Context, dirPath string, isDebug bool) (EvalEnv, error) {
	// Ensure service registry exists for cleanup
	ctx, services := ensureServiceRegistry(ctx)
	if services != nil {
		defer services.StopAll()
	}

	// Load project config (dang.toml) if not already in context
	ctx, err := ensureProjectImports(ctx, dirPath)
	if err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}

	// Discover all .dang files in the directory
	dangFiles, err := filepath.Glob(filepath.Join(dirPath, "*.dang"))
	if err != nil {
		return nil, fmt.Errorf("failed to find .dang files in directory %s: %w", dirPath, err)
	}

	if len(dangFiles) == 0 {
		return nil, fmt.Errorf("no .dang files found in directory: %s", dirPath)
	}

	// Sort files for deterministic order
	sort.Strings(dangFiles)

	// Parse all files and collect their blocks
	var allForms []Node

	for _, filePath := range dangFiles {
		// Parse the file
		parsed, err := ParseFileWithRecovery(filePath, GlobalStore("filePath", filePath))
		if err != nil {
			return nil, fmt.Errorf("failed to parse file %s: %w", filePath, err)
		}

		moduleBlock := parsed.(*ModuleBlock)
		// Add all forms from this file to the combined block
		allForms = append(allForms, moduleBlock.Forms...)
	}

	// Auto-inject imports for any import configs in context that aren't
	// already explicitly imported. This allows SDKs (e.g. Dagger) to make
	// their import available without requiring module authors to write it.
	allForms = InjectAutoImports(ctx, allForms)

	// Create a master ModuleBlock containing all forms from all files
	// The phased approach will handle dependency ordering
	masterBlock := &ModuleBlock{
		Forms:  allForms,
		Inline: true,
	}

	if isDebug {
		fmt.Printf("Evaluating directory: %s\n", dirPath)
		fmt.Printf("Found %d .dang files with %d total forms\n", len(dangFiles), len(masterBlock.Forms))
		// pretty.Println(masterBlock)
	}

	// Create type environment
	typeEnv := NewPreludeEnv()

	// Run type inference using phased approach
	if isDebug {
		fmt.Println("Running phased inference...")
	}

	inferred, err := Infer(ctx, typeEnv, masterBlock, true)
	if err != nil {
		return nil, ConvertInferError(err)
	}

	slog.Debug("directory type inference completed", "type", inferred, "dir", dirPath)

	// Create evaluation environment
	evalEnv := NewEvalEnv(typeEnv)

	// Evaluate the combined block using phased evaluation
	if isDebug {
		fmt.Println("Running phased evaluation...")
	}

	// Create an eval context for error reporting. Since we're evaluating
	// forms from multiple files, we don't provide a source string here.
	// The error formatter will read individual source files as needed.
	evalCtx := NewEvalContext(dirPath, "")

	result, err := EvalNodeWithContext(ctx, evalEnv, masterBlock, evalCtx)
	if err != nil {
		return nil, err
	}

	slog.Debug("directory evaluation completed", "result", result.String(), "dir", dirPath)

	return evalEnv, nil
}

// EvalNode evaluates any AST node (legacy interface)
func EvalNode(ctx context.Context, env EvalEnv, node Node) (Value, error) {
	return EvalNodeWithContext(ctx, env, node, GetEvalContext(ctx))
}

// EvalNodeWithContext evaluates any AST node with enhanced error reporting
func EvalNodeWithContext(ctx context.Context, env EvalEnv, node Node, evalCtx *EvalContext) (Value, error) {
	// Store the eval context in the Go context for evaluators to access
	if evalCtx != nil {
		ctx = WithEvalContext(ctx, evalCtx)
	}

	if evaluator, ok := node.(Evaluator); ok {
		val, err := evaluator.Eval(ctx, env)
		if err != nil {
			// Let user-level raise errors propagate unwrapped so
			// TryCatch can intercept them cleanly.
			var raised *RaisedError
			if errors.As(err, &raised) {
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
