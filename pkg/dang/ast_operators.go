package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// OperatorType defines the category of binary operator
type OperatorType int

const (
	ArithmeticOp OperatorType = iota // Unifies types, returns unified type
	ComparisonOp                     // Validates types, returns Boolean
)

// BinaryOperatorEvaluator defines the evaluation function for specific operators
type BinaryOperatorEvaluator func(leftVal, rightVal Value) (Value, error)

// BinaryOperator provides common functionality for binary operators
type BinaryOperator struct {
	InferredTypeHolder
	Left     Node
	Right    Node
	Loc      *SourceLocation
	OpType   OperatorType
	OpName   string
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
			// Unify types and return the unified type
			subs, err := hm.Unify(lt, rt)
			if err != nil {
				return nil, err
			}
			return lt.Apply(subs).(hm.Type), nil
		case ComparisonOp:
			// Validate types but always return Boolean
			return NonNullTypeNode{&NamedTypeNode{"Boolean", b.Loc}}.Infer(ctx, env, fresh)
		default:
			return nil, fmt.Errorf("unknown operator type: %d", b.OpType)
		}
	})
}

// Common evaluation logic
func (b *BinaryOperator) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, b, func() (Value, error) {
		leftVal, err := EvalNode(ctx, env, b.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}
		rightVal, err := EvalNode(ctx, env, b.Right)
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
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val + r.Val}, nil
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
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val - r.Val}, nil
		}
	}
	return nil, fmt.Errorf("subtraction not supported for types %T and %T", leftVal, rightVal)
}

func multiplicationEval(leftVal, rightVal Value) (Value, error) {
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val * r.Val}, nil
		}
	}
	return nil, fmt.Errorf("multiplication not supported for types %T and %T", leftVal, rightVal)
}

func divisionEval(leftVal, rightVal Value) (Value, error) {
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			if r.Val == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return IntValue{Val: l.Val / r.Val}, nil
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
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val < rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than comparison not supported for types %T and %T", leftVal, rightVal)
}

func greaterThanEval(leftVal, rightVal Value) (Value, error) {
	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val > rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("greater than comparison not supported for types %T and %T", leftVal, rightVal)
}

func lessThanEqualEval(leftVal, rightVal Value) (Value, error) {
	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val <= rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than or equal comparison not supported for types %T and %T", leftVal, rightVal)
}

func greaterThanEqualEval(leftVal, rightVal Value) (Value, error) {
	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
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
		subs, err := hm.Unify(lt, rt)
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

func (d *Default) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, d, func() (Value, error) {
		leftVal, err := EvalNode(ctx, env, d.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}

		// Check if left value is null
		if _, isNull := leftVal.(NullValue); isNull {
			// Use the right side as default
			return EvalNode(ctx, env, d.Right)
		}

		return leftVal, nil
	})
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
		return NonNullTypeNode{&NamedTypeNode{"Boolean", e.Loc}}.Infer(ctx, env, fresh)
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

func (e *Equality) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, e, func() (Value, error) {
		leftVal, err := EvalNode(ctx, env, e.Left)
		if err != nil {
			return nil, fmt.Errorf("evaluating left side: %w", err)
		}

		rightVal, err := EvalNode(ctx, env, e.Right)
		if err != nil {
			return nil, fmt.Errorf("evaluating right side: %w", err)
		}

		// Compare the values
		equal := valuesEqual(leftVal, rightVal)
		return BoolValue{Val: equal}, nil
	})
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
			EvalFunc: additionEval,
		},
	}
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
			EvalFunc: subtractionEval,
		},
	}
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
			EvalFunc: multiplicationEval,
		},
	}
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
			EvalFunc: divisionEval,
		},
	}
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
			EvalFunc: moduloEval,
		},
	}
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
			EvalFunc: lessThanEval,
		},
	}
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
			EvalFunc: greaterThanEval,
		},
	}
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
			EvalFunc: lessThanEqualEval,
		},
	}
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
			EvalFunc: greaterThanEqualEval,
		},
	}
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
		boolType, err := NonNullTypeNode{&NamedTypeNode{"Boolean", u.Loc}}.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		_, err = hm.Unify(exprType, boolType)
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

func (u *UnaryNegation) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, u, func() (Value, error) {
		val, err := EvalNode(ctx, env, u.Expr)
		if err != nil {
			return nil, fmt.Errorf("evaluating expression: %w", err)
		}

		if boolVal, ok := val.(BoolValue); ok {
			return BoolValue{Val: !boolVal.Val}, nil
		}

		return nil, fmt.Errorf("unary negation requires Boolean value, got %T", val)
	})
}
