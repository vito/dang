package dash

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"dagger.io/dagger/querybuilder"
	"github.com/Khan/genqlient/graphql"
	"github.com/chewxy/hm"
	"github.com/kr/pretty"
	"github.com/vito/dash/introspection"
	"github.com/vito/dash/pkg/ioctx"
)

// Value represents a runtime value in the Dash language
type Value interface {
	Type() hm.Type
	String() string
}

// Evaluator defines the interface for evaluating AST nodes
type Evaluator interface {
	Eval(ctx context.Context, env EvalEnv) (Value, error)
}

// EvalEnv represents the evaluation environment
type EvalEnv interface {
	Get(name string) (Value, bool)
	Set(name string, value Value) EvalEnv
	SetWithVisibility(name string, value Value, visibility Visibility)
	Clone() EvalEnv
}

// GraphQLFunction represents a GraphQL API function that makes actual calls
type GraphQLFunction struct {
	Name       string
	TypeName   string
	Field      *introspection.Field
	FnType     *hm.FunctionType
	Client     graphql.Client
	Schema     *introspection.Schema
	QueryChain *querybuilder.Selection // Keep track of the query chain built so far
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
		// Check if this is a method call (contains a dot) or a top-level function
		if strings.Contains(g.Name, ".") {
			// This is a method call like "container.from" - we need to build a nested query
			parts := strings.Split(g.Name, ".")
			query = querybuilder.Query()
			for _, part := range parts {
				query = query.Select(part)
			}
		} else {
			// This is a top-level function call
			query = querybuilder.Query().Select(g.Name)
		}
	}

	// Add arguments to the query
	for _, arg := range g.Field.Args {
		if val, ok := args[arg.Name]; ok {
			// Convert Dash value to Go value for GraphQL
			goVal, err := dashValueToGo(val)
			if err != nil {
				return nil, fmt.Errorf("converting argument %s: %w", arg.Name, err)
			}
			query = query.Arg(arg.Name, goVal)
		}
	}

	// For functions that return scalar types, execute the query immediately
	if isScalarType(g.Field.TypeRef, g.Schema) {
		// Execute the query and return the scalar value
		var result interface{}
		query = query.Bind(&result).Client(g.Client)
		if err := query.Execute(ctx); err != nil {
			return nil, fmt.Errorf("executing GraphQL query for %s.%s: %w", g.TypeName, g.Name, err)
		}
		return goValueToDash(result, g.Field.TypeRef)
	}

	// For non-scalar types, return a GraphQLValue that can be further selected
	return GraphQLValue{
		Name:       g.Name,
		TypeName:   getTypeName(g.Field.TypeRef),
		Field:      g.Field,
		ValType:    g.FnType.Ret(false),
		Client:     g.Client,
		Schema:     g.Schema,
		QueryChain: query, // Pass the query chain for further building
	}, nil
}

// GraphQLValue represents a GraphQL object/field value
type GraphQLValue struct {
	Name       string
	TypeName   string
	Field      *introspection.Field
	ValType    hm.Type
	Client     graphql.Client
	Schema     *introspection.Schema
	QueryChain *querybuilder.Selection // Keep track of the query chain built so far
}

func (g GraphQLValue) Type() hm.Type {
	return g.ValType
}

func (g GraphQLValue) String() string {
	return fmt.Sprintf("gql:%s.%s", g.TypeName, g.Name)
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
		argType, err := gqlToTypeNode(NewEnv(g.Schema), arg.TypeRef)
		if err != nil {
			continue
		}
		args.Add(arg.Name, hm.NewScheme(nil, argType))
	}

	retType, err := gqlToTypeNode(NewEnv(g.Schema), field.TypeRef)
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
		QueryChain: g.QueryChain, // Pass the current query chain
	}, nil
}

// NewEvalEnvWithSchema creates an evaluation environment populated with GraphQL API values
func NewEvalEnvWithSchema(client graphql.Client, schema *introspection.Schema) EvalEnv {
	// Create a type environment to help with type conversion
	typeEnv := NewEnv(schema)

	// Create a ModuleValue from the type environment
	env := NewModuleValue(typeEnv)

	for _, t := range schema.Types {
		for _, f := range t.Fields {
			ret, err := gqlToTypeNode(typeEnv, f.TypeRef)
			if err != nil {
				continue
			}

			// This is a function - create a GraphQLFunction value
			args := NewRecordType("")
			for _, arg := range f.Args {
				argType, err := gqlToTypeNode(typeEnv, f.TypeRef)
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
				QueryChain: nil, // Top-level functions start with no query chain
			}

			// Add to global scope if it's from the Query type
			if t.Name == schema.QueryType.Name {
				env.Set(f.Name, gqlFunc)
			}
		}
	}

	// Add builtin functions
	addBuiltinFunctions(env)

	return env
}

