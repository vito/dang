package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// Case represents a case expression that evaluates branches based on equality
type Case struct {
	InferredTypeHolder
	Expr    Node
	Clauses []*CaseClause
	Loc     *SourceLocation
}

var _ Node = (*Case)(nil)

func (c *Case) DeclaredSymbols() []string {
	return nil // Case expressions don't declare anything
}

func (c *Case) ReferencedSymbols() []string {
	var symbols []string
	symbols = append(symbols, c.Expr.ReferencedSymbols()...)
	// Add symbols from clause expressions
	for _, clause := range c.Clauses {
		symbols = append(symbols, clause.Value.ReferencedSymbols()...)
		symbols = append(symbols, clause.Expr.ReferencedSymbols()...)
	}
	return symbols
}

func (c *Case) Body() hm.Expression { return c }

func (c *Case) GetSourceLocation() *SourceLocation { return c.Loc }

func (c *Case) Infer(ctx context.Context, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	return WithInferErrorHandling(c, func() (hm.Type, error) {
		exprType, err := c.Expr.Infer(ctx, env, fresh)
		if err != nil {
			return nil, err
		}

		if len(c.Clauses) == 0 {
			return nil, fmt.Errorf("Case.Infer: no case clauses")
		}

		var resultType hm.Type
		for i, clause := range c.Clauses {
			// Infer the value type and check it's compatible with the expression
			valueType, err := clause.Value.Infer(ctx, env, fresh)
			if err != nil {
				return nil, err
			}

			// Check that the value type is assignable to the expression type
			_, err = hm.Assignable(valueType, exprType)
			if err != nil {
				return nil, WrapInferError(fmt.Errorf("Case.Infer: clause %d value type mismatch: %s != %s", i, exprType, valueType), clause)
			}

			// Infer the result type
			caseType, err := WithInferErrorHandling(clause, func() (hm.Type, error) {
				return clause.Expr.Infer(ctx, env, fresh)
			})
			if err != nil {
				return nil, err
			}

			if i == 0 {
				resultType = caseType
			} else {
				subs, err := hm.Assignable(caseType, resultType)
				if err != nil {
					return nil, WrapInferError(fmt.Errorf("Case.Infer: clause %d type mismatch: %s != %s", i, resultType, caseType), clause)
				}
				resultType = resultType.Apply(subs).(hm.Type)
			}
		}

		return resultType, nil
	})
}

func (c *Case) Walk(fn func(Node) bool) {
	if !fn(c) {
		return
	}
	c.Expr.Walk(fn)
	for _, clause := range c.Clauses {
		clause.Value.Walk(fn)
		clause.Expr.Walk(fn)
	}
}

func (c *Case) Eval(ctx context.Context, env EvalEnv) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		// Evaluate the expression we're matching against
		exprVal, err := EvalNode(ctx, env, c.Expr)
		if err != nil {
			return nil, fmt.Errorf("evaluating case expression: %w", err)
		}

		// Try each clause in order
		for i, clause := range c.Clauses {
			// Evaluate the clause value
			clauseVal, err := EvalNode(ctx, env, clause.Value)
			if err != nil {
				return nil, fmt.Errorf("evaluating clause %d value: %w", i, err)
			}

			// Check for equality
			if valuesEqual(clauseVal, exprVal) {
				// Evaluate and return the clause expression
				return EvalNode(ctx, env, clause.Expr)
			}
		}

		return nil, fmt.Errorf("no case clause matched the value: %v", exprVal)
	})
}

// CaseClause represents a single clause in a case expression
type CaseClause struct {
	InferredTypeHolder
	Value Node
	Expr  Node
	Loc   *SourceLocation
}

var _ SourceLocatable = (*CaseClause)(nil)

func (c *CaseClause) GetSourceLocation() *SourceLocation {
	return c.Loc
}
