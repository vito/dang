package dang

import (
	"bytes"
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// Type variables for literal types
var (
	// Null does not have a type. Its type is always inferred as a free variable.
	// NullType    = NewClass("Null")

	IDType         = NewModule("ID", ScalarKind)
	BooleanType    = NewModule("Boolean", ScalarKind)
	StringType     = NewModule("String", ScalarKind)
	IntType        = NewModule("Int", ScalarKind)
	FloatType      = NewModule("Float", ScalarKind)
	ListTypeModule = NewModule("List", ScalarKind)
	ErrorType      = NewModule("Error", InterfaceKind)
	BasicErrorType = NewModule("BasicError", ObjectKind)
)

// Constant is implemented by nodes whose type can be determined without
// inspecting the surrounding environment (e.g. string, int, and boolean
// literals).  SlotDecl.Hoist uses this to register field types early so
// that sibling method default-value expressions can reference them.
type Constant interface {
	ConstantType() hm.Type
}

var _ Constant = (*String)(nil)
var _ Constant = (*Template)(nil)
var _ Constant = (*Int)(nil)
var _ Constant = (*Boolean)(nil)

// List represents a list literal
type List struct {
	InferredTypeHolder
	Elements []Node
	Loc      *SourceLocation
}

var _ Node = (*List)(nil)
var _ Evaluator = (*List)(nil)

func (l *List) Infer(ctx context.Context, env hm.Env, f hm.Fresher) (hm.Type, error) {
	// If an expected list type is in scope (e.g. from a slot annotation),
	// push its element type down into each element's inference so that
	// elements of distinct union members can satisfy a union element type.
	elemExpected := expectedListElementType(currentInferExpectedType(ctx))
	elemCtx := contextWithoutInferExpectedType(ctx)
	if elemExpected != nil {
		elemCtx = contextWithInferExpectedType(elemCtx, elemExpected)
	}

	if len(l.Elements) == 0 {
		tv := f.Fresh()
		t := hm.NonNullType{Type: ListType{tv}}
		l.SetInferredType(t)
		return t, nil
	}

	var t hm.Type
	for i, el := range l.Elements {
		et, err := el.Infer(elemCtx, env, f)
		if err != nil {
			return nil, err
		}
		if elemExpected != nil {
			if _, err := hm.Assignable(et, elemExpected); err != nil {
				return nil, NewInferError(fmt.Errorf("list element %d: cannot use %s as %s", i, et, elemExpected), l.Elements[i])
			}
			t = elemExpected
			continue
		}
		if t == nil {
			t = et
		} else {
			merged, _, err := hm.MergeTypes(t, et)
			if err != nil {
				return nil, NewInferError(fmt.Errorf("unify index %d: no common type between %s and %s", i, et, t), l.Elements[i])
			}
			t = merged
		}
	}
	listType := hm.NonNullType{Type: ListType{t}}
	l.SetInferredType(listType)
	return listType, nil
}

// expectedListElementType peels NonNull/List wrappers off an expected list
// type so it can be propagated to list element inference. Returns nil if
// the expected type is not a list shape.
func expectedListElementType(expected hm.Type) hm.Type {
	if expected == nil {
		return nil
	}
	if nn, ok := expected.(hm.NonNullType); ok {
		expected = nn.Type
	}
	switch lt := expected.(type) {
	case ListType:
		return lt.Type
	case GraphQLListType:
		return lt.Type
	}
	return nil
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

func (l *List) Walk(fn func(Node) bool) {
	if !fn(l) {
		return
	}
	for _, elem := range l.Elements {
		elem.Walk(fn)
	}
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
	t := hm.NullableTypeVariable{TypeVariable: fresh.Fresh()}
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

func (n *Null) Walk(fn func(Node) bool) {
	fn(n)
}

// SelfKeyword represents the 'self' keyword that accesses dynamic scope
type SelfKeyword struct {
	InferredTypeHolder
	Loc *SourceLocation
}

var _ Node = (*SelfKeyword)(nil)
var _ Evaluator = (*SelfKeyword)(nil)

func (s *SelfKeyword) Body() hm.Expression { return s }

func (s *SelfKeyword) GetSourceLocation() *SourceLocation { return s.Loc }

func (s *SelfKeyword) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := env.GetDynamicScopeType()
	if t == nil {
		return nil, NewInferError(fmt.Errorf("'self' is not available in this context"), s)
	}
	s.SetInferredType(t)
	return t, nil
}

func (s *SelfKeyword) DeclaredSymbols() []string {
	return nil // self doesn't declare anything
}

func (s *SelfKeyword) ReferencedSymbols() []string {
	return nil // self doesn't reference a lexical symbol
}

func (s *SelfKeyword) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	if dynScope, ok := env.Self(); ok {
		return dynScope, nil
	}
	return nil, fmt.Errorf("'self' is not available in this context")
}

func (s *SelfKeyword) Walk(fn func(Node) bool) {
	fn(s)
}

// String represents a string literal
type String struct {
	InferredTypeHolder
	Value        string
	TripleQuoted bool // true if originally written with triple quotes
	Loc          *SourceLocation
}

var _ Node = (*String)(nil)
var _ Evaluator = (*String)(nil)

func (s *String) Body() hm.Expression { return s }

func (s *String) GetSourceLocation() *SourceLocation { return s.Loc }

func (s *String) ConstantType() hm.Type { return hm.NonNullType{Type: StringType} }

func (s *String) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := s.ConstantType()
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

func (s *String) Walk(fn func(Node) bool) {
	fn(s)
}

// TemplatePart is one segment of a Template: either a literal string chunk
// (Expr is nil) or an interpolated expression (Lit is empty).
type TemplatePart struct {
	Lit  string
	Expr Node
}

// Template represents a backtick-delimited template string literal with
// optional ${...} interpolations. Fence is the count of backticks used as the
// delimiter (1 for single-line, >=3 for multi-line). Lang is the optional
// language tag on multi-line templates (used for editor highlighting).
type Template struct {
	InferredTypeHolder
	Parts []TemplatePart
	Fence int
	Lang  string
	Loc   *SourceLocation
}

var _ Node = (*Template)(nil)
var _ Evaluator = (*Template)(nil)

func (t *Template) Body() hm.Expression { return t }

func (t *Template) GetSourceLocation() *SourceLocation { return t.Loc }

func (t *Template) ConstantType() hm.Type { return hm.NonNullType{Type: StringType} }

func (t *Template) IsLiteralOnly() bool {
	for _, p := range t.Parts {
		if p.Expr != nil {
			return false
		}
	}
	return true
}

func (t *Template) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	for _, p := range t.Parts {
		if p.Expr == nil {
			continue
		}
		if _, err := p.Expr.Infer(ctx, env, fresh); err != nil {
			return nil, err
		}
	}
	tt := t.ConstantType()
	t.SetInferredType(tt)
	return tt, nil
}

