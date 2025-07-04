package bind

import (
	"context"
	"fmt"

	"github.com/vito/bind/pkg/hm"
)

type Default struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Default{}
var _ Evaluator = Default{}

func (d Default) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := d.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := d.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// For the default operator, the left side can be nullable and the right side
	// provides the fallback value. We need to unify the non-null version of the
	// left type with the right type.

	// Unify types with subtyping support for nullable/NonNull compatibility
	if _, err := hm.Unify(lt, rt); err != nil {
		return nil, WrapInferError(err, d)
	}

	// Return the right type (the fallback value type)
	return rt, nil
}

func (d Default) DeclaredSymbols() []string {
	return nil // Default operator doesn't declare anything
}

func (d Default) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, d.Left.ReferencedSymbols()...)
	symbols = append(symbols, d.Right.ReferencedSymbols()...)
	return symbols
}

func (d Default) Body() hm.Expression { return d }

func (d Default) GetSourceLocation() *SourceLocation { return d.Loc }

func (d Default) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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
}

type Equality struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Equality{}
var _ Evaluator = Equality{}

func (e Equality) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity, but allow cross-type comparison at runtime
	_, err := e.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = e.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// Equality always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (e Equality) DeclaredSymbols() []string {
	return nil // Equality operator doesn't declare anything
}

func (e Equality) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, e.Left.ReferencedSymbols()...)
	symbols = append(symbols, e.Right.ReferencedSymbols()...)
	return symbols
}

func (e Equality) Body() hm.Expression { return e }

func (e Equality) GetSourceLocation() *SourceLocation { return e.Loc }

func (e Equality) Eval(ctx context.Context, env EvalEnv) (Value, error) {
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
}

type Addition struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Addition{}
var _ Evaluator = Addition{}

func (a Addition) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := a.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := a.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := hm.Unify(lt, rt); err != nil {
		return nil, WrapInferError(err, a)
	}
	return lt, nil
}

func (a Addition) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (a Addition) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, a.Left.ReferencedSymbols()...)
	symbols = append(symbols, a.Right.ReferencedSymbols()...)
	return symbols
}

func (a Addition) Body() hm.Expression { return a }

func (a Addition) GetSourceLocation() *SourceLocation { return a.Loc }

func (a Addition) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, a.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, a.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
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

type Subtraction struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Subtraction{}
var _ Evaluator = Subtraction{}

func (s Subtraction) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := s.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := s.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := hm.Unify(lt, rt); err != nil {
		return nil, WrapInferError(err, s)
	}
	return lt, nil
}

func (s Subtraction) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (s Subtraction) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, s.Left.ReferencedSymbols()...)
	symbols = append(symbols, s.Right.ReferencedSymbols()...)
	return symbols
}

func (s Subtraction) Body() hm.Expression { return s }

func (s Subtraction) GetSourceLocation() *SourceLocation { return s.Loc }

func (s Subtraction) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, s.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, s.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val - r.Val}, nil
		}
	}
	return nil, fmt.Errorf("subtraction not supported for types %T and %T", leftVal, rightVal)
}

type Multiplication struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Multiplication{}
var _ Evaluator = Multiplication{}

func (m Multiplication) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := m.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := m.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := hm.Unify(lt, rt); err != nil {
		return nil, WrapInferError(err, m)
	}
	return lt, nil
}

func (m Multiplication) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (m Multiplication) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, m.Left.ReferencedSymbols()...)
	symbols = append(symbols, m.Right.ReferencedSymbols()...)
	return symbols
}

func (m Multiplication) Body() hm.Expression { return m }

func (m Multiplication) GetSourceLocation() *SourceLocation { return m.Loc }

func (m Multiplication) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, m.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, m.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
	switch l := leftVal.(type) {
	case IntValue:
		if r, ok := rightVal.(IntValue); ok {
			return IntValue{Val: l.Val * r.Val}, nil
		}
	}
	return nil, fmt.Errorf("multiplication not supported for types %T and %T", leftVal, rightVal)
}

type Division struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Division{}
var _ Evaluator = Division{}

func (d Division) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := d.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := d.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := hm.Unify(lt, rt); err != nil {
		return nil, WrapInferError(err, d)
	}
	return lt, nil
}

func (d Division) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (d Division) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, d.Left.ReferencedSymbols()...)
	symbols = append(symbols, d.Right.ReferencedSymbols()...)
	return symbols
}

func (d Division) Body() hm.Expression { return d }

func (d Division) GetSourceLocation() *SourceLocation { return d.Loc }

func (d Division) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, d.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, d.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
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

type Modulo struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Modulo{}
var _ Evaluator = Modulo{}

func (m Modulo) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	lt, err := m.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	rt, err := m.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	if _, err := hm.Unify(lt, rt); err != nil {
		return nil, WrapInferError(err, m)
	}
	return lt, nil
}

