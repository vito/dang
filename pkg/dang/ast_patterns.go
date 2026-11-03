package dang

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// Case represents a case expression that evaluates branches based on equality
type Case struct {
	InferredTypeHolder
	Expr      Node
	Clauses   []*CaseClause
	NoOperand bool // true when written as `case { ... }` without an explicit operand
	Loc       *SourceLocation

	// exhaustive is set during inference when the clauses are known to cover
	// every possible operand value (an else clause, or type patterns covering
	// every member of a non-null union). A non-exhaustive case can fall
	// through and evaluate to null, so its result type is nullable.
	exhaustive bool
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

// clauseFacts returns the refinements a value clause implies for its body
// (truthy) and for the clauses after it (falsy). Without an operand a
// clause's value is a condition. With a bare name as operand, `null =>`
// leaves it non-null for the later clauses, and a non-null value matching
// makes it non-null in that clause's body. The check is the clause's value
// being inferred (noteCheck), so writes after it end the refinements.
func (c *Case) clauseFacts(ctx context.Context, env hm.Env, clause *CaseClause, valueType hm.Type) ConditionFacts {
	if c.NoOperand {
		return analyzeCondition(clause.Value, env)
	}
	sym, ok := c.Expr.(*Symbol)
	if !ok {
		return ConditionFacts{}
	}
	nonNull, ok := nonNullType(env, sym.Name)
	if !ok {
		return ConditionFacts{}
	}
	noteCheck(ctx, clause.Value)
	n := []Narrowing{{Symbol: sym.Name, Type: nonNull, check: clause.Value}}
	if _, isNull := clause.Value.(*Null); isNull {
		return ConditionFacts{Falsy: n}
	}
	if _, nn := valueType.(hm.NonNullType); nn {
		return ConditionFacts{Truthy: n}
	}
	return ConditionFacts{}
}

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

		hasElse := false
		// Arms are exclusive: each arm's body skips the writes of the arms
		// before it (narrowlog.go).
		var arms []tickRange
		inferArm := func(clause *CaseClause, infer func() (hm.Type, error)) (hm.Type, error) {
			var unskips []func()
			for _, r := range arms {
				unskips = append(unskips, skipRange(ctx, r.from, r.to))
			}
			start := enterBranch(ctx)
			t, err := infer()
			end := enterBranch(ctx)
			for _, undo := range unskips {
				undo()
			}
			arms = append(arms, tickRange{start, end})
			skipBranch(ctx, clause.Expr, start)
			return t, err
		}
		defer func() { noteExclusive(ctx, arms) }()
		var elseClause *CaseClause
		var priorPatterns []*CaseClause
		var valuePatterns []Node
		var resultType hm.Type
		// Later clauses run only when the earlier ones didn't match, so they
		// see the earlier clauses' falsy refinements: after `null =>`, the
		// operand is non-null.
		armEnv := env
		for i, clause := range c.Clauses {
			env := armEnv
			// A clause after an else catch-all can never match, whatever
			// kind it is.
			if err := checkClauseReachable(clause, elseClause, nil); err != nil {
				return nil, err
			}

			var caseType hm.Type
			if clause.IsTypePattern() {
				// Type pattern clause: binding: TypeName => expr
				if err := c.inferTypePatternClause(ctx, env, fresh, clause, exprType); err != nil {
					return nil, err
				}
				if err := checkClauseReachable(clause, nil, priorPatterns); err != nil {
					return nil, err
				}
				priorPatterns = append(priorPatterns, clause)

				// Infer the clause body in a scoped env with the binding.
				// Use resolvedMemberType which respects narrowed unions from
				// inline fragment selections.
				caseType, err = inferArm(clause, func() (hm.Type, error) {
					return WithInferErrorHandling(clause, func() (hm.Type, error) {
						clauseEnv := env.Clone()
						clauseEnv = clauseEnv.Add(clause.Binding, hm.NewScheme(nil, hm.NonNullType{Type: clause.resolvedMemberType}))
						return clause.Expr.Infer(ctx, clauseEnv, fresh)
					})
				})
			} else if !clause.IsElse {
				// Value match clause
				var valueType hm.Type
				valueType, err = clause.Value.Infer(ctx, env, fresh)
				if err != nil {
					return nil, err
				}

				// Check that the value type is assignable to the expression type,
				// permitting literal coercion (e.g. String! literal -> custom
				// scalar or enum matching the operand's type).
				_, err = assignableForValue(env, valueType, exprType, clause.Value)
				if err != nil {
					// A literal that names no value of the enum explains
					// itself, pointing at the literal (checkEnumLiterals).
					var literalErr *InferError
					if errors.As(err, &literalErr) {
						return nil, err
					}
					return nil, WrapInferError(fmt.Errorf("case pattern of type %s can't match a %s operand", valueType, exprType), clause.Value)
				}
				facts := c.clauseFacts(ctx, env, clause, valueType)
				valuePatterns = append(valuePatterns, clause.Value)
				clause.Value = wrapCoerce(clause.Value, exprType, "")

				bodyEnv := withNarrowings(ctx, env, facts.Truthy)
				caseType, err = inferArm(clause, func() (hm.Type, error) {
					return WithInferErrorHandling(clause, func() (hm.Type, error) {
						return clause.Expr.Infer(ctx, bodyEnv, fresh)
					})
				})
				armEnv = withNarrowings(ctx, armEnv, facts.Falsy)
			} else {
				// Else clause. Note: an else after type patterns is never
				// flagged here — a nullable operand falls through every type
				// pattern, so a trailing else stays reachable even after an
				// interface catch-all.
				hasElse = true
				elseClause = clause
				caseType, err = inferArm(clause, func() (hm.Type, error) {
					return WithInferErrorHandling(clause, func() (hm.Type, error) {
						return clause.Expr.Infer(ctx, env, fresh)
					})
				})
			}
			if err != nil {
				return nil, err
			}

			if i == 0 {
				resultType = caseType
				continue
			}
			// Clause bodies that diverge widen to a union, same as if/else
			// branches.
			resultType = mergeControlResultTypesTagged(
				resultType, armOrigin("case clause", c.Clauses[0].Loc),
				caseType, armOrigin("case clause", clause.Loc),
			)
		}

		c.exhaustive = hasElse || c.coversOperand(env, exprType, valuePatterns)
		if !c.exhaustive {
			// Nothing guarantees a clause matches, so the case can fall
			// through and evaluate to null.
			resultType = nullableControlResultType(resultType)
		}

		return resultType, nil
	})
}

