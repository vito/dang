package dash

import (
	"context"
	"fmt"
	"log"
	"os"

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
}

// SimpleEvalEnv is a simple implementation of EvalEnv
type SimpleEvalEnv struct {
	vars   map[string]Value
	parent EvalEnv
}

func NewEvalEnv() *SimpleEvalEnv {
	return &SimpleEvalEnv{
		vars: make(map[string]Value),
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
	}
	return newEnv
}

// GraphQLFunction represents a GraphQL API function that makes actual calls
type GraphQLFunction struct {
	Name     string
	TypeName string
	Field    *introspection.Field
	FnType   *hm.FunctionType
}

func (g GraphQLFunction) Type() hm.Type {
	return g.FnType
}

func (g GraphQLFunction) String() string {
	return fmt.Sprintf("gql:%s.%s", g.TypeName, g.Name)
}

func (g GraphQLFunction) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	// TODO: This is where we would make the actual GraphQL call
	// For now, return a placeholder
	return StringValue{Val: fmt.Sprintf("GraphQL call to %s.%s", g.TypeName, g.Name)}, nil
}

// GraphQLValue represents a GraphQL object/field value
type GraphQLValue struct {
	Name     string
	TypeName string
	Field    *introspection.Field
	ValType  hm.Type
}

func (g GraphQLValue) Type() hm.Type {
	return g.ValType
}

func (g GraphQLValue) String() string {
	return fmt.Sprintf("gql:%s.%s", g.TypeName, g.Name)
}

// NewEvalEnvWithSchema creates an evaluation environment populated with GraphQL API values
func NewEvalEnvWithSchema(schema *introspection.Schema) EvalEnv {
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
					Name:     f.Name,
					TypeName: t.Name,
					Field:    f,
					FnType:   fnType,
				}

				// Add to global scope if it's from the Query type
				if t.Name == schema.QueryType.Name {
					env.Set(f.Name, gqlFunc)
				}
			} else {
				// This is a field/property - create a GraphQLValue
				gqlVal := GraphQLValue{
					Name:     f.Name,
					TypeName: t.Name,
					Field:    f,
					ValType:  ret,
				}

				// Add to global scope if it's from the Query type
				if t.Name == schema.QueryType.Name {
					env.Set(f.Name, gqlVal)
				}
			}
		}
	}

	return env
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

func CheckFile(schema *introspection.Schema, filePath string) error {
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

	log.Printf("INFERRED TYPE: %s", inferred)

	// Now evaluate the program
	evalEnv := NewEvalEnvWithSchema(schema)
	ctx := context.Background()

	result, err := EvalNodeWithContext(ctx, evalEnv, node, evalCtx)
	if err != nil {
		// If it's already a SourceError, don't wrap it again
		if _, isSourceError := err.(*SourceError); isSourceError {
			return err
		}
		return fmt.Errorf("evaluation error: %w", err)
	}

	log.Printf("EVALUATION RESULT: %s", result.String())
	fmt.Fprintf(os.Stderr, ">>> FINAL RESULT: %s\n", result.String())

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
