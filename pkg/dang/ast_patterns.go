package dang

import (
	"context"
	"fmt"

	"github.com/vito/dang/pkg/hm"
)

// Case represents a case expression that evaluates branches based on equality
type Case struct {
	InferredTypeHolder
	Expr      Node
	Clauses   []*CaseClause
	NoOperand bool // true when written as `case { ... }` without an explicit operand
	Loc       *SourceLocation
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
		// The case operand is a non-tail expression: its type drives
		// pattern selection but its value is not the case's result.
		operandCtx := contextWithoutInferExpectedType(ctx)
		exprType, err := c.Expr.Infer(operandCtx, env, fresh)
		if err != nil {
			return nil, err
		}

		if len(c.Clauses) == 0 {
			return nil, fmt.Errorf("Case.Infer: no case clauses")
		}

		// When an expected type is in scope, propagate it into each
		// clause body so distinct union members (or other assignable
		// types) can flow from different clauses without being merged
		// against each other.
		expected := currentInferExpectedType(ctx)
		clauseCtx := ctx
		if expected == nil {
			clauseCtx = contextWithoutInferExpectedType(ctx)
		}

		var resultType hm.Type
		for i, clause := range c.Clauses {
			var caseType hm.Type
			if clause.IsTypePattern() {
				// Type pattern clause: binding: TypeName => expr
				if err := c.inferTypePatternClause(operandCtx, env, fresh, clause, exprType); err != nil {
					return nil, err
				}

				// Infer the clause body in a scoped env with the binding.
				// Use resolvedMemberType which respects narrowed unions from
				// inline fragment selections.
				caseType, err = WithInferErrorHandling(clause, func() (hm.Type, error) {
					clauseEnv := env.Clone()
					clauseEnv = clauseEnv.Add(clause.Binding, hm.NewScheme(nil, hm.NonNullType{Type: clause.resolvedMemberType}))
					return clause.Expr.Infer(clauseCtx, clauseEnv, fresh)
				})
			} else if !clause.IsElse {
				// Value match clause
				var valueType hm.Type
				valueType, err = clause.Value.Infer(operandCtx, env, fresh)
				if err != nil {
					return nil, err
				}

				// Check that the value type is assignable to the expression type
				_, err = hm.Assignable(valueType, exprType)
				if err != nil {
					return nil, WrapInferError(fmt.Errorf("Case.Infer: clause %d value type mismatch: %s != %s", i, exprType, valueType), clause)
				}

				caseType, err = WithInferErrorHandling(clause, func() (hm.Type, error) {
					return clause.Expr.Infer(clauseCtx, env, fresh)
				})
			} else {
				// Else clause
				caseType, err = WithInferErrorHandling(clause, func() (hm.Type, error) {
					return clause.Expr.Infer(clauseCtx, env, fresh)
				})
			}
			if err != nil {
				return nil, err
			}

			if expected != nil {
				if _, err := hm.Assignable(caseType, expected); err != nil {
					return nil, WrapInferError(fmt.Errorf("case clause %d type mismatch: cannot use %s as %s", i, caseType, expected), clause)
				}
				resultType = expected
				continue
			}

			if i == 0 {
				resultType = caseType
				continue
			}
			mergedType, _, err := hm.MergeTypes(resultType, caseType)
			if err != nil {
				return nil, WrapInferError(fmt.Errorf("Case.Infer: clause %d type mismatch: %s != %s", i, resultType, caseType), clause)
			}
			resultType = mergedType
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

	// Resolve the pattern type via NamedTypeNode.Infer so generic
	// patterns like `Lft[Int!]` come through as AppliedType.
	patternType, err := clause.TypePattern.Infer(ctx, env, fresh)
	if err != nil {
		return WrapInferError(err, clause.TypePattern)
	}
	patternMod := unionMemberModuleOf(patternType)
	if patternMod == nil {
		return NewInferError(fmt.Errorf("type pattern %s is not a named type", clause.TypePattern.Name), clause.TypePattern)
	}

	// Resolve the type pattern from the operand's members (handles narrowed
	// unions from inline fragments and generic union applications).
	resolved, err := resolveTypePattern(modEnv, unwrapped, patternType, patternMod, clause.TypePattern.Name)
	if err != nil {
		return NewInferError(err, clause.TypePattern)
	}
	if resolved == nil {
		return NewInferError(fmt.Errorf("type pattern requires a union or interface operand, got %s", exprType), c.Expr)
	}

	// Store the resolved member type for use when inferring the clause body
	clause.resolvedMemberType = resolved
	return nil
}

// resolveTypePattern checks that the pattern is a valid member of the
// operand and returns the type to bind in the clause body.
func resolveTypePattern(env Env, operand hm.Type, patternType hm.Type, patternMod *Module, patternName string) (hm.Type, error) {
	switch op := operand.(type) {
	case *Module:
		if op.Kind != UnionKind && op.Kind != InterfaceKind {
			return nil, fmt.Errorf("type pattern requires a union or interface operand, got %s type %s", op.Kind, op)
		}
		if op.Kind == UnionKind {
			for _, m := range op.GetMembers() {
				mod, ok := m.(*Module)
				if !ok {
					continue
				}
				// Pointer identity is the common case. Match by canonical
				// type so a narrowed projection (from inline fragments)
				// matches the original pattern name, and fall back to a
				// name match for prelude-shared modules.
				if mod == patternMod || canonicalModule(mod) == canonicalModule(patternMod) {
					// For narrowed unions, return the narrowed member so
					// the clause body sees the selected field subset.
					return mod, nil
				}
				if mod.Name() == patternName {
					return mod, nil
				}
			}
			return nil, fmt.Errorf("type %s is not a member of union %s", patternName, op)
		}
		// Interface
		mod, err := resolveInterfaceTypePattern(env, op, patternName)
		if err != nil {
			return nil, err
		}
		_ = mod
		return patternType, nil
	case *AppliedType:
		switch op.Base.Kind {
		case UnionKind:
			if _, err := hm.Assignable(patternType, op); err != nil {
				return nil, fmt.Errorf("type %s is not a member of union %s: %s", patternType, op, err)
			}
			return patternType, nil
		case InterfaceKind:
			// Allow the interface itself (`c: Container[Int!] => ...`).
			if patternMod == op.Base {
				if _, err := hm.Assignable(patternType, op); err != nil {
					return nil, fmt.Errorf("type %s is not assignable to interface %s: %s", patternType, op, err)
				}
				return patternType, nil
			}
			// Otherwise the pattern type must implement the interface, and
			// — once applied — must satisfy the operand's specific args.
			implements := false
			for _, super := range patternMod.Supertypes() {
				if mod, ok := super.(*Module); ok && mod == op.Base {
					implements = true
					break
				}
				if at, ok := super.(*AppliedType); ok && at.Base == op.Base {
					implements = true
					break
				}
			}
			if !implements {
				return nil, fmt.Errorf("type %s does not implement interface %s", patternType, op)
			}
			if _, err := hm.Assignable(patternType, op); err != nil {
				return nil, fmt.Errorf("type %s is not assignable to interface %s: %s", patternType, op, err)
			}
			return patternType, nil
		default:
			return nil, fmt.Errorf("type pattern requires a union or interface operand, got %s type %s", op.Base.Kind, op)
		}
	}
	if operandUnion, ok := operand.(*hm.UnionType); ok {
		if _, err := resolveInlineUnionTypePattern(env, operandUnion, patternName); err != nil {
			return nil, err
		}
		return patternType, nil
	}
	return nil, nil
}

func resolveInterfaceTypePattern(env Env, iface *Module, patternName string) (*Module, error) {
	memberType, found := env.NamedType(patternName)
	if !found {
		return nil, fmt.Errorf("unknown type %s in type pattern", patternName)
	}
	memberMod, ok := memberType.(*Module)
	if !ok {
		return nil, fmt.Errorf("type pattern %s is not an object type", patternName)
	}

	// Allow the interface itself as a type pattern (matches any implementer,
	// like a typed catch-all).
	if memberMod == iface {
		return memberMod, nil
	}

	// Otherwise the type must implement the interface.
	for _, super := range memberMod.Supertypes() {
		if super == iface {
			return memberMod, nil
		}
	}
	return nil, fmt.Errorf("type %s does not implement interface %s", patternName, iface)
}

func resolveInlineUnionTypePattern(env Env, union *hm.UnionType, patternName string) (*Module, error) {
	patternType, found := env.NamedType(patternName)
	if !found {
		return nil, fmt.Errorf("unknown type %s in type pattern", patternName)
	}
	patternMod, ok := patternType.(*Module)
	if !ok {
		return nil, fmt.Errorf("type pattern %s is not a named type", patternName)
	}

	for _, option := range union.Options {
		optionMod, ok := moduleType(option)
		if !ok {
			continue
		}
		if optionMod == patternMod || optionMod.Name() == patternName {
			return patternMod, nil
		}
		if optionMod.Kind == UnionKind && optionMod.HasMember(patternMod) {
			return patternMod, nil
		}
		if optionMod.Kind == InterfaceKind {
			if _, err := resolveInterfaceTypePattern(env, optionMod, patternName); err == nil {
				return patternMod, nil
			}
		}
	}

	return nil, fmt.Errorf("type %s is not a member of union %s", patternName, union)
}

func moduleType(t hm.Type) (*Module, bool) {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	mod, ok := t.(*Module)
	return mod, ok
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
					childEnv := env.Derive(true)
					childEnv.Bind(clause.Binding, exprVal, PrivateVisibility)
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

// matchesType checks if a value's concrete type matches the given pattern.
// It uses pointer identity, following Canonical links on narrowed
// projections, and ImplementsInterface for interface patterns. The
// AppliedType args are checked statically and dropped at runtime — only
// the Base module participates in the match.
func matchesType(val Value, pattern hm.Type) bool {
	patBase := unionMemberModuleOf(pattern)
	if patBase == nil {
		return false
	}
	valMod := valueModule(val)
	if valMod == nil {
		return false
	}
	// Resolve both sides to their canonical type so that narrowed
	// projections compare against the same identity.
	valCanon := canonicalModule(valMod)
	patCanon := canonicalModule(patBase)
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
	case GraphQLValue:
		if v.TypeEnv != nil {
			if typeEnv, found := v.TypeEnv.NamedType(v.TypeName); found {
				if mod, ok := typeEnv.(*Module); ok {
					return mod
				}
			}
		}
	case StringValue:
		return StringType
	case IntValue:
		return IntType
	case FloatValue:
		return FloatType
	case BoolValue:
		return BooleanType
	case ScalarValue:
		if mod, ok := v.ScalarType.(*Module); ok {
			return mod
		}
	case EnumValue:
		if mod, ok := v.EnumType.(*Module); ok {
			return mod
		}
	}
	return nil
}

// CaseClause represents a single clause in a case expression
type CaseClause struct {
	InferredTypeHolder
	Value   Node
	Expr    Node
	IsElse  bool   // true if this is an else clause
	Binding string // variable name for type pattern (e.g. "user" in "user: User => ...")
	// TypePattern is the type reference for a type pattern clause. It is a
	// NamedTypeNode so it can carry type arguments for generic members,
	// e.g. `l: Lft[Int!] => ...`.
	TypePattern *NamedTypeNode
	Loc         *SourceLocation

	// resolvedMemberType is set during inference to the concrete member type
	// for this type pattern clause. For narrowed unions (from inline fragment
	// selections), this is the narrowed type with only the selected fields.
	// May be either a *Module (non-generic member, narrowed type, or
	// interface pattern) or an *AppliedType (generic member with the
	// pattern's type args applied).
	resolvedMemberType hm.Type
}

var _ SourceLocatable = (*CaseClause)(nil)

func (c *CaseClause) IsTypePattern() bool {
	return c.TypePattern != nil
}

func (c *CaseClause) GetSourceLocation() *SourceLocation {
	return c.Loc
}