// addBuiltinFunctions adds builtin functions like print to the evaluation environment
func addBuiltinFunctions(env EvalEnv) {
	// Create the print function
	// Type signature: print(value: a) -> Null where 'a' is a type variable (any type)
	argType := hm.TypeVariable('a')
	args := NewRecordType("")
	args.Add("value", hm.NewScheme(nil, argType))
	printType := hm.NewFnType(args, hm.TypeVariable('n')) // returns null (type variable)

	printFn := BuiltinFunction{
		Name:   "print",
		FnType: printType,
		Call: func(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
			// Get the value to print
			val, ok := args["value"]
			if !ok {
				return nil, fmt.Errorf("print: missing required argument 'value'")
			}

			// Print the value using the context-based writer from ioctx
			writer := ioctx.StdoutFromContext(ctx)
			fmt.Fprintln(writer, val.String())

			// Return null
			return NullValue{}, nil
		},
	}

	env.Set("print", printFn)

	// Create the json function
	// Type signature: json(value: a) -> String! where 'a' is a type variable (any type)
	jsonArgs := NewRecordType("")
	jsonArgs.Add("value", hm.NewScheme(nil, argType))
	jsonReturnType := NonNullType{StringType}
	jsonType := hm.NewFnType(jsonArgs, jsonReturnType)

	jsonFn := BuiltinFunction{
		Name:   "json",
		FnType: jsonType,
		Call: func(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
			// Get the value to marshal
			val, ok := args["value"]
			if !ok {
				return nil, fmt.Errorf("json: missing required argument 'value'")
			}

			// Marshal the value to JSON
			jsonBytes, err := json.Marshal(val)
			if err != nil {
				return nil, fmt.Errorf("json: failed to marshal value: %w", err)
			}

			return StringValue{Val: string(jsonBytes)}, nil
		},
	}

	env.Set("json", jsonFn)
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

	// Check if it's a custom scalar in the schema
	for _, t := range schema.Types {
		if t.Name == currentType.Name && t.Kind == "SCALAR" {
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

// Helper function to convert Dash values to Go values for GraphQL
func dashValueToGo(val Value) (interface{}, error) {
	switch v := val.(type) {
	case StringValue:
		return v.Val, nil
	case IntValue:
		return v.Val, nil
	case BoolValue:
		return v.Val, nil
	case NullValue:
		return nil, nil
	case ListValue:
		// Convert list elements to Go slice
		result := make([]interface{}, len(v.Elements))
		for i, elem := range v.Elements {
			goVal, err := dashValueToGo(elem)
			if err != nil {
				return nil, fmt.Errorf("converting list element %d: %w", i, err)
			}
			result[i] = goVal
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

// Helper function to convert Go values back to Dash values
func goValueToDash(val interface{}, typeRef *introspection.TypeRef) (Value, error) {
	if val == nil {
		return NullValue{}, nil
	}

	switch v := val.(type) {
	case string:
		return StringValue{Val: v}, nil
	case int:
		return IntValue{Val: v}, nil
	case int64:
		return IntValue{Val: int(v)}, nil
	case float64:
		// For now, treat floats as ints - could be improved
		return IntValue{Val: int(v)}, nil
	case bool:
		return BoolValue{Val: v}, nil
	case []any:
		var vals []Value
		var elemType hm.Type
		for _, item := range v {
			val, err := goValueToDash(item, typeRef.OfType)
			if err != nil {
				return nil, err
			}
			if elemType == nil {
				elemType = val.Type()
			} else {
				if _, err := UnifyWithCompatibility(elemType, val.Type()); err != nil {
					return nil, fmt.Errorf("type mismatch: %s vs %s", elemType, val.Type())
				}
			}
			vals = append(vals, val)
		}
		if elemType == nil {
			elemType = hm.TypeVariable('a')
		}
		return ListValue{Elements: vals, ElemType: elemType}, nil
	default:
		return nil, fmt.Errorf("unsupported Go value type: %T", val)
	}
}

// Runtime value implementations

// StringValue represents a string value
type StringValue struct {
	Val string
}

func (s StringValue) Type() hm.Type {
	return NonNullType{StringType}
}

func (s StringValue) String() string {
	return s.Val
}

func (s StringValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Val)
}

// IntValue represents an integer value
type IntValue struct {
	Val int
}

func (i IntValue) Type() hm.Type {
	return NonNullType{IntType}
}

func (i IntValue) String() string {
	return fmt.Sprintf("%d", i.Val)
}

func (i IntValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.Val)
}

// BoolValue represents a boolean value
type BoolValue struct {
	Val bool
}

func (b BoolValue) Type() hm.Type {
	return NonNullType{BooleanType}
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
	return NonNullType{ListType{l.ElemType}}
}

func (l ListValue) String() string {
	result := "["
	for i, elem := range l.Elements {
		if i > 0 {
			result += ", "
		}
		result += elem.String()
	}
	return result + "]"
}

func (l ListValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.Elements)
}

// FunctionValue represents a function value
type FunctionValue struct {
	Args     []string
	Body     Node
	Closure  EvalEnv
	FnType   *hm.FunctionType
	Defaults map[string]Node // Map of argument name to default value expression
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

// ModuleValue represents a module value that implements EvalEnv
type ModuleValue struct {
	Mod          *Module
	Values       map[string]Value
	Visibilities map[string]Visibility // Track visibility of each field
	Parent       *ModuleValue          // For hierarchical scoping
}

// NewModuleValue creates a new ModuleValue with an empty values map
func NewModuleValue(mod *Module) *ModuleValue {
	return &ModuleValue{
		Mod:          mod,
		Values:       make(map[string]Value),
		Visibilities: make(map[string]Visibility),
		Parent:       nil,
	}
}

func (m ModuleValue) Type() hm.Type {
	return NonNullType{m.Mod}
}

func (m ModuleValue) String() string {
	return fmt.Sprintf("module %s", m.Mod.Named)
}

// EvalEnv interface implementation
func (m *ModuleValue) Get(name string) (Value, bool) {
	if val, ok := m.Values[name]; ok {
		return val, true
	}
	if m.Parent != nil {
		return m.Parent.Get(name)
	}
	return nil, false
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

func (m *ModuleValue) Clone() EvalEnv {
	newValues := make(map[string]Value)
	newVisibilities := make(map[string]Visibility)
	return &ModuleValue{
		Mod:          m.Mod,
		Values:       newValues,
		Visibilities: newVisibilities,
		Parent:       m,
	}
}

// SetWithVisibility sets a value with explicit visibility information
func (m *ModuleValue) SetWithVisibility(name string, value Value, visibility Visibility) {
	m.Values[name] = value
	m.Visibilities[name] = visibility
}

// MarshalJSON implements json.Marshaler for ModuleValue
// Only includes public fields in the JSON output
func (m ModuleValue) MarshalJSON() ([]byte, error) {
	result := make(map[string]Value)
	m.collectPublic(result)
	return json.Marshal(result)
}

func (m ModuleValue) collectPublic(dest map[string]Value) {
	for name, value := range m.Values {
		if _, shadowed := dest[name]; shadowed {
			continue
		}
		// Only include public fields
		if m.Visibilities[name] == PublicVisibility {
			dest[name] = value
		}
	}
	if m.Parent != nil {
		m.Parent.collectPublic(dest)
	}
}

// dashValueToJSON converts a Dash Value to a JSON-serializable Go value
func dashValueToJSON(val Value) (interface{}, error) {
	return dashValueToJSONWithVisited(val, make(map[*map[string]Value]bool))
}

// dashValueToJSONWithVisited converts a Dash Value to a JSON-serializable Go value with recursion protection
func dashValueToJSONWithVisited(val Value, visited map[*map[string]Value]bool) (interface{}, error) {
	switch v := val.(type) {
	case StringValue:
		return v.Val, nil
	case IntValue:
		return v.Val, nil
	case BoolValue:
		return v.Val, nil
	case NullValue:
		return nil, nil
	case ListValue:
		jsonList := make([]interface{}, len(v.Elements))
		for i, elem := range v.Elements {
			jsonElem, err := dashValueToJSONWithVisited(elem, visited)
			if err != nil {
				return nil, fmt.Errorf("converting list element %d: %w", i, err)
			}
			jsonList[i] = jsonElem
		}
		return jsonList, nil
	case ModuleValue:
		// Check for recursion using the Values map pointer
		mapPtr := &v.Values
		if visited[mapPtr] {
			return "<circular reference>", nil
		}
		visited[mapPtr] = true
		defer delete(visited, mapPtr)

		// For modules, create a map manually to avoid infinite recursion
		result := make(map[string]interface{})
		for name, value := range v.Values {
			// Only include public fields
			if visibility, exists := v.Visibilities[name]; !exists || visibility == PublicVisibility {
				jsonValue, err := dashValueToJSONWithVisited(value, visited)
				if err != nil {
					return nil, fmt.Errorf("failed to convert field %q to JSON: %w", name, err)
				}
				result[name] = jsonValue
			}
		}
		return result, nil
	case FunctionValue:
		// Functions can't be serialized to JSON - represent as a string
		return fmt.Sprintf("function(%v)", v.Args), nil
	case GraphQLFunction:
		// GraphQL functions represented as their name
		return v.String(), nil
	case BuiltinFunction:
		// Builtin functions represented as their name
		return v.String(), nil
	default:
		// For unknown types, use their string representation
		return v.String(), nil
	}
}

// BoundMethod represents a method bound to a specific receiver
type BoundMethod struct {
	Method   FunctionValue
	Receiver *ModuleValue
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

// BuiltinFunction represents a builtin function like print
type BuiltinFunction struct {
	Name   string
	FnType *hm.FunctionType
	Call   func(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error)
}

func (b BuiltinFunction) Type() hm.Type {
	return b.FnType
}

func (b BuiltinFunction) String() string {
	return fmt.Sprintf("builtin:%s", b.Name)
}

func RunFile(client graphql.Client, schema *introspection.Schema, filePath string, debug bool) error {
	// Read the source file for error reporting
	sourceBytes, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}
	source := string(sourceBytes)

	// Create evaluation context for enhanced error reporting
	evalCtx := NewEvalContext(filePath, source)

	dash, err := ParseFile(filePath)
	if err != nil {
		return err
	}

	node := dash.(Block)

	if debug {
		pretty.Println(node)
	}

	typeEnv := NewEnv(schema)

	inferred, err := Infer(typeEnv, node, true)
	if err != nil {
		return err
	}

	slog.Debug("type inference completed", "type", inferred)

	// Now evaluate the program
	evalEnv := NewEvalEnvWithSchema(client, schema)
	ctx := context.Background()
	ctx = ioctx.StdoutToContext(ctx, os.Stdout)

	result, err := EvalNodeWithContext(ctx, evalEnv, node, evalCtx)
	if err != nil {
		// If it's already a SourceError, don't wrap it again
		if _, isSourceError := err.(*SourceError); isSourceError {
			return err
		}
		return fmt.Errorf("evaluation error: %w", err)
	}

	slog.Debug("evaluation completed", "result", result.String())
	slog.Debug("final program result", "result", result.String())

	return nil
}

// EvalNode evaluates any AST node (legacy interface)
func EvalNode(ctx context.Context, env EvalEnv, node Node) (Value, error) {
	return EvalNodeWithContext(ctx, env, node, nil)
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
			// Check if the error is already a SourceError - if so, don't wrap it again
			if _, isSourceError := err.(*SourceError); isSourceError || evalCtx == nil {
				return nil, err
			}
			// Only create source error if the evaluator didn't already create one
			return nil, evalCtx.CreateSourceError(err, node)
		}
		if val == nil {
			return nil, fmt.Errorf("Evaluator(%T) returned nil", node)
		}
		return val, nil
	}

	// Fallback for nodes that don't implement Evaluator directly
	switch n := node.(type) {
	case String:
		return StringValue{Val: n.Value}, nil
	case Int:
		return IntValue{Val: int(n.Value)}, nil
	case Boolean:
		return BoolValue{Val: n.Value}, nil
	case Null:
		return NullValue{}, nil
	default:
		err := fmt.Errorf("evaluation not implemented for node type %T", node)
		if evalCtx != nil {
			return nil, evalCtx.CreateSourceError(err, node)
		}
		return nil, err
	}
}
