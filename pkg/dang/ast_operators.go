package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// OperatorType defines the category of binary operator
type OperatorType int

const (
	ArithmeticOp OperatorType = iota // Unifies types, returns unified type
	ComparisonOp                     // Validates types, returns Boolean
)

// BinaryOperatorEvaluator defines the evaluation function for specific operators
type BinaryOperatorEvaluator func(leftVal, rightVal Value) (Value, error)

// operandClass is the set of value categories an arithmetic operator accepts.
// It is the static shadow of what each EvalFunc supports at runtime, letting
// the type checker reject e.g. `"a" * "b"` at definition time rather than
// failing mid-evaluation.
type operandClass uint8

const (
	numericOperand operandClass = 1 << iota // Int and Float (which may mix)
	stringOperand                           // String, e.g. concatenation with +
	listOperand                             // [a], e.g. concatenation with +
)

func (c operandClass) has(x operandClass) bool { return c&x != 0 }

// BinaryOperator provides common functionality for binary operators
type BinaryOperator struct {
	InferredTypeHolder
	Left     Node
	Right    Node
	Loc      *SourceLocation
	OpType   OperatorType
	OpName   string
	Operands operandClass // accepted operand categories (arithmetic + ordering comparisons)
	EvalFunc BinaryOperatorEvaluator
}

// Common interface implementations
func (b *BinaryOperator) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (b *BinaryOperator) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, b.Left.ReferencedSymbols()...)
	symbols = append(symbols, b.Right.ReferencedSymbols()...)
	return symbols
}

func (b *BinaryOperator) Body() hm.Expression { return b }

func (b *BinaryOperator) GetSourceLocation() *SourceLocation { return b.Loc }

// unconstrainedVar reports whether t is a rigid (skolem) type variable
// (optionally wrapped in NonNull) — i.e. an author-written, universally
// quantified type parameter such as the `b` in `do(&yield: b): b`. Such a type
// must work for every possible type, so it supports no operations and operators
// that require a concrete type must reject it at definition time.
//
// A flexible inference variable (e.g. the element type of an empty list literal)
// is deliberately NOT rejected: unification is still free to resolve it.
func unconstrainedVar(t hm.Type) (hm.Type, bool) {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	switch tv := t.(type) {
	case hm.TypeVariable:
		return tv, tv.IsRigid()
	case hm.NullableTypeVariable:
		return tv, tv.IsRigid()
	default:
		return nil, false
	}
}

// Common type inference based on operator type
func (b *BinaryOperator) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(b, func() (hm.Type, error) {
		lt, err := b.Left.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		rt, err := b.Right.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		switch b.OpType {
		case ArithmeticOp:
			return b.resolveOperands(lt, rt)
		case ComparisonOp:
			// Ordering comparisons (< > <= >=) carry a domain just like
			// arithmetic and must reject non-orderable operands (e.g.
			// `"a" < "b"`). Equality (== !=) carries no domain — it is
			// deliberately defined across all types — so it skips the check.
			if b.Operands != 0 {
				if _, err := b.resolveOperands(lt, rt); err != nil {
					return nil, err
				}
			}
			return NonNullTypeNode{&NamedTypeNode{nil, "Boolean", b.Loc}}.Infer(ctx, env, fresh)
		default:
			return nil, fmt.Errorf("unknown operator type: %d", b.OpType)
		}
	})
}

