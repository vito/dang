package dash

import (
	"context"
	"fmt"

	"github.com/chewxy/hm"
)

// Type variables for literal types
var (
	// Null does not have a type. Its type is always inferred as a free variable.
	// NullType    = NewClass("Null")

	BooleanType = NewModule("Boolean")
	StringType  = NewModule("String")
	IntType     = NewModule("Int")
)

// List represents a list literal
type List struct {
	Elements []Node
	Loc      *SourceLocation
}

var _ Node = List{}
var _ Evaluator = List{}

func (l List) Infer(env hm.Env, f hm.Fresher) (hm.Type, error) {
	if len(l.Elements) == 0 {
		// For now, just return the original approach and document this as a known issue
		// The real fix requires changes to how the HM library handles recursive types
		tv := f.Fresh()
		return NonNullType{ListType{tv}}, nil
	}

	var t hm.Type
	for i, el := range l.Elements {
		et, err := el.Infer(env, f)
		if err != nil {
			return nil, err
		}
		if t == nil {
			t = et
		} else if _, err := UnifyWithCompatibility(t, et); err != nil {
			// TODO: is this right?
			return nil, fmt.Errorf("unify index %d: %w", i, err)
		}
	}
	return NonNullType{ListType{t}}, nil
}

func (l List) DeclaredSymbols() []string {
	return nil // Lists don't declare anything
}

func (l List) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from all elements
	for _, elem := range l.Elements {
		symbols = append(symbols, elem.ReferencedSymbols()...)
	}

	return symbols
}

func (l List) Body() hm.Expression { return l }

func (l List) GetSourceLocation() *SourceLocation { return l.Loc }

func (l List) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if len(l.Elements) == 0 {
		return ListValue{Elements: []Value{}, ElemType: hm.TypeVariable('a')}, nil
	}

	values := make([]Value, len(l.Elements))
	var elemType hm.Type

	for i, elem := range l.Elements {
		val, err := EvalNode(ctx, env, elem)
		if err != nil {
			return nil, fmt.Errorf("evaluating list element %d: %w", i, err)
		}
		values[i] = val
		if i == 0 {
			elemType = val.Type()
		}
	}

	return ListValue{Elements: values, ElemType: elemType}, nil
}

// Null represents a null literal
type Null struct {
	Loc *SourceLocation
}

var _ Node = Null{}
var _ Evaluator = Null{}

func (n Null) Body() hm.Expression { return n }

func (n Null) GetSourceLocation() *SourceLocation { return n.Loc }

func (Null) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return fresh.Fresh(), nil
}

func (n Null) DeclaredSymbols() []string {
	return nil // Null literals don't declare anything
}

func (n Null) ReferencedSymbols() []string {
	return nil // Null literals don't reference anything
}

func (n Null) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return NullValue{}, nil
}

// String represents a string literal
type String struct {
	Value string
	Loc   *SourceLocation
}

var _ Node = String{}
var _ Evaluator = String{}

func (s String) Body() hm.Expression { return s }

func (s String) GetSourceLocation() *SourceLocation { return s.Loc }

func (s String) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"String"}}.Infer(env, fresh)
}

func (s String) DeclaredSymbols() []string {
	return nil // String literals don't declare anything
}

func (s String) ReferencedSymbols() []string {
	return nil // String literals don't reference anything
}

func (s String) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return StringValue{Val: s.Value}, nil
}

// Quoted represents a quoted string literal
type Quoted struct {
	Quoter string
	Raw    string
}

// Boolean represents a boolean literal
type Boolean struct {
	Value bool
	Loc   *SourceLocation
}

var _ Node = Boolean{}
var _ Evaluator = Boolean{}

func (b Boolean) Body() hm.Expression { return b }

func (b Boolean) GetSourceLocation() *SourceLocation { return b.Loc }

func (b Boolean) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (b Boolean) DeclaredSymbols() []string {
	return nil // Boolean literals don't declare anything
}

func (b Boolean) ReferencedSymbols() []string {
	return nil // Boolean literals don't reference anything
}

func (b Boolean) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return BoolValue{Val: b.Value}, nil
}

// Int represents an integer literal
type Int struct {
	Value int64
	Loc   *SourceLocation
}

var _ Node = Int{}
var _ Evaluator = Int{}

func (i Int) Body() hm.Expression { return i }

func (i Int) GetSourceLocation() *SourceLocation { return i.Loc }

func (i Int) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return NonNullTypeNode{NamedTypeNode{"Int"}}.Infer(env, fresh)
}

func (i Int) DeclaredSymbols() []string {
	return nil // Int literals don't declare anything
}

func (i Int) ReferencedSymbols() []string {
	return nil // Int literals don't reference anything
}

func (i Int) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return IntValue{Val: int(i.Value)}, nil
}
