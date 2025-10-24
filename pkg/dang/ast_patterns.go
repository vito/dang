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