// resolveOperands validates a binary operator's operands against its declared
// domain and returns the result type. Arithmetic callers use that type
// directly; ordering-comparison callers discard it and return Boolean. It
// rejects operands outside the domain (e.g. `"a" * "b"` or `"a" < "b"`) and
// allows Int and Float to mix, widening to Float when either side is Float —
// something the old "assign right to left" check rejected.
func (b *BinaryOperator) resolveOperands(lt, rt hm.Type) (hm.Type, error) {
	// An operator is not defined for an unconstrained generic type variable: a
	// universally-quantified type (e.g. the `b` in `do(&yield: b): b`) must work
	// for every possible type, so we cannot assume it is numeric/string/etc.
	// Reject at definition time rather than failing at the call site — without
	// typeclasses, a generic value can only be passed around, not operated on.
	if tv, ok := unconstrainedVar(lt); ok {
		return nil, fmt.Errorf("operator %s is not defined for the generic type %s: a type variable supports no operations", b.OpName, tv)
	}
	if tv, ok := unconstrainedVar(rt); ok {
		return nil, fmt.Errorf("operator %s is not defined for the generic type %s: a type variable supports no operations", b.OpName, tv)
	}

	// If either side is still an open inference variable, we cannot domain-check
	// yet; fall back to plain unification and let later constraints decide.
	// (Rigid signature variables were already rejected above.)
	if hasFreeVar(lt) || hasFreeVar(rt) {
		subs, err := hm.Assignable(rt, lt)
		if err != nil {
			return nil, err
		}
		return lt.Apply(subs).(hm.Type), nil
	}

	lb, lNonNull := stripNonNull(lt)
	rb, rNonNull := stripNonNull(rt)
	nonNull := lNonNull && rNonNull

	switch {
	case b.Operands.has(numericOperand) && isNumeric(lb) && isNumeric(rb):
		result := IntType
		if lb == FloatType || rb == FloatType {
			result = FloatType
		}
		return withNonNull(result, nonNull), nil
	case b.Operands.has(stringOperand) && lb == StringType && rb == StringType:
		return withNonNull(StringType, nonNull), nil
	case b.Operands.has(listOperand) && isList(lb) && isList(rb):
		// Both sides are lists; unify their element types.
		subs, err := hm.Assignable(rt, lt)
		if err != nil {
			return nil, err
		}
		return lt.Apply(subs).(hm.Type), nil
	}

	if !lt.Eq(rt) {
		return nil, withUnionProvenance(
			fmt.Errorf("operator %s is not defined between types %s and %s", b.OpName, lt, rt), lt, rt)
	}
	return nil, withUnionProvenance(
		fmt.Errorf("operator %s is not defined for type %s", b.OpName, lt), lt)
}

// stripNonNull unwraps a NonNullType, reporting whether the wrapper was present.
func stripNonNull(t hm.Type) (hm.Type, bool) {
	if nn, ok := t.(hm.NonNullType); ok {
		return nn.Type, true
	}
	return t, false
}

// withNonNull re-applies a NonNull wrapper when nonNull is true.
func withNonNull(t hm.Type, nonNull bool) hm.Type {
	if nonNull {
		return hm.NonNullType{Type: t}
	}
	return t
}

func isNumeric(t hm.Type) bool { return t == IntType || t == FloatType }

func isList(t hm.Type) bool {
	switch t.(type) {
	case ListType, GraphQLListType:
		return true
	default:
		return false
	}
}

func hasFreeVar(t hm.Type) bool { return len(t.FreeTypeVar()) > 0 }

// Common evaluation logic
func (b *BinaryOperator) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, b, func() (Value, error) {
		leftVal, err := EvalNode(ctx, scope, b.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}
		rightVal, err := EvalNode(ctx, scope, b.Right)
		if err != nil {
			return nil, fmt.Errorf("evaluating right side: %w", err)
		}

		return b.EvalFunc(leftVal, rightVal)
	})
}

// Evaluation functions for specific operators
func additionEval(leftVal, rightVal Value) (Value, error) {
	switch l := leftVal.(type) {
	case IntValue:
		switch r := rightVal.(type) {
		case IntValue:
			return IntValue{Val: l.Val + r.Val}, nil
		case FloatValue:
			return FloatValue{Val: float64(l.Val) + r.Val}, nil
		}
	case FloatValue:
		switch r := rightVal.(type) {
		case IntValue:
			return FloatValue{Val: l.Val + float64(r.Val)}, nil
		case FloatValue:
			return FloatValue{Val: l.Val + r.Val}, nil
		}
	case StringValue:
		if r, ok := rightVal.(StringValue); ok {
			return StringValue{Val: l.Val + r.Val}, nil
		}
	case ListValue:
		if r, ok := rightVal.(ListValue); ok {
			// Concatenate the lists
			combined := make([]Value, len(l.Elements)+len(r.Elements))
			copy(combined, l.Elements)
			copy(combined[len(l.Elements):], r.Elements)

			// Use the element type from the left operand, or right if left is empty
			elemType := l.ElemType
			if len(l.Elements) == 0 && len(r.Elements) > 0 {
				elemType = r.ElemType
			}

			return ListValue{Elements: combined, ElemType: elemType}, nil
		}
	}
	return nil, fmt.Errorf("addition not supported for types %T and %T", leftVal, rightVal)
}