// coversOperand reports whether the clauses match every possible value of
// the operand, making an else clause unnecessary: type patterns covering a
// union's members, value patterns naming every value of an enum (`Color.RED`,
// an imported bare `RED`, or a literal `"RED"`), or `true` and `false` for a
// Boolean. A nullable operand also needs a `null` pattern.
func (c *Case) coversOperand(env hm.Env, exprType hm.Type, values []Node) bool {
	inner, nullable := exprType, true
	if nn, ok := exprType.(hm.NonNullType); ok {
		inner, nullable = nn.Type, false
	}
	if _, ok := inner.(*hm.UnionType); ok {
		// An inline union carries nullability on its options.
		nullable = false
	}
	if nullable && !hasNullPattern(values) {
		return false
	}
	if c.coversAllMembers(inner) {
		return true
	}
	mod, ok := inner.(*Type)
	if !ok {
		return false
	}
	switch {
	case mod == BooleanType:
		seen := map[bool]bool{}
		for _, v := range values {
			if b, ok := unwrapGrouped(v).(*Boolean); ok {
				seen[b.Value] = true
			}
		}
		return seen[true] && seen[false]
	case mod.Kind == EnumKind && !mod.IsMeta():
		names := enumValueNames(mod)
		if len(names) == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, v := range values {
			if name, ok := enumPatternValue(env, mod, v); ok {
				seen[name] = true
			}
		}
		for _, name := range names {
			if !seen[name] {
				return false
			}
		}
		return true
	}
	return false
}

func hasNullPattern(values []Node) bool {
	for _, v := range values {
		if _, ok := unwrapGrouped(v).(*Null); ok {
			return true
		}
	}
	return false
}

// enumPatternValue reports which value of enum a case value pattern names,
// when that's known statically: `Color.RED` (a member read off the enum's
// namespace), a bare `RED` bound to the enum's own value (an unqualified
// import), or a string literal (checked against the values already).
func enumPatternValue(env hm.Env, enum *Type, v Node) (string, bool) {
	switch n := unwrapGrouped(v).(type) {
	case *String:
		return n.Value, enumHasValue(enum, n.Value)
	case *Select:
		holder, ok := n.Receiver.(interface{ GetInferredType() hm.Type })
		if !ok || holder.GetInferredType() == nil {
			return "", false
		}
		recv := holder.GetInferredType()
		if nn, ok := recv.(hm.NonNullType); ok {
			recv = nn.Type
		}
		if meta, ok := recv.(*Type); ok && meta.IsMeta() && meta.InstanceType() == enum {
			return n.Field.Name, enumHasValue(enum, n.Field.Name)
		}
	case *Symbol:
		// Only the enum's own binding, not a variable that happens to be
		// named like a value: the scheme is shared with the enum's table.
		own, ok := enum.LocalSchemeOf(n.Name)
		if !ok {
			return "", false
		}
		if bound, ok := env.SchemeOf(n.Name); ok && bound == own {
			return n.Name, enumHasValue(enum, n.Name)
		}
	}
	return "", false
}

