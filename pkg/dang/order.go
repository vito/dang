package dang

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/vito/dang/v2/pkg/hm"
)

// orderedKinds names the Ordered types: those sort, sortBy and the ordering
// operators (< > <= >=) can order, and a type parameter bounded by Ordered
// stands for.
const orderedKinds = "Ordered types are Int!, Float!, String!, Boolean!, enums, scalars, and lists and {{ }} records of those"

func init() {
	hm.OrderedHost = func(t hm.Type) ([]hm.OrderedNeed, string) {
		var needs []hm.OrderedNeed
		why := unordered(t, "it", &needs)
		return needs, why
	}
}

// checkSortKey rejects, at type-check time, sorting by values of type key
// that have no order: what orderer.compare would fail on at runtime. Values
// of a type not yet known (a flexible type variable) pass, and are checked as
// they are compared.
func checkSortKey(prefix string, key hm.Type) error {
	if why := whyUnordered(key, "it"); why != "" {
		return fmt.Errorf("%s %s: %s", prefix, key, why)
	}
	return nil
}

// whyUnordered explains why values of type t have no order, or returns ""
// when they do, or might: a type not yet known (a flexible type variable)
// passes. what names the value in the explanation. It is the one definition
// of which types are Ordered, which the Ordered bound on type variables
// checks too (see hm.OrderedHost).
func whyUnordered(t hm.Type, what string) string {
	return unordered(t, what, nil)
}

// unordered is whyUnordered, collecting into needs, when it isn't nil, the
// flexible variables the order of t still depends on.
func unordered(t hm.Type, what string, needs *[]hm.OrderedNeed) string {
	inner, nonNull := stripNonNull(t)
	switch tv := inner.(type) {
	case hm.TypeVariable:
		return unorderedVar(tv, nonNull, what, needs)
	case hm.NullableTypeVariable:
		if nonNull {
			return unorderedVar(tv.TypeVariable, true, what, needs)
		}
		if tv.IsRigid() && !tv.IsOrdered() {
			return unorderedVar(tv.TypeVariable, true, what, needs)
		}
		return fmt.Sprintf("%s may be null; supply a default with ??", what)
	}
	if _, decoded := inner.(DecodedType); decoded {
		return fmt.Sprintf("%s is decoded data; give it a type first, e.g. `(JSON.decode(...) :: T)`", what)
	}
	if union, ok := inner.(*hm.UnionType); ok && !knownNullable(union) {
		// Branches returning different types (`if (c) { 1 } else { 2.5 }`):
		// never null, but no single order covers every member.
		return fmt.Sprintf("%s mixes types; return one type from every branch", what)
	}
	if !nonNull {
		return fmt.Sprintf("%s may be null; supply a default with ??", what)
	}
	switch it := inner.(type) {
	case ListType:
		return unordered(it.Type, "an element", needs)
	case *Type:
		if it.IsMeta() {
			break
		}
		switch {
		case it.Kind == ScalarKind && it != ListTypeModule && it != MapTypeModule,
			it.Kind == EnumKind:
			return ""
		case it.Kind == ObjectKind && it.Named == "":
			for name, scheme := range it.Bindings(PrivateVisibility) {
				ft, _ := scheme.Type()
				if isMethodType(ft) {
					continue
				}
				if why := unordered(ft, "field "+name, needs); why != "" {
					return why
				}
			}
			return ""
		}
	}
	if what != "it" {
		what = fmt.Sprintf("%s (%s)", what, t)
	}
	return fmt.Sprintf("%s has no order; %s", what, orderedKinds)
}

// unorderedVar is unordered for a type variable tv, written non-null
// (`a!`) or not. A flexible variable may still come to stand for an Ordered
// type, so it passes, as one of the needs. A rigid one stands for whatever
// its declaration says: Ordered if it is bounded so, and then possibly null
// unless written `a!`.
func unorderedVar(tv hm.TypeVariable, nonNull bool, what string, needs *[]hm.OrderedNeed) string {
	if !tv.IsRigid() {
		if needs != nil {
			*needs = append(*needs, hm.OrderedNeed{Var: tv, NonNull: nonNull})
		}
		return ""
	}
	name := tv.Unbounded()
	if !tv.IsOrdered() {
		return fmt.Sprintf("%s is of the generic type %s, which has no known order; declare it `[%s: Ordered]`", what, name, name)
	}
	if !nonNull {
		return fmt.Sprintf("%s is of the generic type %s, which may be null; write %s! for its non-null values", what, name, name)
	}
	return ""
}

// sortKeyOfList returns the element type of a list type, for checking sort.
func sortKeyOfList(t hm.Type) hm.Type {
	inner, _ := stripNonNull(t)
	if list, ok := inner.(ListType); ok {
		return list.Type
	}
	return t
}

// sortStable returns elems ordered by their keys (keys[i] is elems[i]'s),
// ascending or descending, keeping elements with equal keys in their input
// order.
func sortStable(elems, keys []Value, descending bool) ([]Value, error) {
	idx := make([]int, len(elems))
	for i := range idx {
		idx[i] = i
	}
	o := &orderer{}
	var err error
	slices.SortStableFunc(idx, func(a, b int) int {
		if err != nil {
			return 0
		}
		var c int
		c, err = o.compare(keys[a], keys[b])
		if descending {
			c = -c
		}
		return c
	})
	if err != nil {
		return nil, err
	}
	sorted := make([]Value, len(elems))
	for i, j := range idx {
		sorted[i] = elems[j]
	}
	return sorted, nil
}