func subtractionEval(leftVal, rightVal Value) (Value, error) {
	switch l := leftVal.(type) {
	case IntValue:
		switch r := rightVal.(type) {
		case IntValue:
			return IntValue{Val: l.Val - r.Val}, nil
		case FloatValue:
			return FloatValue{Val: float64(l.Val) - r.Val}, nil
		}
	case FloatValue:
		switch r := rightVal.(type) {
		case IntValue:
			return FloatValue{Val: l.Val - float64(r.Val)}, nil
		case FloatValue:
			return FloatValue{Val: l.Val - r.Val}, nil
		}
	}
	return nil, fmt.Errorf("subtraction not supported for types %T and %T", leftVal, rightVal)
}

func multiplicationEval(leftVal, rightVal Value) (Value, error) {
	switch l := leftVal.(type) {
	case IntValue:
		switch r := rightVal.(type) {
		case IntValue:
			return IntValue{Val: l.Val * r.Val}, nil
		case FloatValue:
			return FloatValue{Val: float64(l.Val) * r.Val}, nil
		}
	case FloatValue:
		switch r := rightVal.(type) {
		case IntValue:
			return FloatValue{Val: l.Val * float64(r.Val)}, nil
		case FloatValue:
			return FloatValue{Val: l.Val * r.Val}, nil
		}
	}
	return nil, fmt.Errorf("multiplication not supported for types %T and %T", leftVal, rightVal)
}

func divisionEval(leftVal, rightVal Value) (Value, error) {
	switch l := leftVal.(type) {
	case IntValue:
		switch r := rightVal.(type) {
		case IntValue:
			if r.Val == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return IntValue{Val: l.Val / r.Val}, nil
		case FloatValue:
			if r.Val == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return FloatValue{Val: float64(l.Val) / r.Val}, nil
		}
	case FloatValue:
		switch r := rightVal.(type) {
		case IntValue:
			if r.Val == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return FloatValue{Val: l.Val / float64(r.Val)}, nil
		case FloatValue:
			if r.Val == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return FloatValue{Val: l.Val / r.Val}, nil
		}
	}
	return nil, fmt.Errorf("division not supported for types %T and %T", leftVal, rightVal)
}

func moduloEval(leftVal, rightVal Value) (Value, error) {
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			if r.Val == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			return IntValue{Val: l.Val % r.Val}, nil
		}
	}
	return nil, fmt.Errorf("modulo not supported for types %T and %T", leftVal, rightVal)
}

func inequalityEval(leftVal, rightVal Value) (Value, error) {
	// Compare the values
	equal := valuesEqual(leftVal, rightVal)
	return BoolValue{Val: !equal}, nil
}