// coversAllMembers reports whether the type pattern clauses cover every
// possible non-null value of the operand type (its nullability is handled by
// coversOperand).
func (c *Case) coversAllMembers(exprType hm.Type) bool {
	var patterns []*Type
	for _, clause := range c.Clauses {
		if clause.IsTypePattern() && clause.resolvedMemberType != nil {
			patterns = append(patterns, clause.resolvedMemberType)
		}
	}
	if len(patterns) == 0 {
		return false
	}

	switch t := exprType.(type) {
	case *Type:
		return moduleCoveredBy(t, patterns)
	case *hm.UnionType:
		return inlineUnionCoveredBy(t, patterns)
	}
	return false
}

func inlineUnionCoveredBy(union *hm.UnionType, patterns []*Type) bool {
	for _, option := range union.Options {
		nn, ok := option.(hm.NonNullType)
		if !ok {
			// A nullable option admits null, which no pattern matches.
			return false
		}
		mod, ok := nn.Type.(*Type)
		if !ok || !moduleCoveredBy(mod, patterns) {
			return false
		}
	}
	return true
}

// moduleCoveredBy reports whether every value of mod is matched by one of
// the type patterns. A union is covered when all of its members are; an
// interface only by itself (its implementer set is open).
func moduleCoveredBy(mod *Type, patterns []*Type) bool {
	modCanon := canonicalModule(mod)
	for _, p := range patterns {
		pCanon := canonicalModule(p)
		if modCanon == pCanon || modCanon.Name() == pCanon.Name() {
			return true
		}
		if pCanon.Kind == InterfaceKind && modCanon.ImplementsInterface(pCanon) {
			return true
		}
	}
	if modCanon.Kind == UnionKind {
		members := modCanon.GetMembers()
		if len(members) == 0 {
			return false
		}
		for _, m := range members {
			memberMod, ok := m.(*Type)
			if !ok || !moduleCoveredBy(memberMod, patterns) {
				return false
			}
		}
		return true
	}
	return false
}

// inferTypePatternClause validates a type pattern clause against the operand type.
// The operand must be a union (or interface), and the type pattern must name one of its members.
func (c *Case) inferTypePatternClause(_ context.Context, env hm.Env, _ hm.Fresher, clause *CaseClause, exprType hm.Type) error {
	typeScope, ok := env.(TypeScope)
	if !ok {
		return NewInferError(fmt.Errorf("type patterns require a module environment"), clause)
	}

	// Unwrap NonNull
	unwrapped := exprType
	if nn, ok := unwrapped.(hm.NonNullType); ok {
		unwrapped = nn.Type
	}

	// Resolve the type pattern from the operand's members first (handles
	// narrowed unions from inline fragments), falling back to the environment.
	var memberMod *Type
	if operandMod, ok := unwrapped.(*Type); ok {
		if operandMod.Kind != UnionKind && operandMod.Kind != InterfaceKind {
			return NewInferError(fmt.Errorf("type pattern requires a union or interface operand, got %s type %s", operandMod.Kind, operandMod), c.Expr)
		}

		if operandMod.Kind == UnionKind {
			for _, m := range operandMod.GetMembers() {
				if mod, ok := m.(*Type); ok && mod.Name() == clause.TypePattern.Name {
					memberMod = mod
					break
				}
			}
			if memberMod == nil {
				return NewInferError(fmt.Errorf("type %s is not a member of union %s", clause.TypePattern.Name, operandMod), clause.TypePattern)
			}
		} else if operandMod.Kind == InterfaceKind {
			var err error
			memberMod, err = resolveInterfaceTypePattern(typeScope, operandMod, clause.TypePattern.Name)
			if err != nil {
				return NewInferError(err, clause.TypePattern)
			}
		}
	} else if operandUnion, ok := unwrapped.(*hm.UnionType); ok {
		var err error
		memberMod, err = resolveInlineUnionTypePattern(typeScope, operandUnion, clause.TypePattern.Name)
		if err != nil {
			return NewInferError(err, clause.TypePattern)
		}
	} else {
		return NewInferError(fmt.Errorf("type pattern requires a union or interface operand, got %s", exprType), c.Expr)
	}

	// Store the resolved member type for use when inferring the clause body
	clause.resolvedMemberType = memberMod
	return nil
}