func (t *Template) DeclaredSymbols() []string { return nil }

func (t *Template) ReferencedSymbols() []string {
	var syms []string
	for _, p := range t.Parts {
		if p.Expr != nil {
			syms = append(syms, p.Expr.ReferencedSymbols()...)
		}
	}
	return syms
}

func (t *Template) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	var buf bytes.Buffer
	for i, p := range t.Parts {
		if p.Expr == nil {
			buf.WriteString(p.Lit)
			continue
		}
		val, err := EvalNode(ctx, env, p.Expr)
		if err != nil {
			return nil, fmt.Errorf("evaluating template part %d: %w", i, err)
		}
		if s, ok := val.(StringValue); ok {
			buf.WriteString(s.Val)
		} else {
			buf.WriteString(val.String())
		}
	}
	return StringValue{Val: buf.String()}, nil
}

func (t *Template) Walk(fn func(Node) bool) {
	if !fn(t) {
		return
	}
	for _, p := range t.Parts {
		if p.Expr != nil {
			p.Expr.Walk(fn)
		}
	}
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

func (b *Boolean) ConstantType() hm.Type { return hm.NonNullType{Type: BooleanType} }

func (b *Boolean) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := b.ConstantType()
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

func (b *Boolean) Walk(fn func(Node) bool) {
	fn(b)
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

func (i *Int) ConstantType() hm.Type { return hm.NonNullType{Type: IntType} }

func (i *Int) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := i.ConstantType()
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

func (i *Int) Walk(fn func(Node) bool) {
	fn(i)
}

// Float represents a floating-point literal
type Float struct {
	InferredTypeHolder
	Value float64
	Text  string // original source text, to preserve formatting
	Loc   *SourceLocation
}

var _ Node = (*Float)(nil)
var _ Evaluator = (*Float)(nil)

func (f *Float) Body() hm.Expression { return f }

func (f *Float) GetSourceLocation() *SourceLocation { return f.Loc }

func (f *Float) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	t := hm.NonNullType{Type: FloatType}
	f.SetInferredType(t)
	return t, nil
}

func (f *Float) DeclaredSymbols() []string {
	return nil // Float literals don't declare anything
}

func (f *Float) ReferencedSymbols() []string {
	return nil // Float literals don't reference anything
}

func (f *Float) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return FloatValue{Val: f.Value}, nil
}

func (f *Float) Walk(fn func(Node) bool) {
	fn(f)
}