func lessThanEval(leftVal, rightVal Value) (Value, error) {
	switch lv := leftVal.(type) {
	case IntValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val < rv.Val}, nil
		case FloatValue:
			return BoolValue{Val: float64(lv.Val) < rv.Val}, nil
		}
	case FloatValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val < float64(rv.Val)}, nil
		case FloatValue:
			return BoolValue{Val: lv.Val < rv.Val}, nil
		}
	case StringValue:
		if rv, ok := rightVal.(StringValue); ok {
			return BoolValue{Val: lv.Val < rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than comparison not supported for types %T and %T", leftVal, rightVal)
}

func greaterThanEval(leftVal, rightVal Value) (Value, error) {
	switch lv := leftVal.(type) {
	case IntValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val > rv.Val}, nil
		case FloatValue:
			return BoolValue{Val: float64(lv.Val) > rv.Val}, nil
		}
	case FloatValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val > float64(rv.Val)}, nil
		case FloatValue:
			return BoolValue{Val: lv.Val > rv.Val}, nil
		}
	case StringValue:
		if rv, ok := rightVal.(StringValue); ok {
			return BoolValue{Val: lv.Val > rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("greater than comparison not supported for types %T and %T", leftVal, rightVal)
}

func lessThanEqualEval(leftVal, rightVal Value) (Value, error) {
	switch lv := leftVal.(type) {
	case IntValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val <= rv.Val}, nil
		case FloatValue:
			return BoolValue{Val: float64(lv.Val) <= rv.Val}, nil
		}
	case FloatValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val <= float64(rv.Val)}, nil
		case FloatValue:
			return BoolValue{Val: lv.Val <= rv.Val}, nil
		}
	case StringValue:
		if rv, ok := rightVal.(StringValue); ok {
			return BoolValue{Val: lv.Val <= rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than or equal comparison not supported for types %T and %T", leftVal, rightVal)
}

func greaterThanEqualEval(leftVal, rightVal Value) (Value, error) {
	switch lv := leftVal.(type) {
	case IntValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val >= rv.Val}, nil
		case FloatValue:
			return BoolValue{Val: float64(lv.Val) >= rv.Val}, nil
		}
	case FloatValue:
		switch rv := rightVal.(type) {
		case IntValue:
			return BoolValue{Val: lv.Val >= float64(rv.Val)}, nil
		case FloatValue:
			return BoolValue{Val: lv.Val >= rv.Val}, nil
		}
	case StringValue:
		if rv, ok := rightVal.(StringValue); ok {
			return BoolValue{Val: lv.Val >= rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("greater than or equal comparison not supported for types %T and %T", leftVal, rightVal)
}

type Default struct {
	InferredTypeHolder
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = (*Default)(nil)
var _ Evaluator = (*Default)(nil)

func (d *Default) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(d, func() (hm.Type, error) {
		lt, err := d.Left.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		rt, err := d.Right.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// For the default operator, the left side can be nullable and the right side
		// provides the fallback value. We need to unify the non-null version of the
		// left type with the right type.

		// Unify types with subtyping support for nullable/NonNull compatibility
		subs, err := hm.Assignable(rt, lt)
		if err != nil {
			return nil, err
		}

		// Return the right type (the fallback value type) with substitutions applied
		return rt.Apply(subs).(hm.Type), nil
	})
}

func (d *Default) DeclaredSymbols() []string {
	return nil // Default operator doesn't declare anything
}

func (d *Default) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, d.Left.ReferencedSymbols()...)
	symbols = append(symbols, d.Right.ReferencedSymbols()...)
	return symbols
}

func (d *Default) Body() hm.Expression { return d }

func (d *Default) GetSourceLocation() *SourceLocation { return d.Loc }

func (d *Default) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, d, func() (Value, error) {
		leftVal, err := EvalNode(ctx, scope, d.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}

		// Check if left value is null
		if _, isNull := leftVal.(NullValue); isNull {
			// Use the right side as default
			return EvalNode(ctx, scope, d.Right)
		}

		return leftVal, nil
	})
}

func (d *Default) Walk(fn func(Node) bool) {
	if !fn(d) {
		return
	}
	d.Left.Walk(fn)
	d.Right.Walk(fn)
}

type Equality struct {
	InferredTypeHolder
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = (*Equality)(nil)
var _ Evaluator = (*Equality)(nil)

func (e *Equality) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(e, func() (hm.Type, error) {
		// Type check both sides for validity, but allow cross-type comparison at runtime
		_, err := e.Left.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		_, err = e.Right.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Equality always returns a boolean
		return NonNullTypeNode{&NamedTypeNode{nil, "Boolean", e.Loc}}.Infer(ctx, env, fresh)
	})
}

func (e *Equality) DeclaredSymbols() []string {
	return nil // Equality operator doesn't declare anything
}

func (e *Equality) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, e.Left.ReferencedSymbols()...)
	symbols = append(symbols, e.Right.ReferencedSymbols()...)
	return symbols
}

func (e *Equality) Body() hm.Expression { return e }

func (e *Equality) GetSourceLocation() *SourceLocation { return e.Loc }

func (e *Equality) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, e, func() (Value, error) {
		leftVal, err := EvalNode(ctx, scope, e.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}

		rightVal, err := EvalNode(ctx, scope, e.Right)
		if err != nil {
			return nil, fmt.Errorf("evaluating right side: %w", err)
		}

		// Compare the values
		equal := valuesEqual(leftVal, rightVal)
		return BoolValue{Val: equal}, nil
	})
}

func (e *Equality) Walk(fn func(Node) bool) {
	if !fn(e) {
		return
	}
	e.Left.Walk(fn)
	e.Right.Walk(fn)
}

type Addition struct {
	BinaryOperator
}

var _ Node = (*Addition)(nil)
var _ Evaluator = (*Addition)(nil)

func NewAddition(left, right Node, loc *SourceLocation) *Addition {
	return &Addition{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ArithmeticOp,
			OpName:   "addition",
			Operands: numericOperand | stringOperand | listOperand,
			EvalFunc: additionEval,
		},
	}
}