func resolveInterfaceTypePattern(env TypeScope, iface *Type, patternName string) (*Type, error) {
	memberType, found := env.NamedType(patternName)
	if !found {
		return nil, fmt.Errorf("unknown type %s in type pattern", patternName)
	}
	memberMod, ok := memberType.(*Type)
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

func resolveInlineUnionTypePattern(env TypeScope, union *hm.UnionType, patternName string) (*Type, error) {
	patternType, found := env.NamedType(patternName)
	if !found {
		return nil, fmt.Errorf("unknown type %s in type pattern", patternName)
	}
	patternMod, ok := patternType.(*Type)
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

func moduleType(t hm.Type) (*Type, bool) {
	if nn, ok := t.(hm.NonNullType); ok {
		t = nn.Type
	}
	mod, ok := t.(*Type)
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
		if clause.TypePattern != nil {
			fn(clause.TypePattern)
		}
		clause.Expr.Walk(fn)
	}
}

func (c *Case) Eval(ctx context.Context, scope ValueScope) (Value, error) {
	return WithEvalErrorHandling(ctx, c, func() (Value, error) {
		// Evaluate the expression we're matching against
		exprVal, err := EvalNode(ctx, scope, c.Expr)
		if err != nil {
			return nil, fmt.Errorf("evaluating case expression: %w", err)
		}

		// Type patterns match, and bind, the operand's concrete type. A
		// GraphQL interface or union handle is resolved to it first; value
		// clauses still compare against the handle itself, since resolving
		// it gives a new handle with a different identity (see valuesEqual),
		// unless it turned out to be null.
		typedVal := exprVal
		// A mutation-reached interface or union handle is never resolved
		// (see needsConcreteType), so a type pattern its declared type
		// doesn't satisfy can't be decided either way.
		var unresolvedMutation *GraphQLValue
		if gqlVal, ok := exprVal.(GraphQLValue); ok {
			if c.needsConcreteType(gqlVal) {
				typedVal, err = gqlVal.resolveObject(ctx, "the concrete type of "+gqlVal.Name)
				if err != nil {
					return nil, err
				}
				if _, isNull := typedVal.(NullValue); isNull {
					exprVal = typedVal
				}
			} else if gqlVal.IsMutation && gqlVal.isAbstract() {
				unresolvedMutation = &gqlVal
			}
		}

		// Try each clause in order
		for i, clause := range c.Clauses {
			// Else clauses always match
			if clause.IsElse {
				return EvalNode(ctx, scope, clause.Expr)
			}

			// Type pattern clauses: check against the resolved type
			if clause.IsTypePattern() {
				if matchesType(typedVal, clause.resolvedMemberType) {
					// Create a child scope with the binding. It is unsealed, like
					// a value clause's scope, so the clause can update enclosing
					// bindings (e.g. loop state) the same way.
					childScope := scope.Derive(false)
					childScope.Bind(clause.Binding, typedVal, PrivateVisibility)
					return EvalNode(ctx, childScope, clause.Expr)
				}
				if unresolvedMutation != nil {
					// Falling through to a later arm or else would silently
					// take the wrong branch for a value that may well be this
					// type.
					return nil, CreateEvalError(ctx, c.unresolvedMutationPatternError(*unresolvedMutation, clause), clause)
				}
				continue
			}

			// Evaluate the clause value
			clauseVal, err := EvalNode(ctx, scope, clause.Value)
			if err != nil {
				return nil, fmt.Errorf("evaluating clause %d value: %w", i, err)
			}

			// Check for equality
			if valuesEqual(clauseVal, exprVal) {
				// Evaluate and return the clause expression
				return EvalNode(ctx, scope, clause.Expr)
			}
		}

		if c.exhaustive {
			// Inference proved a clause must match; falling through here is
			// a compiler bug, not a user error.
			return nil, fmt.Errorf("exhaustive case did not match the value: %v", exprVal)
		}

		// No clause matched: the case's type is nullable to account for
		// exactly this fallthrough.
		return NullValue{}, nil
	})
}

// needsConcreteType reports whether matching the case's type patterns needs
// the concrete type of g, a GraphQL handle, to be fetched from the server.
//
// A handle typed as an interface or union only knows that abstract type (a
// non-null field, or one selected off a mutation; nullable fields are
// resolved on their own, see GraphQLFunction.Call), so it would never match
// a concrete type pattern. Resolving it costs one small query, so it is
// skipped when there are no type patterns, when the handle is already of a
// concrete type, and when the first type pattern matches the abstract type
// itself (e.g. `n: Node`), since every concrete type would match it too.
//
// A handle reached through a mutation is never resolved: any query built on
// its chain re-sends the mutation, so it keeps its abstract type. Case.Eval
// raises unresolvedMutationPatternError for the first type pattern that the
// abstract type doesn't satisfy, rather than guess.
func (c *Case) needsConcreteType(g GraphQLValue) bool {
	if g.IsMutation || !g.isAbstract() {
		return false
	}
	for _, clause := range c.Clauses {
		if clause.IsTypePattern() {
			// A handle that may be null must be resolved all the same, for
			// a null to fall through to `null` patterns or else.
			return g.MaybeNull || !matchesType(g, clause.resolvedMemberType)
		}
	}
	return false
}

// unresolvedMutationPatternError explains why a type pattern can't be matched
// against g, an interface or union handle reached through a mutation, and
// how to discriminate it instead: an inline fragment selection on the
// mutation call sends it once, with each member's fields. The example lists
// a fragment for each of the case's type patterns, since a selection without
// one for the value's actual type fails.
func (c *Case) unresolvedMutationPatternError(g GraphQLValue, clause *CaseClause) error {
	var fragments []string
	for _, cl := range c.Clauses {
		if cl.IsTypePattern() && cl.TypePattern.Name != g.TypeName {
			fragments = append(fragments, fmt.Sprintf("... on %s {{ ... }}", cl.TypePattern.Name))
		}
	}
	return fmt.Errorf(
		"cannot match type pattern %s against a %s reached through mutation %s: finding its concrete type would send the mutation again; select each possible type's fields on the mutation call instead, e.g. .{{ %s }}",
		clause.TypePattern.Name, g.TypeName, g.Name, strings.Join(fragments, ", "),
	)
}

// matchesType checks if a value's concrete type matches the given pattern
// module.  It uses pointer identity, following Canonical links on narrowed
// projections, and ImplementsInterface for interface patterns.
func matchesType(val Value, pattern *Type) bool {
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
func canonicalModule(m *Type) *Type {
	if m.Canonical != nil {
		return m.Canonical
	}
	return m
}

// typePatternCovers reports whether an earlier type-pattern clause already
// matches every value a later pattern would, mirroring matchesType's
// runtime dispatch exactly: canonical pointer identity, or an earlier
// interface pattern that the later type implements. Strictly pairwise —
// patterns that only *collectively* cover a later one leave it reachable,
// so a defensive else after exhaustive union patterns stays legal.
func typePatternCovers(earlier, later *Type) bool {
	earlierCanon := canonicalModule(earlier)
	laterCanon := canonicalModule(later)
	if earlierCanon == laterCanon {
		return true
	}
	return earlierCanon.Kind == InterfaceKind && laterCanon.ImplementsInterface(earlierCanon)
}

// checkClauseReachable rejects a clause that can never match: any clause
// after an else catch-all, or a type pattern already covered by an earlier
// one. Call it at the top of a clause loop, before inferring the clause,
// with the clause's resolvedMemberType populated for type patterns.
func checkClauseReachable(clause *CaseClause, elseClause *CaseClause, priorPatterns []*CaseClause) error {
	if elseClause != nil {
		return NewInferError(
			fmt.Errorf("unreachable clause: follows the else catch-all on line %d", elseClause.Loc.Line),
			clause,
		)
	}
	if clause.IsTypePattern() && clause.resolvedMemberType != nil {
		for _, prev := range priorPatterns {
			if typePatternCovers(prev.resolvedMemberType, clause.resolvedMemberType) {
				return NewInferError(
					fmt.Errorf("unreachable clause: %s is already matched by the %s clause on line %d",
						clause.TypePattern.Name, prev.TypePattern.Name, prev.Loc.Line),
					clause.TypePattern,
				)
			}
		}
	}
	return nil
}

// valueModule extracts the *Type backing a runtime value.
func valueModule(val Value) *Type {
	switch v := val.(type) {
	case *Object:
		if v.Mod != nil {
			if mod, ok := v.Mod.(*Type); ok {
				return mod
			}
		}
	case GraphQLValue:
		if v.TypeScope != nil {
			if typeScope, found := v.TypeScope.NamedType(v.TypeName); found {
				if mod, ok := typeScope.(*Type); ok {
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
		if mod, ok := v.ScalarType.(*Type); ok {
			return mod
		}
	case EnumValue:
		if mod, ok := v.EnumType.(*Type); ok {
			return mod
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
	resolvedMemberType *Type
}

var _ SourceLocatable = (*CaseClause)(nil)

func (c *CaseClause) IsTypePattern() bool {
	return c.TypePattern != nil
}

func (c *CaseClause) GetSourceLocation() *SourceLocation {
	return c.Loc
}
