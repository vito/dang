package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// Match represents a pattern matching expression
type Match struct {
	InferredTypeHolder
	Expr  Node
	Cases []MatchCase
	Loc   *SourceLocation
}

var _ Node = (*Match)(nil)

func (m *Match) DeclaredSymbols() []string {
	return nil // Match expressions don't declare anything
}

func (m *Match) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, m.Expr.ReferencedSymbols()...)
	// Add symbols from case expressions
	for _, case_ := range m.Cases {
		symbols = append(symbols, case_.Expr.ReferencedSymbols()...)
	}
	return symbols
}

func (m *Match) Body() hm.Expression { return m }

func (m *Match) GetSourceLocation() *SourceLocation { return m.Loc }

func (m *Match) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	exprType, err := m.Expr.Infer(ctx, env, fresh)
	if err != nil {
		return nil, err
	}

	if len(m.Cases) == 0 {
		return nil, fmt.Errorf("Match.Infer: no match cases")
	}

	var resultType hm.Type
	for i, case_ := range m.Cases {
		caseEnv := env.Clone()

		// TODO: Pattern matching type checking - for now just add pattern variables
		if varPattern, ok := case_.Pattern.(VariablePattern); ok {
			caseEnv.Add(varPattern.Name, hm.NewScheme(nil, exprType))
		}

		caseType, err := case_.Expr.Infer(ctx, caseEnv, fresh)
		if err != nil {
			return nil, err
		}

		if i == 0 {
			resultType = caseType
		} else {
			subs, err := hm.Unify(resultType, caseType)
			if err != nil {
				return nil, fmt.Errorf("Match.Infer: case %d type mismatch: %s != %s", i, resultType, caseType)
			}
			resultType = resultType.Apply(subs).(hm.Type)
		}
	}

	return resultType, nil
}

func (m *Match) Walk(fn func(Node) bool) {
	if !fn(m) {
		return
	}
	m.Expr.Walk(fn)
	for _, case_ := range m.Cases {
		case_.Expr.Walk(fn)
	}
}

func (m *Match) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, m, func() (Value, error) {
		// Evaluate the expression we're matching against
		exprVal, err := EvalNode(ctx, env, m.Expr)
		if err != nil {
			return nil, fmt.Errorf("evaluating match expression: %w", err)
		}

		// Try each case in order
		for i, case_ := range m.Cases {
			matched, bindings, err := matchPattern(case_.Pattern, exprVal, env)
			if err != nil {
				return nil, fmt.Errorf("matching case %d: %w", i, err)
			}

			if matched {
				// Create a new environment with pattern bindings
				caseEnv := env.Clone()
				for name, value := range bindings {
					caseEnv.Set(name, value)
				}

				// Evaluate the case expression
				return EvalNode(ctx, caseEnv, case_.Expr)
			}
		}

		return nil, fmt.Errorf("no match case matched the value: %v", exprVal)
	})
}

// matchPattern checks if a value matches a pattern and returns any bindings
func matchPattern(pattern Pattern, value Value, env EvalEnv) (bool, map[string]Value, error) {
	switch p := pattern.(type) {
	case WildcardPattern:
		// Wildcard always matches
		return true, nil, nil

	case LiteralPattern:
		// Evaluate the literal and compare
		litVal, err := EvalNode(context.Background(), env, p.Value)
		if err != nil {
			return false, nil, err
		}
		matched := valuesEqual(litVal, value)
		return matched, nil, nil

	case VariablePattern:
		// Variable pattern always matches and binds the value
		return true, map[string]Value{p.Name: value}, nil

	case ConstructorPattern:
		// Constructor patterns are not yet implemented
		return false, nil, fmt.Errorf("constructor patterns not yet implemented")

	default:
		return false, nil, fmt.Errorf("unknown pattern type: %T", pattern)
	}
}

// MatchCase represents a single case in a match expression
type MatchCase struct {
	InferredTypeHolder
	Pattern Pattern
	Expr    Node
}

// WildcardPattern represents the wildcard pattern '_'
type WildcardPattern struct{}

// LiteralPattern represents a literal value pattern
type LiteralPattern struct {
	InferredTypeHolder
	Value Node
}

// ConstructorPattern represents a constructor pattern with arguments
type ConstructorPattern struct {
	InferredTypeHolder
	Name string
	Args []Pattern
}

// VariablePattern represents a variable binding pattern
type VariablePattern struct {
	InferredTypeHolder
	Name string
}