func (a *Addition) Walk(fn func(Node) bool) {
	if !fn(a) {
		return
	}
	a.Left.Walk(fn)
	a.Right.Walk(fn)
}

type Subtraction struct {
	BinaryOperator
}

var _ Node = (*Subtraction)(nil)
var _ Evaluator = (*Subtraction)(nil)

func NewSubtraction(left, right Node, loc *SourceLocation) *Subtraction {
	return &Subtraction{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ArithmeticOp,
			OpName:   "subtraction",
			Operands: numericOperand,
			EvalFunc: subtractionEval,
		},
	}
}

func (s *Subtraction) Walk(fn func(Node) bool) {
	if !fn(s) {
		return
	}
	s.Left.Walk(fn)
	s.Right.Walk(fn)
}

type Multiplication struct {
	BinaryOperator
}

var _ Node = (*Multiplication)(nil)
var _ Evaluator = (*Multiplication)(nil)

func NewMultiplication(left, right Node, loc *SourceLocation) *Multiplication {
	return &Multiplication{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ArithmeticOp,
			OpName:   "multiplication",
			Operands: numericOperand,
			EvalFunc: multiplicationEval,
		},
	}
}

func (m *Multiplication) Walk(fn func(Node) bool) {
	if !fn(m) {
		return
	}
	m.Left.Walk(fn)
	m.Right.Walk(fn)
}

type Division struct {
	BinaryOperator
}

var _ Node = (*Division)(nil)
var _ Evaluator = (*Division)(nil)

func NewDivision(left, right Node, loc *SourceLocation) *Division {
	return &Division{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ArithmeticOp,
			OpName:   "division",
			Operands: numericOperand,
			EvalFunc: divisionEval,
		},
	}
}

func (d *Division) Walk(fn func(Node) bool) {
	if !fn(d) {
		return
	}
	d.Left.Walk(fn)
	d.Right.Walk(fn)
}

type Modulo struct {
	BinaryOperator
}

var _ Node = (*Modulo)(nil)
var _ Evaluator = (*Modulo)(nil)

func NewModulo(left, right Node, loc *SourceLocation) *Modulo {
	return &Modulo{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ArithmeticOp,
			OpName:   "modulo",
			Operands: numericOperand,
			EvalFunc: moduloEval,
		},
	}
}

func (m *Modulo) Walk(fn func(Node) bool) {
	if !fn(m) {
		return
	}
	m.Left.Walk(fn)
	m.Right.Walk(fn)
}

type Inequality struct {
	BinaryOperator
}

var _ Node = (*Inequality)(nil)
var _ Evaluator = (*Inequality)(nil)

func NewInequality(left, right Node, loc *SourceLocation) *Inequality {
	return &Inequality{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ComparisonOp,
			OpName:   "inequality",
			EvalFunc: inequalityEval,
		},
	}
}

func (i *Inequality) Walk(fn func(Node) bool) {
	if !fn(i) {
		return
	}
	i.Left.Walk(fn)
	i.Right.Walk(fn)
}

type LessThan struct {
	BinaryOperator
}

var _ Node = (*LessThan)(nil)
var _ Evaluator = (*LessThan)(nil)

func NewLessThan(left, right Node, loc *SourceLocation) *LessThan {
	return &LessThan{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ComparisonOp,
			OpName:   "less_than",
			Operands: numericOperand | stringOperand,
			EvalFunc: lessThanEval,
		},
	}
}

func (l *LessThan) Walk(fn func(Node) bool) {
	if !fn(l) {
		return
	}
	l.Left.Walk(fn)
	l.Right.Walk(fn)
}

type GreaterThan struct {
	BinaryOperator
}

var _ Node = (*GreaterThan)(nil)
var _ Evaluator = (*GreaterThan)(nil)

func NewGreaterThan(left, right Node, loc *SourceLocation) *GreaterThan {
	return &GreaterThan{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ComparisonOp,
			OpName:   "greater_than",
			Operands: numericOperand | stringOperand,
			EvalFunc: greaterThanEval,
		},
	}
}

func (g *GreaterThan) Walk(fn func(Node) bool) {
	if !fn(g) {
		return
	}
	g.Left.Walk(fn)
	g.Right.Walk(fn)
}

type LessThanEqual struct {
	BinaryOperator
}

var _ Node = (*LessThanEqual)(nil)
var _ Evaluator = (*LessThanEqual)(nil)

