package dang

import (
	"github.com/vito/dang/pkg/hm"
)

// NullAssertion represents a null check that can be used for type narrowing
type NullAssertion struct {
	// Symbol is the variable being checked for null
	Symbol string
	// IsNonNullAssertion is true for "x != null", false for "x == null"
	IsNonNullAssertion bool
}

// AnalyzeNullAssertions examines a conditional expression and extracts null assertions
// that can be used for type narrowing in the conditional branches.
func AnalyzeNullAssertions(condition Node) []NullAssertion {
	var assertions []NullAssertion

	switch cond := condition.(type) {
	case *Equality:
		// Check for patterns like "x == null" or "null == x"
		if assertion := analyzeEqualityForNull(cond, false); assertion != nil {
			assertions = append(assertions, *assertion)
		}
	case *Inequality:
		// Check for patterns like "x != null" or "null != x"
		if assertion := analyzeInequalityForNull(cond, true); assertion != nil {
			assertions = append(assertions, *assertion)
		}
		// TODO: Add support for compound conditions with && and ||
	}

	return assertions
}

// analyzeEqualityForNull checks if an equality expression is a null check
func analyzeEqualityForNull(eq *Equality, isNonNull bool) *NullAssertion {
	// Check "symbol == null"
	if symbol := extractSymbolName(eq.Left); symbol != "" && isNullLiteral(eq.Right) {
		return &NullAssertion{
			Symbol:             symbol,
			IsNonNullAssertion: isNonNull,
		}
	}

	// Check "null == symbol"
	if isNullLiteral(eq.Left) {
		if symbol := extractSymbolName(eq.Right); symbol != "" {
			return &NullAssertion{
				Symbol:             symbol,
				IsNonNullAssertion: isNonNull,
			}
		}
	}

	return nil
}

// analyzeInequalityForNull checks if an inequality expression is a null check
func analyzeInequalityForNull(ineq *Inequality, isNonNull bool) *NullAssertion {
	// Check "symbol != null"
	if symbol := extractSymbolName(ineq.Left); symbol != "" && isNullLiteral(ineq.Right) {
		return &NullAssertion{
			Symbol:             symbol,
			IsNonNullAssertion: isNonNull,
		}
	}

	// Check "null != symbol"
	if isNullLiteral(ineq.Left) {
		if symbol := extractSymbolName(ineq.Right); symbol != "" {
			return &NullAssertion{
				Symbol:             symbol,
				IsNonNullAssertion: isNonNull,
			}
		}
	}

	return nil
}

// extractSymbolName extracts the symbol name from a node if it's a simple symbol reference
func extractSymbolName(node Node) string {
	if symbol, ok := node.(*Symbol); ok {
		return symbol.Name
	}
	return ""
}

// isNullLiteral checks if a node is a null literal
func isNullLiteral(node Node) bool {
	_, ok := node.(*Null)
	return ok
}

// TypeRefinement represents a type refinement that should be applied in a conditional branch
type TypeRefinement struct {
	Symbol string
	Type   hm.Type
}

// CreateTypeRefinements converts null assertions into type refinements for conditional branches
func CreateTypeRefinements(assertions []NullAssertion, env hm.Env, fresh hm.Fresher) ([]TypeRefinement, []TypeRefinement, error) {
	var thenRefinements []TypeRefinement
	var elseRefinements []TypeRefinement

	for _, assertion := range assertions {
		// Get the current type of the symbol
		if scheme, found := env.SchemeOf(assertion.Symbol); found {
			currentType, isMono := scheme.Type()
			if !isMono {
				// Skip polymorphic types for now
				continue
			}

			// Create non-null version of the type
			nonNullType, err := makeNonNull(currentType, env, fresh)
			if err != nil {
				return nil, nil, err
			}

			if assertion.IsNonNullAssertion {
				// In the "then" branch, the symbol is non-null
				thenRefinements = append(thenRefinements, TypeRefinement{
					Symbol: assertion.Symbol,
					Type:   nonNullType,
				})
				// In the "else" branch, the symbol remains nullable (no refinement needed)
			} else {
				// In the "then" branch, the symbol is null (keep original type)
				// In the "else" branch, the symbol is non-null
				elseRefinements = append(elseRefinements, TypeRefinement{
					Symbol: assertion.Symbol,
					Type:   nonNullType,
				})
			}
		}
	}

	return thenRefinements, elseRefinements, nil
}

// makeNonNull converts a potentially nullable type to its non-null version
func makeNonNull(t hm.Type, env hm.Env, fresh hm.Fresher) (hm.Type, error) {
	// If it's already a NonNull type, return as-is
	if _, isNonNull := t.(hm.NonNullType); isNonNull {
		return t, nil
	}

	// For any other type, wrap in NonNull
	return hm.NonNullType{Type: t}, nil
}

// ApplyTypeRefinements creates a new environment with type refinements applied
func ApplyTypeRefinements(env hm.Env, refinements []TypeRefinement) hm.Env {
	if len(refinements) == 0 {
		return env
	}

	// Clone the environment to avoid modifying the original
	refined := env.Clone()

	// Apply each refinement
	for _, refinement := range refinements {
		// Create a monomorphic scheme for the refined type
		scheme := hm.NewScheme(nil, refinement.Type)
		refined = refined.Add(refinement.Symbol, scheme)
	}

	return refined
}
