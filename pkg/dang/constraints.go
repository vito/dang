package dang

import (
	"fmt"
	"slices"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// BuiltinConstraint is a constraint a type parameter may be bounded by
// (`[a: Ordered]`). It is not a type: it names the types that satisfy it,
// which a bounded type variable then stands for, and the operators their
// values support.
type BuiltinConstraint struct {
	// Name is how a bound names it.
	Name string
	// Why explains why values of type t don't satisfy the constraint, or
	// returns "" when they do, or might: a flexible type variable passes,
	// appended to needs, the variables the constraint then needs of the
	// type (hm.ConstraintHost). what names the value in the explanation.
	// A rigid variable satisfies it when bounded by it (c.Constraint); the
	// predicate decides what else it requires (Ordered's must be non-null).
	Why func(c *BuiltinConstraint, t hm.Type, what string, needs *[]hm.Need) string
	// Operators names the operators (BinaryOperator.OpName) that values of
	// a type variable bounded by the constraint support, which an
	// unbounded one's don't: the error for those says to declare the
	// bound.
	Operators []string

	// Constraint is the constraint's identity in hm, which bounded type
	// variables carry.
	Constraint hm.Constraint
}

// builtinConstraints are the constraints there are, by name.
var builtinConstraints = map[string]*BuiltinConstraint{}

// newBuiltinConstraint registers a constraint, with hm too.
func newBuiltinConstraint(c *BuiltinConstraint) *BuiltinConstraint {
	c.Constraint = hm.RegisterConstraint(c.Name, func(t hm.Type) ([]hm.Need, string) {
		var needs []hm.Need
		why := c.Why(c, t, "it", &needs)
		return needs, why
	})
	builtinConstraints[c.Name] = c
	return c
}

// orderedConstraint is Ordered: the types whose values have an order, which
// sort and sortBy order and the ordering operators compare (whyUnordered).
var orderedConstraint = newBuiltinConstraint(&BuiltinConstraint{
	Name:      "Ordered",
	Why:       whyUnordered,
	Operators: []string{"less_than", "greater_than", "less_than_equal", "greater_than_equal"},
})

// lookupConstraint finds a constraint named by a bound.
func lookupConstraint(bound *NamedTypeNode) (*BuiltinConstraint, bool) {
	if bound.Base != nil {
		return nil, false
	}
	c, ok := builtinConstraints[bound.Name]
	return c, ok
}

// constraintEnabling returns the constraint whose bound gives a type
// variable's values the operator op, if any.
func constraintEnabling(op string) (*BuiltinConstraint, bool) {
	for _, c := range builtinConstraints {
		for _, o := range c.Operators {
			if o == op {
				return c, true
			}
		}
	}
	return nil, false
}

// constraintNames lists the constraints for messages, in a stable order.
func constraintNames() string {
	var names []string
	for name := range builtinConstraints {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// constraintAsTypeError reports a constraint written where a type goes.
func constraintAsTypeError(c *BuiltinConstraint) error {
	return fmt.Errorf("%s is a constraint, not a type: declare a type parameter bounded by it, like `[a: %s]`, and use that (`x: a!`)", c.Name, c.Name)
}