// orderer compares values in the order sort and sortBy define: numbers
// numerically, strings and scalars by their bytes, false before true, enum
// values in declaration order, and lists and {{ }} records element by element
// (a list that is a prefix of another comes first).
type orderer struct {
	enums  map[*Type]map[string]int
	fields map[*Type][]string
}

func (o *orderer) compare(a, b Value) (int, error) {
	switch l := a.(type) {
	case IntValue:
		switch r := b.(type) {
		case IntValue:
			return cmp.Compare(l.Val, r.Val), nil
		case FloatValue:
			return cmp.Compare(float64(l.Val), r.Val), nil
		}
	case FloatValue:
		switch r := b.(type) {
		case IntValue:
			return cmp.Compare(l.Val, float64(r.Val)), nil
		case FloatValue:
			return cmp.Compare(l.Val, r.Val), nil
		}
	case StringValue:
		switch r := b.(type) {
		case StringValue:
			return strings.Compare(l.Val, r.Val), nil
		case ScalarValue:
			return strings.Compare(l.Val, r.Val), nil
		}
	case ScalarValue:
		switch r := b.(type) {
		case StringValue:
			return strings.Compare(l.Val, r.Val), nil
		case ScalarValue:
			// A scalar whose GraphQL value is a number (a timestamp, a
			// BigInt) holds its JSON text: order it numerically.
			if l.Encoded && r.Encoded {
				ln, lok := new(big.Rat).SetString(l.Val)
				rn, rok := new(big.Rat).SetString(r.Val)
				if lok && rok {
					return ln.Cmp(rn), nil
				}
			}
			return strings.Compare(l.Val, r.Val), nil
		}
	case BoolValue:
		if r, ok := b.(BoolValue); ok {
			return cmp.Compare(boolRank(l.Val), boolRank(r.Val)), nil
		}
	case EnumValue:
		if r, ok := b.(EnumValue); ok {
			return o.compareEnums(l, r), nil
		}
	case ListValue:
		if r, ok := b.(ListValue); ok {
			for i := range min(len(l.Elements), len(r.Elements)) {
				c, err := o.compare(l.Elements[i], r.Elements[i])
				if err != nil || c != 0 {
					return c, err
				}
			}
			return cmp.Compare(len(l.Elements), len(r.Elements)), nil
		}
	case *Object:
		if r, ok := b.(*Object); ok {
			return o.compareRecords(l, r)
		}
	}
	if _, ok := a.(NullValue); ok {
		return 0, fmt.Errorf("cannot sort by null")
	}
	if _, ok := b.(NullValue); ok {
		return 0, fmt.Errorf("cannot sort by null")
	}
	return 0, fmt.Errorf("cannot order %s against %s; %s", a.Type(), b.Type(), orderedKinds)
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// compareEnums orders two values of an enum by its declaration order.
func (o *orderer) compareEnums(l, r EnumValue) int {
	enum, ok := l.EnumType.(*Type)
	if !ok || l.EnumType != r.EnumType {
		return strings.Compare(l.Val, r.Val)
	}
	if o.enums == nil {
		o.enums = map[*Type]map[string]int{}
	}
	ranks, ok := o.enums[enum]
	if !ok {
		ranks = map[string]int{}
		for i, name := range enumValueNames(enum) {
			ranks[name] = i
		}
		o.enums[enum] = ranks
	}
	lr, lok := ranks[l.Val]
	rr, rok := ranks[r.Val]
	if !lok || !rok {
		return strings.Compare(l.Val, r.Val)
	}
	return cmp.Compare(lr, rr)
}

// compareRecords orders two {{ }} records field by field, in the order
// their fields are written. Record types are structural, so two records of
// one type may have been written with their fields in different orders; with
// no order both agree on, comparing them is an error rather than a choice
// that would depend on which operand came first.
func (o *orderer) compareRecords(l, r *Object) (int, error) {
	lt, lok := l.Mod.(*Type)
	rt, rok := r.Mod.(*Type)
	if !lok || !rok || lt.Named != "" || rt.Named != "" {
		return 0, fmt.Errorf("cannot order %s against %s; %s", l.Type(), r.Type(), orderedKinds)
	}
	lf, rf := o.recordFields(lt), o.recordFields(rt)
	if !slices.Equal(lf, rf) {
		return 0, fmt.Errorf("cannot order records written with their fields in different orders ({{%s}} and {{%s}}): write every record key's fields in one order",
			strings.Join(lf, ", "), strings.Join(rf, ", "))
	}
	for _, name := range lf {
		lv, lfound := l.lookupValue(name)
		rv, rfound := r.lookupValue(name)
		if !lfound || !rfound {
			return 0, fmt.Errorf("cannot order records: field %q is missing", name)
		}
		c, err := o.compare(lv, rv)
		if err != nil || c != 0 {
			return c, err
		}
	}
	return 0, nil
}

// recordFields lists a record type's data fields in order.
func (o *orderer) recordFields(t *Type) []string {
	if fields, ok := o.fields[t]; ok {
		return fields
	}
	var fields []string
	for name, scheme := range t.Bindings(PrivateVisibility) {
		if ft, _ := scheme.Type(); !isMethodType(ft) {
			fields = append(fields, name)
		}
	}
	if o.fields == nil {
		o.fields = map[*Type][]string{}
	}
	o.fields[t] = fields
	return fields
}
