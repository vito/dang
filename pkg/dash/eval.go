package dash

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/chewxy/hm"
	"github.com/dagger/dagger/codegen/introspection"
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
	Clone() EvalEnv
	Writer() io.Writer
	SetWriter(w io.Writer) EvalEnv
}

// SimpleEvalEnv is a simple implementation of EvalEnv
type SimpleEvalEnv struct {
	vars   map[string]Value
	parent EvalEnv
	writer io.Writer
}

func NewEvalEnv() *SimpleEvalEnv {
	return &SimpleEvalEnv{
		vars:   make(map[string]Value),
		writer: os.Stdout, // Default to stdout
	}
}

func (e *SimpleEvalEnv) Get(name string) (Value, bool) {
	if val, ok := e.vars[name]; ok {
		return val, true
	}
	if e.parent != nil {
		return e.parent.Get(name)
	}
	return nil, false
}

func (e *SimpleEvalEnv) Set(name string, value Value) EvalEnv {
	e.vars[name] = value
	return e
}

func (e *SimpleEvalEnv) Clone() EvalEnv {
	newEnv := &SimpleEvalEnv{
		vars:   make(map[string]Value),
		parent: e,
		writer: e.writer, // Inherit writer from parent
	}
	return newEnv
}

func (e *SimpleEvalEnv) Writer() io.Writer {
	if e.writer != nil {
		return e.writer
	}
	if e.parent != nil {
		return e.parent.Writer()
	}
	return os.Stdout // Fallback
}

func (e *SimpleEvalEnv) SetWriter(w io.Writer) EvalEnv {
	e.writer = w
	return e
}

// GraphQLFunction represents a GraphQL API function that makes actual calls
type GraphQLFunction struct {
	Name       string
	TypeName   string
	Field      *introspection.Field
	FnType     *hm.FunctionType
	Client     *dagger.Client
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
		query = query.Bind(&result).Client(g.Client.GraphQLClient())
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
	Client     *dagger.Client
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
	
	// If this field has arguments, return a GraphQLFunction for calling
	if len(field.Args) > 0 {
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
	
	// For 0-arity fields, check if it's scalar
	if isScalarType(field.TypeRef, g.Schema) {
		// Execute the query and return the scalar result
		var query *querybuilder.Selection
		if g.QueryChain != nil {
			// Use existing query chain and add this field
			query = g.QueryChain.Select(fieldName)
		} else {
			// Build from scratch (shouldn't happen in normal flow)
			query = querybuilder.Query().Select(g.Name).Select(fieldName)
		}
		
		var result interface{}
		query = query.Bind(&result).Client(g.Client.GraphQLClient())
		if err := query.Execute(ctx); err != nil {
			return nil, fmt.Errorf("executing GraphQL query for %s.%s: %w", g.TypeName, fieldName, err)
		}
		return goValueToDash(result, field.TypeRef)
	}
	
	// For non-scalar 0-arity fields, return another GraphQLValue for further selection
	var newQueryChain *querybuilder.Selection
	if g.QueryChain != nil {
		newQueryChain = g.QueryChain.Select(fieldName)
	} else {
		newQueryChain = querybuilder.Query().Select(g.Name).Select(fieldName)
	}
	
	return GraphQLValue{
		Name:       fmt.Sprintf("%s.%s", g.Name, fieldName),
		TypeName:   getTypeName(field.TypeRef),
		Field:      field,
		ValType:    g.ValType, // This could be improved with proper type inference
		Client:     g.Client,
		Schema:     g.Schema,
		QueryChain: newQueryChain,
	}, nil
}

// NewEvalEnvWithSchema creates an evaluation environment populated with GraphQL API values
func NewEvalEnvWithSchema(schema *introspection.Schema, dag *dagger.Client) EvalEnv {
	env := NewEvalEnv()

	// Create a type environment to help with type conversion
	typeEnv := NewEnv(schema)

	for _, t := range schema.Types {
		for _, f := range t.Fields {
			ret, err := gqlToTypeNode(typeEnv, f.TypeRef)
			if err != nil {
				continue
			}

			if len(f.Args) > 0 {
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
					Client:     dag,
					Schema:     schema,
					QueryChain: nil, // Top-level functions start with no query chain
				}

				// Add to global scope if it's from the Query type
				if t.Name == schema.QueryType.Name {
					env.Set(f.Name, gqlFunc)
				}
			} else {
				// This is a field/property - create a GraphQLValue
				gqlVal := GraphQLValue{
					Name:       f.Name,
					TypeName:   t.Name,
					Field:      f,
					ValType:    ret,
					Client:     dag,
					Schema:     schema,
					QueryChain: nil, // Top-level values start with no query chain
				}

				// Add to global scope if it's from the Query type
				if t.Name == schema.QueryType.Name {
					env.Set(f.Name, gqlVal)
				}
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

			// Print the value to the configured writer
			fmt.Fprintln(env.Writer(), val.String())
			
			// Return null
			return NullValue{}, nil
		},
	}

	env.Set("print", printFn)
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
	default:
		return nil, fmt.Errorf("unsupported value type: %T", val)
	}
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

// NullValue represents a null value
type NullValue struct{}

func (n NullValue) Type() hm.Type {
	// Null has no specific type, return a fresh type variable
	return hm.TypeVariable('n')
}

func (n NullValue) String() string {
	return "null"
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

// RecordValue represents a record value
type RecordValue struct {
	Fields  map[string]Value
	RecType *RecordType
}

func (r RecordValue) Type() hm.Type {
	return NonNullType{r.RecType}
}

func (r RecordValue) String() string {
	result := "{"
	i := 0
	for k, v := range r.Fields {
		if i > 0 {
			result += ", "
		}
		result += fmt.Sprintf("%s: %s", k, v.String())
		i++
	}
	return result + "}"
}

// FunctionValue represents a function value
type FunctionValue struct {
	Args    []string
	Body    Node
	Closure EvalEnv
	FnType  *hm.FunctionType
}

func (f FunctionValue) Type() hm.Type {
	return f.FnType
}

func (f FunctionValue) String() string {
	return fmt.Sprintf("function(%v)", f.Args)
}

// ModuleValue represents a module value
type ModuleValue struct {
	Mod    *Module
	Values map[string]Value
}

func (m ModuleValue) Type() hm.Type {
	return NonNullType{m.Mod}
}

func (m ModuleValue) String() string {
	return fmt.Sprintf("module %s", m.Mod.Named)
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

func CheckFile(schema *introspection.Schema, dag *dagger.Client, filePath string) error {
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

	typeEnv := NewEnv(schema)

	inferred, err := Infer(typeEnv, node, true)
	if err != nil {
		return err
	}

	slog.Debug("type inference completed", "type", inferred)

	// Now evaluate the program
	evalEnv := NewEvalEnvWithSchema(schema, dag)
	ctx := context.Background()

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
		return val, err
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