func NewLessThanEqual(left, right Node, loc *SourceLocation) *LessThanEqual {
	return &LessThanEqual{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ComparisonOp,
			OpName:   "less_than_equal",
			Operands: numericOperand | stringOperand,
			EvalFunc: lessThanEqualEval,
		},
	}
}

func (l *LessThanEqual) Walk(fn func(Node) bool) {
	if !fn(l) {
		return
	}
	l.Left.Walk(fn)
	l.Right.Walk(fn)
}

type GreaterThanEqual struct {
	BinaryOperator
}

var _ Node = (*GreaterThanEqual)(nil)
var _ Evaluator = (*GreaterThanEqual)(nil)

func NewGreaterThanEqual(left, right Node, loc *SourceLocation) *GreaterThanEqual {
	return &GreaterThanEqual{
		BinaryOperator: BinaryOperator{
			Left:     left,
			Right:    right,
			Loc:      loc,
			OpType:   ComparisonOp,
			OpName:   "greater_than_equal",
			Operands: numericOperand | stringOperand,
			EvalFunc: greaterThanEqualEval,
		},
	}
}

func (g *GreaterThanEqual) Walk(fn func(Node) bool) {
	if !fn(g) {
		return
	}
	g.Left.Walk(fn)
	g.Right.Walk(fn)
}

type UnaryNegation struct {
	InferredTypeHolder
	Expr Node
	Loc  *SourceLocation
}

var _ Node = (*UnaryNegation)(nil)
var _ Evaluator = (*UnaryNegation)(nil)

func (u *UnaryNegation) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(u, func() (hm.Type, error) {
		exprType, err := u.Expr.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Verify that the expression is of type Boolean
		boolType, err := NonNullTypeNode{&NamedTypeNode{nil, "Boolean", u.Loc}}.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		_, err = hm.Assignable(exprType, boolType)
		if err != nil {
			return nil, fmt.Errorf("unary negation requires Boolean type, got %s", exprType)
		}

		// Return Boolean type
		return boolType, nil
	})
}

func (u *UnaryNegation) DeclaredSymbols() []string {
	return nil
}

func (u *UnaryNegation) ReferencedSymbols() []string {
	return u.Expr.ReferencedSymbols()
}

func (u *UnaryNegation) Body() hm.Expression { return u }

func (u *UnaryNegation) GetSourceLocation() *SourceLocation { return u.Loc }

func (u *UnaryNegation) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, u, func() (Value, error) {
		val, err := EvalNode(ctx, scope, u.Expr)
		if err != nil {
			return nil, fmt.Errorf("evaluating expression: %w", err)
		}

		if boolVal, ok := val.(BoolValue); ok {
			return BoolValue{Val: !boolVal.Val}, nil
		}

		return nil, fmt.Errorf("unary negation requires Boolean value, got %T", val)
	})
}

func (u *UnaryNegation) Walk(fn func(Node) bool) {
	if !fn(u) {
		return
	}
	u.Expr.Walk(fn)
}

// UnaryMinus represents the -expr arithmetic negation operator.
type UnaryMinus struct {
	InferredTypeHolder
	Expr Node
	Loc  *SourceLocation
}

var _ Node = (*UnaryMinus)(nil)
var _ Evaluator = (*UnaryMinus)(nil)

func (u *UnaryMinus) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(u, func() (hm.Type, error) {
		exprType, err := u.Expr.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		nonNull, ok := exprType.(hm.NonNullType)
		if !ok {
			return nil, fmt.Errorf("unary minus requires Int or Float, got %s", exprType)
		}
		if nonNull.Type != IntType && nonNull.Type != FloatType {
			return nil, fmt.Errorf("unary minus requires Int or Float, got %s", exprType)
		}
		return exprType, nil
	})
}

func (u *UnaryMinus) DeclaredSymbols() []string {
	return nil
}

func (u *UnaryMinus) ReferencedSymbols() []string {
	return u.Expr.ReferencedSymbols()
}

func (u *UnaryMinus) Body() hm.Expression { return u }

func (u *UnaryMinus) GetSourceLocation() *SourceLocation { return u.Loc }

