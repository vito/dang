package dang

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

	// nullableDecoded is set during inference when the operand is nullable
	// Decoded: there, null data is the operand being null, so it takes only
	// a `null` arm or else, however the null was decoded or passed along.
	nullableDecoded bool
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
		if clause.PatternType != nil {
			symbols = append(symbols, clause.PatternType.ReferencedSymbols()...)
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

		if unionHoldsDecoded(exprType) {
			// Its arms would mix two kinds of matching: by a value's
			// type for the other options, and by the data's shape for
			// the Decoded one, which could also match those arms.
			return nil, NewInferError(fmt.Errorf("a case can't match %s, a union holding decoded data: separate the decoded data first, e.g. rescue to null rather than to a value of another type", exprType), c.Expr)
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
				// Type pattern clause: binding: TypeName! => expr
				if err := c.inferTypePatternClause(ctx, env, fresh, clause, exprType); err != nil {
					return nil, err
				}
				if err := checkClauseReachable(clause, nil, priorPatterns); err != nil {
					return nil, err
				}
				// An arm after one taking every non-null value of the
				// operand never runs either.
				if clause.matchType != nil {
					for _, prev := range priorPatterns {
						if prev.matchType != nil && typeCoveredBy(exprType, []hm.Type{prev.matchType}) {
							return nil, NewInferError(
								fmt.Errorf("unreachable clause: the %s clause on line %d already matches every %s value",
									prev.patternSource(), prev.Loc.Line, hm.NonNull(unwrapNonNull(exprType))),
								clause.patternNode(),
							)
						}
					}
				}
				priorPatterns = append(priorPatterns, clause)

				// Infer the clause body in a scoped env with the binding,
				// of the type the arm matches: for a named pattern on a
				// union narrowed by inline fragments, the narrowed member.
				caseType, err = inferArm(clause, func() (hm.Type, error) {
					return WithInferErrorHandling(clause, func() (hm.Type, error) {
						clauseEnv := env.Clone()
						bound := clause.matchType
						if clause.decodedPatternType != nil {
							bound = clause.decodedPatternType
						}
						clauseEnv = clauseEnv.Add(clause.Binding, hm.NewScheme(nil, bound))
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
					// itself, pointing at the literal (checkLiterals).
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
		inner, nullable := decodedOperand(exprType)
		c.nullableDecoded = inner && nullable
		if inner && !hasElse {
			hasTypeArm, catchAll := false, false
			for _, clause := range c.Clauses {
				if clause.decodedPatternType == nil {
					continue
				}
				hasTypeArm = true
				// A Decoded arm takes any non-null data; null data
				// needs a `null` pattern too.
				if isDecodedCatchAll(clause) {
					catchAll = true
				}
			}
			switch {
			case catchAll && (!nullable || hasNullPattern(valuePatterns)):
				hasElse = true
				c.exhaustive = true
			case catchAll:
				return nil, NewInferError(fmt.Errorf("a case on nullable decoded data needs a `null` arm or an else: a Decoded arm takes no null"), c)
			case hasTypeArm:
				// Decoded data may have the shape of none of the
				// patterns' types, and nothing about its type says which.
				return nil, NewInferError(fmt.Errorf("a case on decoded data needs an else: the data may match none of the patterns"), c)
			}
		}
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
// coversOperand). See typeCoveredBy.
func (c *Case) coversAllMembers(exprType hm.Type) bool {
	var patterns []hm.Type
	for _, clause := range c.Clauses {
		if clause.IsTypePattern() && clause.matchType != nil {
			patterns = append(patterns, clause.matchType)
		}
	}
	if len(patterns) == 0 {
		return false
	}
	return typeCoveredBy(exprType, patterns)
}

// inferTypePatternClause validates a type pattern clause against the operand
// type and resolves the type the arm matches and binds: on decoded data, the
// type whose shape the data must have (decodedPatternType); on anything
// else, a type that some value of the operand's type may inhabit
// (matchType), such as a member of a union operand.
func (c *Case) inferTypePatternClause(ctx context.Context, env hm.Env, fresh hm.Fresher, clause *CaseClause, exprType hm.Type) error {
	if _, ok := env.(TypeScope); !ok {
		return NewInferError(fmt.Errorf("type patterns require a module environment"), clause)
	}
	if clause.BarePattern {
		return NewInferError(fmt.Errorf("a type pattern is the non-null type its arm binds: write `%s: %s! =>`", clause.Binding, clause.PatternName()), clause.patternNode())
	}

	// Unwrap NonNull
	unwrapped := exprType
	if nn, ok := unwrapped.(hm.NonNullType); ok {
		unwrapped = nn.Type
	}

	if _, ok := unwrapped.(DecodedType); ok {
		// Decoded data matches a pattern when it has its type's shape.
		var pattern TypeNode = clause.PatternType
		if pattern == nil {
			if clause.PatternBase != nil {
				// Report a bad qualified name as on any other operand.
				if _, err := clause.lookupNamedPattern(ctx, env, fresh); err != nil {
					return NewInferError(err, clause.TypePattern)
				}
			}
			pattern = clause.namedPatternNode()
		}
		t, err := pattern.Infer(ctx, env, fresh)
		if err != nil {
			return WrapInferError(err, clause.patternNode())
		}
		if _, nn := t.(hm.NonNullType); !nn {
			t = hm.NonNullType{Type: t}
		}
		if len(t.FreeTypeVar()) > 0 {
			return NewInferError(fmt.Errorf("a pattern on decoded data needs a concrete type, not %s", t), clause.patternNode())
		}
		if bad, at := abstractDecodeTarget(t); bad != nil {
			if at != "" {
				return NewInferError(fmt.Errorf("decoded data never materializes as %s %s, which %s holds", bad.Kind, bad.Name(), at), clause.patternNode())
			}
			hint := "match its member types instead"
			if bad.Kind == InterfaceKind {
				hint = "match the types implementing it instead"
			}
			return NewInferError(fmt.Errorf("decoded data never materializes as %s %s: %s", bad.Kind, bad.Name(), hint), clause.patternNode())
		}
		if err := checkDecodeTarget(env, t); err != nil {
			return WrapInferError(err, clause.patternNode())
		}
		clause.decodedPatternType = t
		return nil
	}
	if clause.TypePattern != nil {
		memberMod, ok, err := c.resolveMemberPattern(ctx, env, fresh, clause, unwrapped)
		if err != nil {
			return err
		}
		if ok {
			// Store the resolved member type for use when inferring the
			// clause body
			clause.resolvedMemberType = memberMod
			clause.matchType = hm.NonNullType{Type: memberMod}
			clause.idTakesStrings = idPatternTakesStrings(clause.matchType, exprType)
			return nil
		}
	}

	// Any other pattern matches the values inhabiting its type, which some
	// value of the operand's type must be able to.
	t, err := clause.patternTypeNode().Infer(ctx, env, fresh)
	if err != nil {
		if clause.TypePattern != nil {
			if _, lookupErr := clause.lookupNamedPattern(ctx, env, fresh); lookupErr != nil {
				return NewInferError(lookupErr, clause.TypePattern)
			}
		}
		return WrapInferError(err, clause.patternNode())
	}
	t = hm.NonNullType{Type: unwrapNonNull(t)}
	if holdsTypeVars(t) {
		return NewInferError(fmt.Errorf("a type pattern needs a concrete type, not %s", t), clause.patternNode())
	}
	if holdsNarrowed(exprType) {
		// It would bind a value holding only the selected fields as a
		// type that has others.
		return NewInferError(fmt.Errorf("pattern %s can't match %s, a selection narrowed by inline fragments: match one of the fragments' types", t, exprType), clause.patternNode())
	}
	if !typesOverlap(t, exprType) {
		if _, gqlList := unwrapped.(GraphQLListType); gqlList {
			return NewInferError(fmt.Errorf("pattern %s can't match a GraphQL object list (%s); select fields on its elements first, e.g. value.{{id}}", t, exprType), clause.patternNode())
		}
		return NewInferError(fmt.Errorf("pattern %s can't match a value of type %s", t, exprType), clause.patternNode())
	}
	if mod, ok := unwrapNonNull(t).(*Type); ok && !isRecordModule(mod) {
		clause.resolvedMemberType = mod
	}
	clause.matchType = t
	clause.idTakesStrings = idPatternTakesStrings(t, exprType)
	return nil
}

// idPatternTakesStrings reports whether an arm of type pattern on an operand
// of type operand takes plain strings as IDs (see matchMode.idStrings): the
// pattern holds ID somewhere, and the operand's type holds no String, which
// a plain string would be instead.
func idPatternTakesStrings(pattern, operand hm.Type) bool {
	return mentionsModule(pattern, IDType) && !mentionsModule(operand, StringType)
}

// mentionsModule reports whether t is, or holds as an option, element or
// record field, the named type mod.
func mentionsModule(t hm.Type, mod *Type) bool {
	if opts, ok := unionOptions(t); ok {
		return slices.ContainsFunc(opts, func(o hm.Type) bool { return mentionsModule(o, mod) })
	}
	if elem, ok := listElementTarget(t); ok {
		return mentionsModule(elem, mod)
	}
	if val, ok := mapValueTarget(t); ok {
		return mentionsModule(val, mod)
	}
	m, ok := unwrapNonNull(t).(*Type)
	if !ok {
		return false
	}
	if canonicalModule(m) == mod {
		return true
	}
	if isRecordModule(m) {
		for _, field := range m.AsRecord().Fields {
			if ft, mono := field.Value.Type(); mono && mentionsModule(ft, mod) {
				return true
			}
		}
	}
	return false
}

// matches reports whether val, the operand, takes the clause's type pattern
// arm (see valueInhabits).
func (c *CaseClause) matches(val Value, byContents bool) bool {
	return valueInhabits(val, c.matchType, matchMode{byContents: byContents, idStrings: c.idTakesStrings})
}

// resolveMemberPattern resolves a named type pattern on a union, interface
// or inline union operand the way it always has: to the operand's member of
// that name (a narrowed one, for a selection's inline fragments), or the
// interface itself or a type implementing it. ok is false where the pattern
// is resolved like any other type pattern instead: on another operand, or a
// type that the operand doesn't list but may still hold (a union whose
// members implement an interface operand, say). A type that can't match at
// all keeps its error naming the union or interface.
func (c *Case) resolveMemberPattern(ctx context.Context, env hm.Env, fresh hm.Fresher, clause *CaseClause, unwrapped hm.Type) (*Type, bool, error) {
	patternName := clause.PatternName()
	// overlaps reports whether the named type is still a valid pattern on
	// the operand, though not one of the members listed. Not on a union
	// narrowed by inline fragments: its members hold only the selected
	// fields, so a pattern must name one of them to bind what they hold.
	overlaps := func() bool {
		if holdsNarrowed(unwrapped) {
			return false
		}
		named, err := clause.lookupNamedPattern(ctx, env, fresh)
		return err == nil && typesOverlap(hm.NonNullType{Type: named}, unwrapped)
	}
	if operandMod, ok := unwrapped.(*Type); ok {
		switch operandMod.Kind {
		case UnionKind:
			// A qualified pattern names its type outright, so it is looked
			// up first and then found among the operand's members by
			// identity.
			var named *Type
			if clause.PatternBase != nil {
				var err error
				named, err = clause.lookupNamedPattern(ctx, env, fresh)
				if err != nil {
					return nil, false, NewInferError(err, clause.TypePattern)
				}
			}
			for _, m := range operandMod.GetMembers() {
				mod, ok := m.(*Type)
				if !ok {
					continue
				}
				if named != nil && canonicalModule(mod) == canonicalModule(named) ||
					named == nil && mod.Name() == clause.TypePattern.Name {
					return mod, true, nil
				}
			}
			if overlaps() {
				return nil, false, nil
			}
			return nil, false, NewInferError(fmt.Errorf("type %s is not a member of union %s", patternName, operandMod), clause.TypePattern)
		case InterfaceKind:
			named, err := clause.lookupNamedPattern(ctx, env, fresh)
			if err != nil {
				return nil, false, NewInferError(err, clause.TypePattern)
			}
			memberMod, err := resolveInterfaceTypePattern(operandMod, named, patternName)
			if err == nil {
				return memberMod, true, nil
			}
			if overlaps() {
				return nil, false, nil
			}
			return nil, false, NewInferError(err, clause.TypePattern)
		}
		return nil, false, nil
	}
	if operandUnion, ok := unwrapped.(*hm.UnionType); ok {
		named, err := clause.lookupNamedPattern(ctx, env, fresh)
		if err != nil {
			return nil, false, NewInferError(err, clause.TypePattern)
		}
		memberMod, err := resolveInlineUnionTypePattern(operandUnion, named, patternName)
		if err == nil {
			return memberMod, true, nil
		}
		if overlaps() {
			return nil, false, nil
		}
		return nil, false, NewInferError(err, clause.TypePattern)
	}
	return nil, false, nil
}

// resolveInterfaceTypePattern checks that memberMod, the type a pattern
// names, matches values of iface: iface itself (a typed catch-all) or an
// implementer.
func resolveInterfaceTypePattern(iface *Type, memberMod *Type, patternName string) (*Type, error) {
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

// resolveInlineUnionTypePattern checks that patternMod, the type a pattern
// names, is one of union's options or a member of one.
func resolveInlineUnionTypePattern(union *hm.UnionType, patternMod *Type, patternName string) (*Type, error) {
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
			if _, err := resolveInterfaceTypePattern(optionMod, patternMod, patternName); err == nil {
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
		if clause.TypePattern != nil && clause.PatternBase == nil {
			// A qualified pattern's name is not a bare reference to a
			// type in scope (see PatternBase), so it isn't walked as one.
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
			if clause.IsTypePattern() && clause.decodedPatternType != nil {
				// Decoded data matches an arm by its shape alone, which runs
				// no user code; the first matching arm's data is then
				// materialized like a cast, and its errors (a scalar hook
				// rejecting the data, a constructor raising) are the case's.
				data, ok := typedVal.(DeferredValue)
				if !ok || (c.nullableDecoded && data.Raw == nil) ||
					!decodedShapeMatches(ctx, scope, data, clause.decodedPatternType) {
					continue
				}
				matched, err := materializeDeferred(ctx, scope, data, clause.decodedPatternType, "")
				if err != nil {
					return nil, err
				}
				childScope := scope.Derive(false)
				childScope.Bind(clause.Binding, matched, PrivateVisibility)
				return EvalNode(ctx, childScope, clause.Expr)
			}
			if clause.IsTypePattern() {
				if clause.matches(typedVal, false) {
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
			// Inference proved a clause must match, by the operand's static
			// type. A list, map or record matches by the type it recorded,
			// which is that type or a narrower one, so one should have; in
			// case one recorded a type its static type doesn't fit, the
			// first arm whose type its contents inhabit takes it rather than
			// failing.
			for _, clause := range c.Clauses {
				if clause.IsTypePattern() && clause.matchType != nil &&
					clause.matches(typedVal, true) {
					childScope := scope.Derive(false)
					childScope.Bind(clause.Binding, typedVal, PrivateVisibility)
					return EvalNode(ctx, childScope, clause.Expr)
				}
			}
			// Falling through here is a compiler bug, not a user error.
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
			// a null to fall through to `null` patterns or else. A handle
			// is never a list, map or record, so only a named pattern can
			// match its abstract type.
			return g.MaybeNull || clause.resolvedMemberType == nil ||
				!matchesType(g, clause.resolvedMemberType)
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
		if cl.TypePattern != nil && cl.PatternName() != g.TypeName {
			fragments = append(fragments, fmt.Sprintf("... on %s {{ ... }}", cl.PatternName()))
		}
	}
	return fmt.Errorf(
		"cannot match type pattern %s against a %s reached through mutation %s: finding its concrete type would send the mutation again; select each possible type's fields on the mutation call instead, e.g. .{{ %s }}",
		clause.PatternName(), g.TypeName, g.Name, strings.Join(fragments, ", "),
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
	// Union: check if the value's type is one of its members, or (for a
	// member union or interface) belongs to one.
	if patCanon.Kind == UnionKind {
		for _, m := range patCanon.GetMembers() {
			if mm, ok := m.(*Type); ok && matchesType(val, mm) {
				return true
			}
		}
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

// typePatternCovers reports whether an earlier type pattern already matches
// every value a later pattern would: the later one's type fits the earlier
// one's (see typeFits), which mirrors valueInhabits: a value inhabiting the
// later type, or a list or map recording a type that fits it, inhabits the
// earlier one too. Strictly pairwise — patterns that only *collectively*
// cover a later one leave it reachable, so a defensive else after exhaustive
// union patterns stays legal.
func typePatternCovers(earlier, later hm.Type) bool {
	return typeFits(later, earlier)
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
	if clause.IsTypePattern() && clause.matchType != nil {
		for _, prev := range priorPatterns {
			if prev.matchType != nil && typePatternCovers(prev.matchType, clause.matchType) {
				return NewInferError(
					fmt.Errorf("unreachable clause: %s is already matched by the %s clause on line %d",
						clause.patternSource(), prev.patternSource(), prev.Loc.Line),
					clause.patternNode(),
				)
			}
		}
	}
	if clause.IsTypePattern() && clause.decodedPatternType != nil {
		// On decoded data, an arm is dead after one of the same type, or
		// after a Decoded arm, which takes any data.
		for _, prev := range priorPatterns {
			if prev.decodedPatternType == nil {
				continue
			}
			if isDecodedCatchAll(prev) || decodedPatternCovers(prev.decodedPatternType, clause.decodedPatternType) {
				return NewInferError(
					fmt.Errorf("unreachable clause: %s is already matched by the %s clause on line %d",
						clause.patternSource(), prev.patternSource(), prev.Loc.Line),
					clause.patternNode(),
				)
			}
		}
	}
	return nil
}

// decodedPatternCovers reports whether every piece of data with the shape of
// cur also has the shape of prev (see shapeMatcher), so an arm for cur after
// one for prev never runs. Besides the same type: `Float` covers `Int`; a
// String or scalar arm (which takes any string) covers String, scalar and
// enum arms, and a schema scalar without a hook takes any data at all; a
// list or map arm covers one whose elements its elements cover, and
// `Map[Decoded]` covers every object.
func decodedPatternCovers(prev, cur hm.Type) bool {
	if prev.Eq(cur) {
		return true
	}
	_, prevNonNull := prev.(hm.NonNullType)
	_, curNonNull := cur.(hm.NonNullType)
	p, c := unwrapNonNull(prev), unwrapNonNull(cur)
	if _, ok := p.(DecodedType); ok {
		// Decoded and Decoded! both take any data, null included.
		return true
	}
	if prevNonNull && !curNonNull {
		// cur takes null where prev doesn't.
		return false
	}
	if p.Eq(c) {
		return true
	}
	if pmod, ok := p.(*Type); ok && pmod.Kind == ScalarKind && holdsAnyData(pmod) {
		return true
	}
	if val, ok := mapValueTarget(p); ok {
		if cval, ok := mapValueTarget(c); ok {
			return decodedPatternCovers(val, cval)
		}
		if _, any := unwrapNonNull(val).(DecodedType); any {
			mod, ok := c.(*Type)
			return ok && (mod.Kind == ObjectKind || mod.Kind == InputKind)
		}
		return false
	}
	if elem, ok := listElementTarget(p); ok {
		celem, ok := listElementTarget(c)
		return ok && decodedPatternCovers(elem, celem)
	}
	if p == FloatType && c == IntType {
		return true
	}
	if takesAnyString(p) {
		if c == StringType {
			return true
		}
		cmod, ok := c.(*Type)
		return ok && (cmod.Kind == ScalarKind || cmod.Kind == EnumKind) && !isPrimitiveScalar(cmod)
	}
	return false
}

// takesAnyString reports whether an arm of type t matches every string:
// String, or any scalar besides the primitives (see shapeMatcher).
func takesAnyString(t hm.Type) bool {
	if t == StringType {
		return true
	}
	mod, ok := t.(*Type)
	return ok && mod.Kind == ScalarKind && !isPrimitiveScalar(mod)
}

// unionHoldsDecoded reports whether a case operand is an inline union with
// a Decoded option, like `JSON.decode(t) rescue 0` (Decoded | Int!).
func unionHoldsDecoded(exprType hm.Type) bool {
	union, ok := unwrapNonNull(exprType).(*hm.UnionType)
	if !ok {
		return false
	}
	for _, option := range union.Options {
		if _, decoded := unwrapNonNull(option).(DecodedType); decoded {
			return true
		}
	}
	return false
}

// decodedOperand reports whether a case operand is decoded data, and whether
// it may be null.
func decodedOperand(exprType hm.Type) (decoded, nullable bool) {
	inner := exprType
	if nn, ok := exprType.(hm.NonNullType); ok {
		inner = nn.Type
	} else {
		nullable = true
	}
	_, decoded = inner.(DecodedType)
	return decoded, nullable
}

// isDecodedCatchAll reports whether a clause on decoded data is a Decoded
// arm, which takes any non-null data as it is.
func isDecodedCatchAll(clause *CaseClause) bool {
	if clause.decodedPatternType == nil {
		return false
	}
	_, ok := unwrapNonNull(clause.decodedPatternType).(DecodedType)
	return ok
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
	Binding     string  // variable name for type pattern (e.g. "user" in "user: User! => ...")
	TypePattern *Symbol // type name for type pattern (e.g. "User" in "user: User! => ...")
	// PatternBase qualifies TypePattern: the `Lib` in `g: Lib.Greeter! =>`,
	// resolved like the same qualified type written anywhere else.
	PatternBase *NamedTypeNode
	// PatternType is any other type pattern (`items: [Decoded!]! => ...`,
	// `obj: Map[Int!]! => ...`, `r: {{name: String!}}! => ...`).
	//
	// A type pattern is written as the non-null type its arm binds; the
	// clause keeps the type without its `!` (see typePatternClause).
	PatternType TypeNode
	// BarePattern marks a type pattern written without its `!` where that
	// is no longer allowed, for inference to reject with a hint.
	BarePattern bool
	// PatternBang records that a type pattern was written with its `!`,
	// for the formatter to keep it where the `!` is optional (see
	// barePatterns). It is spelling only, so migration ignores it.
	PatternBang bool `dang:"spelling"`
	// PatternLoc is where a type pattern is written, `!` included.
	PatternLoc *SourceLocation
	Loc        *SourceLocation

	// resolvedMemberType is set during inference to the concrete member type
	// for this type pattern clause. For narrowed unions (from inline fragment
	// selections), this is the narrowed type with only the selected fields.
	// It is nil for patterns that are not named types.
	resolvedMemberType *Type

	// matchType is set during inference when the operand is not decoded
	// data: the non-null type the arm matches (see valueInhabits) and binds.
	matchType hm.Type

	// idTakesStrings is set during inference for an ID pattern on an
	// operand that can't be a String: an ID from a GraphQL result is a
	// plain string at runtime, which only its static type tells apart.
	idTakesStrings bool

	// decodedPatternType is set during inference when the operand is
	// decoded data: the non-null type whose shape the data must have for
	// the clause to match, and which the matching data materializes at.
	decodedPatternType hm.Type
}

var _ SourceLocatable = (*CaseClause)(nil)

func (c *CaseClause) IsTypePattern() bool {
	return c.TypePattern != nil || c.PatternType != nil
}

// PatternName is the type a type pattern names, as written but without its
// `!`: `User`, `Lib.User`, `[Decoded!]`.
func (c *CaseClause) PatternName() string {
	if c.TypePattern != nil {
		return typeNodeString(c.namedPatternNode())
	}
	if c.PatternType != nil {
		return typeNodeString(c.PatternType)
	}
	return ""
}

// patternSource is a type pattern as it is written: `User!`, `[Decoded!]!`.
func (c *CaseClause) patternSource() string {
	return c.PatternName() + "!"
}

// patternTypeNode is the type a type pattern names, without its `!`.
func (c *CaseClause) patternTypeNode() TypeNode {
	if c.TypePattern != nil {
		return c.namedPatternNode()
	}
	return c.PatternType
}

// namedPatternNode is a named type pattern as the type node it spells,
// qualifier included.
func (c *CaseClause) namedPatternNode() *NamedTypeNode {
	return &NamedTypeNode{Base: c.PatternBase, Name: c.TypePattern.Name, Loc: c.TypePattern.Loc}
}

// lookupNamedPattern resolves a named type pattern to the type it names: a
// qualified one like any qualified type, an unqualified one by name in env.
func (c *CaseClause) lookupNamedPattern(ctx context.Context, env hm.Env, fresh hm.Fresher) (*Type, error) {
	name := c.PatternName()
	var t hm.Type
	if c.PatternBase != nil {
		resolved, err := c.namedPatternNode().Infer(ctx, env, fresh)
		if err != nil {
			return nil, fmt.Errorf("unknown type %s in type pattern", name)
		}
		t = unwrapNonNull(resolved)
	} else {
		typeScope, ok := env.(TypeScope)
		if !ok {
			return nil, fmt.Errorf("type patterns require a module environment")
		}
		found, ok := typeScope.NamedType(c.TypePattern.Name)
		if !ok {
			return nil, fmt.Errorf("unknown type %s in type pattern", name)
		}
		t = found
	}
	mod, ok := t.(*Type)
	if !ok {
		return nil, fmt.Errorf("type pattern %s is not a named type", name)
	}
	return mod, nil
}

// patternNode is the node to blame for a type pattern's errors.
func (c *CaseClause) patternNode() SourceLocatable {
	if c.TypePattern != nil {
		return c.TypePattern
	}
	if c.PatternLoc != nil {
		return patternLocation{c.PatternLoc}
	}
	return c
}

// patternLocation blames a pattern's errors on where it is written.
type patternLocation struct{ loc *SourceLocation }

func (p patternLocation) GetSourceLocation() *SourceLocation { return p.loc }

// abstractDecodeTarget returns an interface or union that materializing t
// would have to build, which decoded data never materializes as, with the
// field path that holds it ("" at t itself). At t itself it looks through
// non-null, lists and maps; below that, only through the fields every match
// needs — an anonymous record's non-null fields and a named object's
// non-null constructor parameters without defaults, except those every
// codec ignores — since data can match a type that holds an interface only
// in a nullable or defaulted field, or in a list that may be empty.
func abstractDecodeTarget(t hm.Type) (*Type, string) {
	seen := map[*Type]bool{}
	var walk func(t hm.Type, via string) (*Type, string)
	walkFields := func(mod *Type, fields []Keyed[*hm.Scheme], via string) (*Type, string) {
		for _, field := range fields {
			if ignoredByAllCodecs(mod, field.Key) {
				continue
			}
			ft, mono := field.Value.Type()
			if !mono {
				continue
			}
			if _, required := ft.(hm.NonNullType); !required {
				continue
			}
			next := field.Key
			if via != "" {
				next = via + "." + field.Key
			} else if mod.Named != "" {
				next = mod.Named + "." + field.Key
			}
			if bad, at := walk(ft, next); bad != nil {
				return bad, at
			}
		}
		return nil, ""
	}
	walk = func(t hm.Type, via string) (*Type, string) {
		t = unwrapNonNull(t)
		if via == "" {
			if elem, ok := listElementTarget(t); ok {
				return walk(elem, via)
			}
			if val, ok := mapValueTarget(t); ok {
				return walk(val, via)
			}
		}
		v, ok := t.(*Type)
		if !ok {
			return nil, ""
		}
		mod := canonicalModule(v)
		if mod.Kind == InterfaceKind || mod.Kind == UnionKind {
			return mod, via
		}
		if seen[mod] || mod.Kind != ObjectKind {
			return nil, ""
		}
		seen[mod] = true
		if mod.Named == "" {
			return walkFields(mod, mod.AsRecord().Fields, via)
		}
		if mod.constructorType != nil {
			if args, ok := mod.constructorType.Arg().(*RecordType); ok {
				return walkFields(mod, args.Fields, via)
			}
		}
		return nil, ""
	}
	return walk(t, "")
}

// typeNodeString writes a type as source spells it, for messages.
func typeNodeString(t TypeNode) string {
	switch tn := t.(type) {
	case *NamedTypeNode:
		if tn.Base != nil {
			return typeNodeString(tn.Base) + "." + tn.Name
		}
		return tn.Name
	case NonNullTypeNode:
		return typeNodeString(tn.Elem) + "!"
	case ListTypeNode:
		return "[" + typeNodeString(tn.Elem) + "]"
	case *AppliedTypeNode:
		args := make([]string, len(tn.Args))
		for i, a := range tn.Args {
			args[i] = typeNodeString(a)
		}
		return typeNodeString(tn.Base) + "[" + strings.Join(args, ", ") + "]"
	case ObjectTypeNode:
		fields := make([]string, len(tn.Fields))
		for i, field := range tn.Fields {
			fields[i] = field.Key + ": " + typeNodeString(field.Type)
		}
		return "{{" + strings.Join(fields, ", ") + "}}"
	case VariableTypeNode:
		return string(rune(tn.Name))
	default:
		return fmt.Sprintf("%v", t)
	}
}

func (c *CaseClause) GetSourceLocation() *SourceLocation {
	return c.Loc
}