func (m Modulo) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (m Modulo) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, m.Left.ReferencedSymbols()...)
	symbols = append(symbols, m.Right.ReferencedSymbols()...)
	return symbols
}

func (m Modulo) Body() hm.Expression { return m }

func (m Modulo) GetSourceLocation() *SourceLocation { return m.Loc }

func (m Modulo) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, m.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, m.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}
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

type Inequality struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = Inequality{}
var _ Evaluator = Inequality{}

func (i Inequality) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity, but allow cross-type comparison at runtime
	_, err := i.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = i.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// Inequality always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (i Inequality) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (i Inequality) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, i.Left.ReferencedSymbols()...)
	symbols = append(symbols, i.Right.ReferencedSymbols()...)
	return symbols
}

func (i Inequality) Body() hm.Expression { return i }

func (i Inequality) GetSourceLocation() *SourceLocation { return i.Loc }

func (i Inequality) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, i.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, i.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	// Compare the values and return the opposite of equality
	equal := valuesEqual(leftVal, rightVal)
	return BoolValue{Val: !equal}, nil
}

type LessThan struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = LessThan{}
var _ Evaluator = LessThan{}

func (l LessThan) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := l.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = l.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// LessThan always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (l LessThan) DeclaredSymbols() []string {
	return nil // Binary operators don't declare anything
}

func (l LessThan) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, l.Left.ReferencedSymbols()...)
	symbols = append(symbols, l.Right.ReferencedSymbols()...)
	return symbols
}

func (l LessThan) Body() hm.Expression { return l }

func (l LessThan) GetSourceLocation() *SourceLocation { return l.Loc }

func (l LessThan) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, l.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, l.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val < rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than comparison not supported for types %T and %T", leftVal, rightVal)
}

type GreaterThan struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = GreaterThan{}
var _ Evaluator = GreaterThan{}

func (g GreaterThan) DeclaredSymbols() []string {
	return nil // Comparison operators don't declare anything
}

func (g GreaterThan) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, g.Left.ReferencedSymbols()...)
	symbols = append(symbols, g.Right.ReferencedSymbols()...)
	return symbols
}

func (g GreaterThan) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := g.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = g.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// GreaterThan always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (g GreaterThan) Body() hm.Expression { return g }

func (g GreaterThan) GetSourceLocation() *SourceLocation { return g.Loc }

func (g GreaterThan) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, g.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, g.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val > rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("greater than comparison not supported for types %T and %T", leftVal, rightVal)
}

type LessThanEqual struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = LessThanEqual{}
var _ Evaluator = LessThanEqual{}

func (l LessThanEqual) DeclaredSymbols() []string {
	return nil // Comparison operators don't declare anything
}

func (l LessThanEqual) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, l.Left.ReferencedSymbols()...)
	symbols = append(symbols, l.Right.ReferencedSymbols()...)
	return symbols
}

func (l LessThanEqual) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := l.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = l.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// LessThanEqual always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (l LessThanEqual) Body() hm.Expression { return l }

func (l LessThanEqual) GetSourceLocation() *SourceLocation { return l.Loc }

func (l LessThanEqual) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, l.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, l.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val <= rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("less than or equal comparison not supported for types %T and %T", leftVal, rightVal)
}

type GreaterThanEqual struct {
	Left  Node
	Right Node
	Loc   *SourceLocation
}

var _ Node = GreaterThanEqual{}
var _ Evaluator = GreaterThanEqual{}

func (g GreaterThanEqual) DeclaredSymbols() []string {
	return nil // Comparison operators don't declare anything
}

func (g GreaterThanEqual) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, g.Left.ReferencedSymbols()...)
	symbols = append(symbols, g.Right.ReferencedSymbols()...)
	return symbols
}

func (g GreaterThanEqual) Infer(env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// Type check both sides for validity
	_, err := g.Left.Infer(env, fresh)
	if err != nil {
		return nil, err
	}
	_, err = g.Right.Infer(env, fresh)
	if err != nil {
		return nil, err
	}

	// GreaterThanEqual always returns a boolean
	return NonNullTypeNode{NamedTypeNode{"Boolean"}}.Infer(env, fresh)
}

func (g GreaterThanEqual) Body() hm.Expression { return g }

func (g GreaterThanEqual) GetSourceLocation() *SourceLocation { return g.Loc }

func (g GreaterThanEqual) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	leftVal, err := EvalNode(ctx, env, g.Left)
	if err != nil {
		return nil, fmt.Errorf("evaluating left side: %w", err)
	}
	rightVal, err := EvalNode(ctx, env, g.Right)
	if err != nil {
		return nil, fmt.Errorf("evaluating right side: %w", err)
	}

	switch lv := leftVal.(type) {
	case IntValue:
		if rv, ok := rightVal.(IntValue); ok {
			return BoolValue{Val: lv.Val >= rv.Val}, nil
		}
	}
	return nil, fmt.Errorf("greater than or equal comparison not supported for types %T and %T", leftVal, rightVal)
}