func (u *UnaryMinus) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, u, func() (Value, error) {
		val, err := EvalNode(ctx, scope, u.Expr)
		if err != nil {
			return nil, fmt.Errorf("evaluating expression: %w", err)
		}

		switch v := val.(type) {
		case IntValue:
			return IntValue{Val: -v.Val}, nil
		case FloatValue:
			return FloatValue{Val: -v.Val}, nil
		}
		return nil, fmt.Errorf("unary minus requires Int or Float value, got %T", val)
	})
}

func (u *UnaryMinus) Walk(fn func(Node) bool) {
	if !fn(u) {
		return
	}
	u.Expr.Walk(fn)
}

// NonNullAssert represents the postfix `expr!` operator, which asserts that
// the operand is non-null. At the type level it narrows `T` to `T!`; at
// runtime it raises if the value turns out to be null.
type NonNullAssert struct {
	InferredTypeHolder
	Expr Node
	Loc  *SourceLocation
}

var _ Node = (*NonNullAssert)(nil)
var _ Evaluator = (*NonNullAssert)(nil)

func (n *NonNullAssert) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(n, func() (hm.Type, error) {
		t, err := n.Expr.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Strip the outer nullability. If the type is already non-null the
		// assertion is a no-op and we keep the type as-is.
		if nn, ok := t.(hm.NonNullType); ok {
			n.SetInferredType(nn)
			return nn, nil
		}

		result := hm.NonNullType{Type: t}
		n.SetInferredType(result)
		return result, nil
	})
}

func (n *NonNullAssert) DeclaredSymbols() []string {
	return nil
}

func (n *NonNullAssert) ReferencedSymbols() []string {
	return n.Expr.ReferencedSymbols()
}

func (n *NonNullAssert) Body() hm.Expression { return n }

func (n *NonNullAssert) GetSourceLocation() *SourceLocation { return n.Loc }

func (n *NonNullAssert) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, n, func() (Value, error) {
		val, err := EvalNode(ctx, scope, n.Expr)
		if err != nil {
			return nil, err
		}

		if _, isNull := val.(NullValue); isNull {
			return nil, fmt.Errorf("non-null assertion failed: value is null")
		}

		return val, nil
	})
}

func (n *NonNullAssert) Walk(fn func(Node) bool) {
	if !fn(n) {
		return
	}
	n.Expr.Walk(fn)
}

// FunctionRef represents the &foo function-reference operator.
type FunctionRef struct {
	InferredTypeHolder
	Expr Node
	Loc  *SourceLocation
}

var _ Node = (*FunctionRef)(nil)
var _ Evaluator = (*FunctionRef)(nil)

func (f *FunctionRef) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(f, func() (hm.Type, error) {
		t, err := inferNodeWithoutAutoCall(ctx, env, fresh, f.Expr)
		if err != nil {
			return nil, err
		}

		if _, ok := t.(*hm.FunctionType); !ok {
			return nil, NewInferError(fmt.Errorf("& requires a function, got %s", t.Name()), f.Expr)
		}

		f.SetInferredType(t)
		return t, nil
	})
}

func (f *FunctionRef) DeclaredSymbols() []string {
	return nil
}

func (f *FunctionRef) ReferencedSymbols() []string {
	return f.Expr.ReferencedSymbols()
}

func (f *FunctionRef) Body() hm.Expression { return f }

func (f *FunctionRef) GetSourceLocation() *SourceLocation { return f.Loc }

func (f *FunctionRef) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, f, func() (Value, error) {
		val, err := evalNodeWithoutAutoCall(ctx, scope, f.Expr)
		if err != nil {
			return nil, err
		}

		if _, ok := val.(Callable); !ok {
			return nil, fmt.Errorf("& requires a function, got %T", val)
		}

		return val, nil
	})
}

func (f *FunctionRef) Walk(fn func(Node) bool) {
	if !fn(f) {
		return
	}
	f.Expr.Walk(fn)
}

// LogicalAnd represents the 'and' operator with short-circuit evaluation
type LogicalAnd struct {
	InferredTypeHolder
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = (*LogicalAnd)(nil)
var _ Evaluator = (*LogicalAnd)(nil)

func (l *LogicalAnd) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(l, func() (hm.Type, error) {
		// Both sides must be Boolean
		leftType, err := l.Left.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		rightType, err := l.Right.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		boolType, err := NonNullTypeNode{&NamedTypeNode{nil, "Boolean", l.Loc}}.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Verify left side is Boolean
		_, err = hm.Assignable(leftType, boolType)
		if err != nil {
			return nil, fmt.Errorf("logical 'and' left operand must be Boolean, got %s", leftType)
		}

		// Verify right side is Boolean
		_, err = hm.Assignable(rightType, boolType)
		if err != nil {
			return nil, fmt.Errorf("logical 'and' right operand must be Boolean, got %s", rightType)
		}

		return boolType, nil
	})
}

