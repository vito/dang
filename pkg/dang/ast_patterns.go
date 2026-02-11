package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// Case represents a case expression that evaluates branches based on equality
type Case struct {
	InferredTypeHolder
	Expr        Node
	Clauses     []*CaseClause
	NoOperand   bool // true when written as `case { ... }` without an explicit operand
	Loc         *SourceLocation
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
		if clause.Value != nil {
			symbols = append(symbols, clause.Value.ReferencedSymbols()...)
		}
		if clause.TypePattern != nil {
			symbols = append(symbols, clause.TypePattern.Name)
		}
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
			if clause.IsTypePattern() {
				// Type pattern clause: binding: TypeName => expr
				if err := c.inferTypePatternClause(ctx, env, fresh, clause, exprType); err != nil {
					return nil, err
				}

				// Infer the clause body in a scoped env with the binding.
				// Use resolvedMemberType which respects narrowed unions from
				// inline fragment selections.
				caseType, err := WithInferErrorHandling(clause, func() (hm.Type, error) {
					clauseEnv := env.Clone()
					clauseEnv = clauseEnv.Add(clause.Binding, hm.NewScheme(nil, hm.NonNullType{Type: clause.resolvedMemberType}))
					return clause.Expr.Infer(ctx, clauseEnv, fresh)
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
			} else if !clause.IsElse {
				// Value match clause
				valueType, err := clause.Value.Infer(ctx, env, fresh)
				if err != nil {
					return nil, err
				}

				// Check that the value type is assignable to the expression type
				_, err = hm.Assignable(valueType, exprType)
				if err != nil {
					return nil, WrapInferError(fmt.Errorf("Case.Infer: clause %d value type mismatch: %s != %s", i, exprType, valueType), clause)
				}

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
			} else {
				// Else clause
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
		}

		return resultType, nil
	})
}

// inferTypePatternClause validates a type pattern clause against the operand type.
// The operand must be a union (or interface), and the type pattern must name one of its members.
func (c *Case) inferTypePatternClause(ctx context.Context, env hm.Env, fresh hm.Fresher, clause *CaseClause, exprType hm.Type) error {
	modEnv, ok := env.(Env)
	if !ok {
		return NewInferError(fmt.Errorf("type patterns require a module environment"), clause)
	}

	// Unwrap NonNull
	unwrapped := exprType
	if nn, ok := unwrapped.(hm.NonNullType); ok {
		unwrapped = nn.Type
	}

	// The operand must be a union or interface type
	operandMod, ok := unwrapped.(*Module)
	if !ok {
		return NewInferError(fmt.Errorf("type pattern requires a union or interface operand, got %s", exprType), c.Expr)
	}

	if operandMod.Kind != UnionKind && operandMod.Kind != InterfaceKind {
		return NewInferError(fmt.Errorf("type pattern requires a union or interface operand, got %s type %s", operandMod.Kind, operandMod.Name()), c.Expr)
	}

	// Resolve the type pattern from the operand's members first (handles
	// narrowed unions from inline fragments), falling back to the environment.
	var memberMod *Module
	if operandMod.Kind == UnionKind {
		for _, m := range operandMod.GetMembers() {
			if mod, ok := m.(*Module); ok && mod.Name() == clause.TypePattern.Name {
				memberMod = mod
				break
			}
		}
		if memberMod == nil {
			return NewInferError(fmt.Errorf("type %s is not a member of union %s", clause.TypePattern.Name, operandMod.Name()), clause.TypePattern)
		}
	} else if operandMod.Kind == InterfaceKind {
		memberType, found := modEnv.NamedType(clause.TypePattern.Name)
		if !found {
			return NewInferError(fmt.Errorf("unknown type %s in type pattern", clause.TypePattern.Name), clause.TypePattern)
		}
		mod, ok := memberType.(*Module)
		if !ok {
			return NewInferError(fmt.Errorf("type pattern %s is not an object type", clause.TypePattern.Name), clause.TypePattern)
		}
		memberMod = mod

		// Allow the interface itself as a type pattern (matches any
		// implementer, like a typed catch-all).
		if memberMod != operandMod {
			// Otherwise the type must implement the interface.
			found2 := false
			for _, iface := range memberMod.Supertypes() {
				if iface == operandMod {
					found2 = true
					break
				}
			}
			if !found2 {
				return NewInferError(fmt.Errorf("type %s does not implement interface %s", clause.TypePattern.Name, operandMod.Name()), clause.TypePattern)
			}
		}
	}

	// Store the resolved member type for use when inferring the clause body
	clause.resolvedMemberType = memberMod
	return nil
}

func (c *Case) Walk(fn func(Node) bool) {
	if !fn(c) {
		return
	}
	c.Expr.Walk(fn)
	for _, clause := range c.Clauses {
		if clause.Value != nil {
			clause.Value.Walk(fn)
		}
		if clause.TypePattern != nil {
			fn(clause.TypePattern)
		}
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
			// Else clauses always match
			if clause.IsElse {
				return EvalNode(ctx, env, clause.Expr)
			}

			// Type pattern clauses: check against the resolved type
			if clause.IsTypePattern() {
				if matchesType(exprVal, clause.resolvedMemberType) {
					// Create a child scope with the binding
					childEnv := env.Fork()
					childEnv.Set(clause.Binding, exprVal)
					return EvalNode(ctx, childEnv, clause.Expr)
				}
				continue
			}

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

// matchesType checks if a value's concrete type matches the given pattern
// module.  It uses pointer identity, following Canonical links on narrowed
// projections, and ImplementsInterface for interface patterns.
func matchesType(val Value, pattern *Module) bool {
	valMod := valueModule(val)
	if valMod == nil {
		return false
	}
	// Resolve both sides to their canonical type so that narrowed
	// projections compare against the same identity.
	valCanon := canonicalModule(valMod)
	patCanon := canonicalModule(pattern)
	if valCanon == patCanon {
		return true
	}
	// Interface: check if the value's type implements the pattern.
	if patCanon.Kind == InterfaceKind {
		return valCanon.ImplementsInterface(patCanon)
	}
	return false
}

// canonicalModule follows the Canonical link to the full type module.
func canonicalModule(m *Module) *Module {
	if m.Canonical != nil {
		return m.Canonical
	}
	return m
}

// valueModule extracts the *Module backing a runtime value.
func valueModule(val Value) *Module {
	switch v := val.(type) {
	case *ModuleValue:
		if v.Mod != nil {
			if mod, ok := v.Mod.(*Module); ok {
				return mod
			}
		}
	}
	return nil
}

// CaseClause represents a single clause in a case expression
type CaseClause struct {
	InferredTypeHolder
	Value       Node
	Expr        Node
	IsElse      bool    // true if this is an else clause
	Binding     string  // variable name for type pattern (e.g. "user" in "user: User => ...")
	TypePattern *Symbol // type name for type pattern (e.g. "User" in "user: User => ...")
	Loc         *SourceLocation

	// resolvedMemberType is set during inference to the concrete member type
	// for this type pattern clause. For narrowed unions (from inline fragment
	// selections), this is the narrowed type with only the selected fields.
	resolvedMemberType *Module
}

var _ SourceLocatable = (*CaseClause)(nil)

func (c *CaseClause) IsTypePattern() bool {
	return c.TypePattern != nil
}

func (c *CaseClause) GetSourceLocation() *SourceLocation {
	return c.Loc
}
