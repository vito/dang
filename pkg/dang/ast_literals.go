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
	// NullType    = NewObject("Null")

	IDType         = NewType("ID", ScalarKind)
	BooleanType    = NewType("Boolean", ScalarKind)
	StringType     = NewType("String", ScalarKind)
	IntType        = NewType("Int", ScalarKind)
	FloatType      = NewType("Float", ScalarKind)
	ListTypeModule = NewType("List", ScalarKind)
	MapTypeModule  = NewType("Map", ScalarKind)
	ErrorType      = NewType("Error", InterfaceKind)
	BasicErrorType = NewType("BasicError", ObjectKind)
)

// Constant is implemented by nodes whose type can be determined without
// inspecting the surrounding environment (e.g. string, int, and boolean
// literals).  FieldDecl.Hoist uses this to register field types early so
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
	if len(l.Elements) == 0 {
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

func (l *List) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	if len(l.Elements) == 0 {
		return ListValue{Elements: []Value{}, ElemType: hm.TypeVariable('a')}, nil
	}

	values := make([]Value, len(l.Elements))
	var elemType hm.Type

	for i, elem := range l.Elements {
		val, err := EvalNode(ctx, scope, elem)
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

// MapEntry is a single key: value pair in a map literal. Keys are arbitrary
// expressions constrained to String! during inference.
type MapEntry struct {
	Key   Node
	Value Node
}

// MapLiteral represents a map literal like ["a": 1, "b": 2] or the empty [:].
type MapLiteral struct {
	InferredTypeHolder
	Entries []MapEntry
	Loc     *SourceLocation
}

var _ Node = (*MapLiteral)(nil)
var _ Evaluator = (*MapLiteral)(nil)

func (m *MapLiteral) Infer(ctx context.Context, env hm.Env, f hm.Fresher) (hm.Type, error) {
	if len(m.Entries) == 0 {
		tv := f.Fresh()
		t := hm.NonNullType{Type: MapType{tv}}
		m.SetInferredType(t)
		return t, nil
	}

	wantKey := hm.NonNullType{Type: StringType}
	var valType hm.Type
	for i, entry := range m.Entries {
		kt, err := entry.Key.Infer(ctx, env, f)
		if err != nil {
			return nil, err
		}
		if _, err := hm.Assignable(kt, wantKey); err != nil {
			return nil, NewInferError(fmt.Errorf("map key must be String!, got %s", kt), entry.Key)
		}

		vt, err := entry.Value.Infer(ctx, env, f)
		if err != nil {
			return nil, err
		}
		if valType == nil {
			valType = vt
		} else {
			merged, _, err := hm.MergeTypes(valType, vt)
			if err != nil {
				return nil, NewInferError(fmt.Errorf("unify entry %d: no common value type between %s and %s", i, vt, valType), m.Entries[i].Value)
			}
			valType = merged
		}
	}
	mapType := hm.NonNullType{Type: MapType{valType}}
	m.SetInferredType(mapType)
	return mapType, nil
}

func (m *MapLiteral) DeclaredSymbols() []string { return nil }

func (m *MapLiteral) ReferencedSymbols() []string {
	var symbols []string
	for _, entry := range m.Entries {
		symbols = append(symbols, entry.Key.ReferencedSymbols()...)
		symbols = append(symbols, entry.Value.ReferencedSymbols()...)
	}
	return symbols
}

func (m *MapLiteral) Body() hm.Expression { return m }

func (m *MapLiteral) GetSourceLocation() *SourceLocation { return m.Loc }

func (m *MapLiteral) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	keys := make([]string, 0, len(m.Entries))
	entries := make(map[string]Value, len(m.Entries))
	var valType hm.Type

	for i, entry := range m.Entries {
		keyVal, err := EvalNode(ctx, scope, entry.Key)
		if err != nil {
			return nil, fmt.Errorf("evaluating map key %d: %w", i, err)
		}
		keyStr, ok := keyVal.(StringValue)
		if !ok {
			return nil, fmt.Errorf("map key %d must be a string, got %s", i, keyVal.Type())
		}

		val, err := EvalNode(ctx, scope, entry.Value)
		if err != nil {
			return nil, fmt.Errorf("evaluating map value for key %q: %w", keyStr.Val, err)
		}
		if _, exists := entries[keyStr.Val]; !exists {
			keys = append(keys, keyStr.Val)
		}
		entries[keyStr.Val] = val
		if valType == nil {
			valType = val.Type()
		}
	}

	if valType == nil {
		valType = hm.TypeVariable('a')
	}
	return MapValue{Keys: keys, Entries: entries, ValType: valType}, nil
}

func (m *MapLiteral) Walk(fn func(Node) bool) {
	if !fn(m) {
		return
	}
	for _, entry := range m.Entries {
		entry.Key.Walk(fn)
		entry.Value.Walk(fn)
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

func (n *Null) Eval(ctx context.Context, _ ValueScope) (Value, error) {
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

func (s *SelfKeyword) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	if dynScope, ok := scope.Self(); ok {
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

func (s *String) Eval(ctx context.Context, scope ValueScope) (Value, error) {
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

func (t *Template) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	var buf bytes.Buffer
	for i, p := range t.Parts {
		if p.Expr == nil {
			buf.WriteString(p.Lit)
			continue
		}
		val, err := EvalNode(ctx, scope, p.Expr)
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

func (b *Boolean) Eval(ctx context.Context, _ ValueScope) (Value, error) {
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

func (i *Int) Eval(ctx context.Context, _ ValueScope) (Value, error) {
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

func (f *Float) Eval(ctx context.Context, _ ValueScope) (Value, error) {
	return FloatValue{Val: f.Value}, nil
}

func (f *Float) Walk(fn func(Node) bool) {
	fn(f)
}