func (l *LogicalAnd) DeclaredSymbols() []string {
	return nil
}

func (l *LogicalAnd) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, l.Left.ReferencedSymbols()...)
	symbols = append(symbols, l.Right.ReferencedSymbols()...)
	return symbols
}

func (l *LogicalAnd) Body() hm.Expression { return l }

func (l *LogicalAnd) GetSourceLocation() *SourceLocation { return l.Loc }

func (l *LogicalAnd) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, l, func() (Value, error) {
		// Evaluate left side first
		leftVal, err := EvalNode(ctx, scope, l.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}

		leftBool, ok := leftVal.(BoolValue)
		if !ok {
			return nil, fmt.Errorf("logical 'and' requires Boolean operands, left side is %T", leftVal)
		}

		// Short-circuit: if left is false, return false without evaluating right
		if !leftBool.Val {
			return BoolValue{Val: false}, nil
		}

		// Left is true, evaluate right side
		rightVal, err := EvalNode(ctx, scope, l.Right)
		if err != nil {
			return nil, fmt.Errorf("evaluating right side: %w", err)
		}

		rightBool, ok := rightVal.(BoolValue)
		if !ok {
			return nil, fmt.Errorf("logical 'and' requires Boolean operands, right side is %T", rightVal)
		}

		return BoolValue{Val: rightBool.Val}, nil
	})
}

func (l *LogicalAnd) Walk(fn func(Node) bool) {
	if !fn(l) {
		return
	}
	l.Left.Walk(fn)
	l.Right.Walk(fn)
}

// LogicalOr represents the 'or' operator with short-circuit evaluation
type LogicalOr struct {
	InferredTypeHolder
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = (*LogicalOr)(nil)
var _ Evaluator = (*LogicalOr)(nil)

func (l *LogicalOr) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(l, func() (hm.Type, error) {
		// Both sides must be Boolean
		leftType, err := l.Left.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}
		rightType, err := l.Right.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		boolType, err := NonNullTypeNode{&NamedTypeNode{nil, "Boolean", l.Loc}}.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		// Verify left side is Boolean
		_, err = hm.Assignable(leftType, boolType)
		if err != nil {
			return nil, fmt.Errorf("logical 'or' left operand must be Boolean, got %s", leftType)
		}

		// Verify right side is Boolean
		_, err = hm.Assignable(rightType, boolType)
		if err != nil {
			return nil, fmt.Errorf("logical 'or' right operand must be Boolean, got %s", rightType)
		}

		return boolType, nil
	})
}

func (l *LogicalOr) DeclaredSymbols() []string {
	return nil
}

func (l *LogicalOr) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, l.Left.ReferencedSymbols()...)
	symbols = append(symbols, l.Right.ReferencedSymbols()...)
	return symbols
}

func (l *LogicalOr) Body() hm.Expression { return l }

func (l *LogicalOr) GetSourceLocation() *SourceLocation { return l.Loc }

func (l *LogicalOr) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, l, func() (Value, error) {
		// Evaluate left side first
		leftVal, err := EvalNode(ctx, scope, l.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}

		leftBool, ok := leftVal.(BoolValue)
		if !ok {
			return nil, fmt.Errorf("logical 'or' requires Boolean operands, left side is %T", leftVal)
		}

		// Short-circuit: if left is true, return true without evaluating right
		if leftBool.Val {
			return BoolValue{Val: true}, nil
		}

		// Left is false, evaluate right side
		rightVal, err := EvalNode(ctx, scope, l.Right)
		if err != nil {
			return nil, fmt.Errorf("evaluating right side: %w", err)
		}

		rightBool, ok := rightVal.(BoolValue)
		if !ok {
			return nil, fmt.Errorf("logical 'or' requires Boolean operands, right side is %T", rightVal)
		}

		return BoolValue{Val: rightBool.Val}, nil
	})
}

func (l *LogicalOr) Walk(fn func(Node) bool) {
	if !fn(l) {
		return
	}
	l.Left.Walk(fn)
	l.Right.Walk(fn)
}
