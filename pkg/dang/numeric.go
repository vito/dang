package dang

import (
	"fmt"

	"github.com/vito/dang/v2/pkg/hm"
)

// numericKinds names the Numeric types: those the arithmetic operators
// (+ - * / %) take, sum adds up, and a type parameter bounded by Numeric
// stands for.
const numericKinds = "Numeric types are Int! and Float!"

// whyNotNumeric is Numeric's predicate (see BuiltinConstraint.Why): it
// explains why values of type t aren't numbers, or returns "" when they are,
// or might: a type not yet known (a flexible type variable) passes, and is
// appended to needs. what names the value in the explanation.
func whyNotNumeric(c *BuiltinConstraint, t hm.Type, what string, needs *[]hm.Need) string {
	inner, nonNull := stripNonNull(t)
	switch tv := inner.(type) {
	case hm.TypeVariable:
		return notNumericVar(c, tv, nonNull, what, needs)
	case hm.NullableTypeVariable:
		if nonNull {
			return notNumericVar(c, tv.TypeVariable, true, what, needs)
		}
		if tv.IsRigid() && !tv.Has(c.Constraint) {
			return notNumericVar(c, tv.TypeVariable, true, what, needs)
		}
		return fmt.Sprintf("%s may be null; supply a default with ??", what)
	}
	if _, decoded := inner.(DecodedType); decoded {
		return fmt.Sprintf("%s is decoded data; give it a type first, e.g. `(JSON.decode(...) :: T)`", what)
	}
	if union, ok := inner.(*hm.UnionType); ok && !knownNullable(union) {
		// `if (c) { 1 } else { 2.5 }`: each member is a number, but a
		// bounded variable stands for one type.
		return fmt.Sprintf("%s mixes types; return one type from every branch", what)
	}
	if isNumeric(inner) {
		if !nonNull {
			return fmt.Sprintf("%s may be null; supply a default with ??", what)
		}
		return ""
	}
	if what != "it" {
		what = fmt.Sprintf("%s (%s)", what, t)
	}
	return fmt.Sprintf("%s is not a number; %s", what, numericKinds)
}

// notNumericVar is whyNotNumeric for a type variable tv, written non-null
// (`a!`) or not. A flexible variable may still come to stand for a number,
// so it passes, as one of the needs. A rigid one stands for whatever its
// declaration says: a number if it is bounded by Numeric, and then possibly
// null unless written `a!`.
func notNumericVar(c *BuiltinConstraint, tv hm.TypeVariable, nonNull bool, what string, needs *[]hm.Need) string {
	if !tv.IsRigid() {
		if needs != nil {
			*needs = append(*needs, hm.Need{Var: tv, NonNull: nonNull})
		}
		return ""
	}
	name := tv.Unbounded()
	if !tv.Has(c.Constraint) {
		return fmt.Sprintf("%s is of the generic type %s, which isn't known to be a number; declare it `[%s: %s]`", what, name, name, c.Name)
	}
	if !nonNull {
		return fmt.Sprintf("%s is of the generic type %s, which may be null; write %s! for its non-null values", what, name, name)
	}
	return ""
}

// numericVar returns the rigid type variable t is, nullable or not, when its
// declaration bounds it by Numeric (`[a: Numeric]`).
func numericVar(t hm.Type) (hm.TypeVariable, bool) {
	inner, _ := stripNonNull(t)
	var tv hm.TypeVariable
	switch v := inner.(type) {
	case hm.TypeVariable:
		tv = v
	case hm.NullableTypeVariable:
		tv = v.TypeVariable
	default:
		return 0, false
	}
	return tv, tv.IsRigid() && tv.Has(numericConstraint.Constraint)
}

// numbersMix reports whether two operand types (non-null stripped) are both
// numbers, which mix in comparisons as in arithmetic: Int and Float, or a
// Numeric-bounded variable with either (`x < 0` for `[a: Ordered &
// Numeric]`) or with itself.
func numbersMix(l, r hm.Type) bool {
	lv, lok := numericVar(l)
	rv, rok := numericVar(r)
	switch {
	case lok && rok:
		return lv == rv
	case lok:
		return isNumeric(r)
	case rok:
		return isNumeric(l)
	}
	return isNumeric(l) && isNumeric(r)
}

// resolveNumericVars types an arithmetic operator with an operand of a type
// variable bounded by Numeric (`a!`, from `[a: Numeric]`), reporting whether
// either operand is one. Bound, a stands for Int! or Float!, so:
//
//   - a! op a! is a!;
//   - a! op Int! (either way round) is a!, as Int op Int is Int and Float op
//     Int is Float;
//   - a! op Float! is Float!, as either kind of number with a Float is;
//   - a! op b! is rejected: with a Int and b Float its type would be neither.
//
// Only the numeric meaning of + applies: a Numeric variable's values don't
// concatenate with strings or lists. A bounded a may be null, so its values
// are refused until written a!.
func (b *BinaryOperator) resolveNumericVars(env hm.Env, lt, rt hm.Type) (hm.Type, bool, error) {
	lv, lok := numericVar(lt)
	rv, rok := numericVar(rt)
	if !lok && !rok {
		return nil, false, nil
	}
	c := numericConstraint
	sides := []struct {
		node    Node
		t       hm.Type
		bounded bool
	}{{b.Left, lt, lok}, {b.Right, rt, rok}}
	for _, side := range sides {
		if !side.bounded {
			continue
		}
		if why := c.Why(c, side.t, "it", nil); why != "" {
			return nil, true, NewInferError(fmt.Errorf("operator %s is not defined for type %s: %s", b.OpName, side.t, why), side.node)
		}
	}
	if lok && rok {
		if lv != rv {
			return nil, true, fmt.Errorf("operator %s is not defined between types %s and %s: they may be different Numeric types (one Int, the other Float); use one type parameter for both", b.OpName, lt, rt)
		}
		return hm.NonNullType{Type: lv}, true, nil
	}
	numT, other, otherNode := hm.Type(hm.NonNullType{Type: lv}), rt, b.Right
	if rok {
		numT, other, otherNode = hm.NonNullType{Type: rv}, lt, b.Left
	}
	if _, ok := unconstrainedVar(other); ok {
		// Bounding it by Numeric too wouldn't help: two Numeric
		// parameters don't mix (see above).
		return nil, true, NewInferError(fmt.Errorf("operator %s is not defined between types %s and %s: %s is a Numeric type, and a type variable supports no operations; give it the same type (%s)", b.OpName, lt, rt, numT, numT), otherNode)
	}
	if knownNullable(other) {
		return nil, true, b.nullableOperandError(env, otherNode, other)
	}
	if hasFreeVar(other) {
		// Not known yet: it must be the same number type.
		if _, err := hm.Assignable(other, numT); err != nil {
			return nil, true, err
		}
		return numT, true, nil
	}
	switch ob, _ := stripNonNull(other); ob {
	case IntType:
		return numT, true, nil
	case FloatType:
		return hm.NonNullType{Type: FloatType}, true, nil
	}
	return nil, true, withUnionProvenance(
		fmt.Errorf("operator %s is not defined between types %s and %s: %s is a Numeric type, so only numbers go with it", b.OpName, lt, rt, numT), lt, rt)
}
