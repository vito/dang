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
	vars map[string]Value
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
		vars: make(map[string]Value),
		parent: e,
	}
	return newEnv
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
	Fields map[string]Value
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
	Args []string
	Body Node
	Closure EvalEnv
	FnType *hm.FunctionType
}

func (f FunctionValue) Type() hm.Type {
	return f.FnType
}

func (f FunctionValue) String() string {
	return fmt.Sprintf("function(%v)", f.Args)
}

// ModuleValue represents a module value
type ModuleValue struct {
	Mod *Module
	Values map[string]Value
}

func (m ModuleValue) Type() hm.Type {
	return NonNullType{m.Mod}
}

func (m ModuleValue) String() string {
	return fmt.Sprintf("module %s", m.Mod.Named)
}

func CheckFile(schema *introspection.Schema, filePath string) error {
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
	evalEnv := NewEvalEnv()
	ctx := context.Background()

	result, err := EvalNode(ctx, evalEnv, node)
	if err != nil {
		return fmt.Errorf("evaluation error: %w", err)
	}

	log.Printf("EVALUATION RESULT: %s", result.String())
	fmt.Fprintf(os.Stderr, ">>> FINAL RESULT: %s\n", result.String())

	return nil
}

// EvalNode evaluates any AST node
func EvalNode(ctx context.Context, env EvalEnv, node Node) (Value, error) {
	if evaluator, ok := node.(Evaluator); ok {
		return evaluator.Eval(ctx, env)
	}

	// Fallback for nodes that don't implement Evaluator directly
	switch n := node.(type) {
	case String:
		return StringValue{Val: n.Value}, nil
	case Int:
		return IntValue{Val: int(n)}, nil
	case Boolean:
		return BoolValue{Val: bool(n)}, nil
	case Null:
		return NullValue{}, nil
	default:
		return nil, fmt.Errorf("evaluation not implemented for node type %T", node)
	}
}

