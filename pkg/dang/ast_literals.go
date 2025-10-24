package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// Type variables for literal types
var (
	// Null does not have a type. Its type is always inferred as a free variable.
	// NullType    = NewClass("Null")

	IDType      = NewModule("ID")
	BooleanType = NewModule("Boolean")
	StringType  = NewModule("String")
	IntType     = NewModule("Int")
)

// List represents a list literal
type List struct {
	InferredTypeHolder
	Elements []Node
	Loc      *SourceLocation
}

var _ Node = (*List)(nil)
var _ Evaluator = (*List)(nil)

func (l *List) Infer(ctx context.Context, env hm.Env, f hm.Fresher) (hm.Type, error) {
	if len(l.Elements) == 0 {
		// For now, just return the original approach and document this as a known issue
		// The real fix requires changes to how the HM library handles recursive types
		tv := f.Fresh()
		t := hm.NonNullType{Type: ListType{tv}}
		l.SetInferredType(t)
		return t, nil
	}

	var t hm.Type
	for i, el := range l.Elements {
		et, err := el.Infer(ctx, env, f)
		if err != nil {
			return nil, err
		}
		if t == nil {
			t = et
		} else {
			subs, err := hm.Unify(t, et)
			if err != nil {
				return nil, NewInferError(fmt.Errorf("unify index %d: %s", i, err.Error()), l.Elements[i])
			}
			t = t.Apply(subs).(hm.Type)
		}
	}
	listType := hm.NonNullType{Type: ListType{t}}
	l.SetInferredType(listType)
	return listType, nil
}

func (l *List) DeclaredSymbols() []string {
	return nil // Lists don't declare anything
}

func (l *List) ReferencedSymbols() []string {
	var symbols []string

	// Add symbols from all elements
	for _, elem := range l.Elements {
		symbols = append(symbols, elem.ReferencedSymbols()...)
	}

	return symbols
}

func (l *List) Body() hm.Expression { return l }

func (l *List) GetSourceLocation() *SourceLocation { return l.Loc }

func (l *List) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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
	InferredTypeHolder
	Loc *SourceLocation
}

var _ Node = (*Null)(nil)
var _ Evaluator = (*Null)(nil)

func (n *Null) Body() hm.Expression { return n }

func (n *Null) GetSourceLocation() *SourceLocation { return n.Loc }

func (n *Null) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := fresh.Fresh()
	n.SetInferredType(t)
	return t, nil
}

func (n *Null) DeclaredSymbols() []string {
	return nil // Null literals don't declare anything
}

func (n *Null) ReferencedSymbols() []string {
	return nil // Null literals don't reference anything
}

func (n *Null) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return NullValue{}, nil
}

// String represents a string literal
type String struct {
	InferredTypeHolder
	Value string
	Loc   *SourceLocation
}

var _ Node = (*String)(nil)
var _ Evaluator = (*String)(nil)

func (s *String) Body() hm.Expression { return s }

func (s *String) GetSourceLocation() *SourceLocation { return s.Loc }

func (s *String) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := hm.NonNullType{Type: StringType}
	s.SetInferredType(t)
	return t, nil
}

func (s *String) DeclaredSymbols() []string {
	return nil // String literals don't declare anything
}

func (s *String) ReferencedSymbols() []string {
	return nil // String literals don't reference anything
}

func (s *String) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return StringValue{Val: s.Value}, nil
}

// Quoted represents a quoted string literal
type Quoted struct {
	InferredTypeHolder
	Quoter string
	Raw    string
}

// Boolean represents a boolean literal
type Boolean struct {
	InferredTypeHolder
	Value bool
	Loc   *SourceLocation
}

var _ Node = (*Boolean)(nil)
var _ Evaluator = (*Boolean)(nil)

func (b *Boolean) Body() hm.Expression { return b }

func (b *Boolean) GetSourceLocation() *SourceLocation { return b.Loc }

func (b *Boolean) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := hm.NonNullType{Type: BooleanType}
	b.SetInferredType(t)
	return t, nil
}

func (b *Boolean) DeclaredSymbols() []string {
	return nil // Boolean literals don't declare anything
}

func (b *Boolean) ReferencedSymbols() []string {
	return nil // Boolean literals don't reference anything
}

func (b *Boolean) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return BoolValue{Val: b.Value}, nil
}

// Int represents an integer literal
type Int struct {
	InferredTypeHolder
	Value int64
	Loc   *SourceLocation
}

var _ Node = (*Int)(nil)
var _ Evaluator = (*Int)(nil)

func (i *Int) Body() hm.Expression { return i }

func (i *Int) GetSourceLocation() *SourceLocation { return i.Loc }

func (i *Int) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := hm.NonNullType{Type: IntType}
	i.SetInferredType(t)
	return t, nil
}

func (i *Int) DeclaredSymbols() []string {
	return nil // Int literals don't declare anything
}

func (i *Int) ReferencedSymbols() []string {
	return nil // Int literals don't reference anything
}

func (i *Int) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return IntValue{Val: int(i.Value)}, nil
}
