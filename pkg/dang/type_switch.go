package dang

import (
	"context"
	"slices"

	"github.com/vito/dang/v2/pkg/hm"
)

// A case is a type switch: a type pattern's arm takes the values that
// inhabit the pattern's type, the non-null type it binds. This file holds
// the one "value inhabits type" rule and its static counterparts, which
// decide whether a pattern can match an operand at all (typesOverlap), and
// whether arms cover an operand or each other (typeFits).
//
//   - Named types (objects, interfaces, unions, scalars, enums) match by
//     identity: the value's own type is the pattern's, implements it, or is
//     one of its members (matchesType).
//   - Lists and maps match by the element type they recorded when they were
//     built, their static type there (see recordedElemType): `[Cat(),
//     Dog()]` holds Cat!s and Dog!s, so it matches `[Pet!]!`, not `[Cat!]!`,
//     and a `[Animal!]!` list matches `[Cat!]!` only if it was built as one,
//     whatever it holds. Where that type still has type variables (a list
//     built in a generic body, or an empty literal), the elements decide.
//   - Records match by their field set and the fields' types, the same way.
//   - Null never matches: every pattern is non-null.
//
// Decoded data is matched by its shape instead (see decodedShapeMatches).

// holdsTypeVars reports whether t is unknown or still holds type variables,
// anonymous records' fields included, so that it can't say statically which
// types it fits.
func holdsTypeVars(t hm.Type) bool {
	switch t := t.(type) {
	case nil:
		return true
	case hm.TypeVariable:
		return true
	case hm.NonNullType:
		return holdsTypeVars(t.Type)
	case ListType:
		return holdsTypeVars(t.Type)
	case GraphQLListType:
		return holdsTypeVars(t.Type)
	case MapType:
		return holdsTypeVars(t.Type)
	case *hm.UnionType:
		return slices.ContainsFunc(t.Options, holdsTypeVars)
	case *Type:
		if !isRecordModule(t) {
			return false
		}
		for _, field := range t.AsRecord().Fields {
			ft, mono := field.Value.Type()
			if !mono || holdsTypeVars(ft) {
				return true
			}
		}
		return false
	}
	return len(t.FreeTypeVar()) > 0
}

// isRecordModule reports whether mod is an anonymous record type, like
// `{{name: String!}}`.
func isRecordModule(mod *Type) bool {
	mod = canonicalModule(mod)
	return mod.Named == "" && mod.Kind == ObjectKind
}

// unionOptions returns the options of an inline union, made non-null when
// the union itself is.
func unionOptions(t hm.Type) ([]hm.Type, bool) {
	if nn, ok := t.(hm.NonNullType); ok {
		u, ok := nn.Type.(*hm.UnionType)
		if !ok {
			return nil, false
		}
		opts := make([]hm.Type, len(u.Options))
		for i, o := range u.Options {
			opts[i] = hm.NonNull(o)
		}
		return opts, true
	}
	u, ok := t.(*hm.UnionType)
	if !ok {
		return nil, false
	}
	return u.Options, true
}

// typeFits reports whether every value of type have is a value of type want,
// as far as the types say: false if either still holds type variables.
// Lists and maps are covariant; records need the same fields, each fitting.
func typeFits(have, want hm.Type) bool {
	if holdsTypeVars(have) || holdsTypeVars(want) {
		return false
	}
	return fits(have, want)
}

func fits(have, want hm.Type) bool {
	if opts, ok := unionOptions(have); ok {
		for _, o := range opts {
			if !fits(o, want) {
				return false
			}
		}
		return len(opts) > 0
	}
	if opts, ok := unionOptions(want); ok {
		return slices.ContainsFunc(opts, func(o hm.Type) bool { return fits(have, o) })
	}
	_, haveNonNull := have.(hm.NonNullType)
	_, wantNonNull := want.(hm.NonNullType)
	if wantNonNull && !haveNonNull {
		return false
	}
	have, want = unwrapNonNull(have), unwrapNonNull(want)
	switch w := want.(type) {
	case ListType:
		h, ok := have.(ListType)
		return ok && fits(h.Type, w.Type)
	case GraphQLListType:
		h, ok := have.(GraphQLListType)
		return ok && fits(h.Type, w.Type)
	case MapType:
		h, ok := have.(MapType)
		return ok && fits(h.Type, w.Type)
	case *Type:
		h, ok := have.(*Type)
		return ok && moduleFits(h, w)
	}
	return have.Eq(want)
}

// moduleFits reports whether every value of the named type or record have is
// a value of want: the same type, a type implementing want or belonging to
// it (transitively), a union all of whose members fit, or a record with the
// same fields, each fitting.
func moduleFits(have, want *Type) bool {
	return moduleFitsVia(have, want, map[*Type]bool{})
}

// moduleFitsVia is moduleFits, skipping the types in progress: an object's
// supertypes include the unions it belongs to, whose members include it.
func moduleFitsVia(have, want *Type, inProgress map[*Type]bool) bool {
	h, w := canonicalModule(have), canonicalModule(want)
	if h == w {
		return true
	}
	if inProgress[h] {
		return false
	}
	inProgress[h] = true
	defer delete(inProgress, h)
	if isRecordModule(h) || isRecordModule(w) {
		return isRecordModule(h) && isRecordModule(w) &&
			recordFieldsMatch(h, w, fits)
	}
	for _, super := range h.Supertypes() {
		if sm, ok := super.(*Type); ok && moduleFitsVia(sm, w, inProgress) {
			return true
		}
	}
	if h.Kind == UnionKind {
		members := h.GetMembers()
		for _, m := range members {
			mm, ok := m.(*Type)
			if !ok || !moduleFitsVia(mm, w, inProgress) {
				return false
			}
		}
		return len(members) > 0
	}
	return false
}

// recordFieldsMatch reports whether records a and b have the same fields,
// with each field's types related by rel.
func recordFieldsMatch(a, b *Type, rel func(a, b hm.Type) bool) bool {
	af, bf := a.AsRecord().Fields, b.AsRecord().Fields
	if len(af) != len(bf) {
		return false
	}
	for i := range af {
		if af[i].Key != bf[i].Key {
			return false
		}
		at, amono := af[i].Value.Type()
		bt, bmono := bf[i].Value.Type()
		if !amono || !bmono || !rel(at, bt) {
			return false
		}
	}
	return true
}

// typesOverlap reports whether some non-null value inhabits both pattern
// and operand, so that an arm of that pattern can match. It is
// conservative: types it can't prove overlap, such as two unrelated
// interfaces or a type variable, don't.
func typesOverlap(pattern, operand hm.Type) bool {
	if opts, ok := unionOptions(operand); ok {
		return slices.ContainsFunc(opts, func(o hm.Type) bool { return typesOverlap(pattern, o) })
	}
	if opts, ok := unionOptions(pattern); ok {
		return slices.ContainsFunc(opts, func(p hm.Type) bool { return typesOverlap(p, operand) })
	}
	pattern, operand = unwrapNonNull(pattern), unwrapNonNull(operand)
	if holdsTypeVars(pattern) || holdsTypeVars(operand) {
		return false
	}
	switch o := operand.(type) {
	case ListType:
		p, ok := pattern.(ListType)
		return ok && typesOverlap(p.Type, o.Type)
	case MapType:
		p, ok := pattern.(MapType)
		return ok && typesOverlap(p.Type, o.Type)
	case *Type:
		p, ok := pattern.(*Type)
		return ok && modulesOverlap(p, o)
	}
	return pattern.Eq(operand)
}

// modulesOverlap reports whether some value inhabits both named types or
// records: one fits the other, they share a union member, or one is a union
// with a member inhabiting the other (e.g. implementing an interface).
func modulesOverlap(a, b *Type) bool {
	a, b = canonicalModule(a), canonicalModule(b)
	if moduleFits(a, b) || moduleFits(b, a) {
		return true
	}
	if isRecordModule(a) || isRecordModule(b) {
		return isRecordModule(a) && isRecordModule(b) &&
			recordFieldsMatch(a, b, typesOverlap)
	}
	for _, pair := range [][2]*Type{{a, b}, {b, a}} {
		union, other := pair[0], pair[1]
		if union.Kind != UnionKind {
			continue
		}
		for _, m := range union.GetMembers() {
			if mm, ok := m.(*Type); ok && modulesOverlap(mm, other) {
				return true
			}
		}
	}
	return false
}

// typeCoveredBy reports whether every non-null value of t matches one of
// the patterns: t, or each of its options, fits one of them, and a named
// union is covered when each of its members is. An interface is covered
// only by a pattern it fits, since its implementers are open.
func typeCoveredBy(t hm.Type, patterns []hm.Type) bool {
	if opts, ok := unionOptions(t); ok {
		for _, o := range opts {
			if _, nn := o.(hm.NonNullType); !nn {
				// A nullable option admits null, which no pattern matches.
				return false
			}
			if !typeCoveredBy(o, patterns) {
				return false
			}
		}
		return len(opts) > 0
	}
	inner := unwrapNonNull(t)
	nonNull := hm.NonNullType{Type: inner}
	for _, p := range patterns {
		if typeFits(nonNull, p) {
			return true
		}
	}
	if mod, ok := inner.(*Type); ok {
		mod = canonicalModule(mod)
		if mod.Kind == UnionKind {
			members := mod.GetMembers()
			for _, m := range members {
				mm, ok := m.(*Type)
				if !ok || !typeCoveredBy(mm, patterns) {
					return false
				}
			}
			return len(members) > 0
		}
	}
	return false
}

// valueInhabits reports whether val is a value of type t, by the rules at
// the top of this file. With byContents, lists, maps and records are judged
// by what they hold instead of the types they recorded (see Case.Eval).
func valueInhabits(val Value, t hm.Type, byContents bool) bool {
	if opts, ok := unionOptions(t); ok {
		return slices.ContainsFunc(opts, func(o hm.Type) bool { return valueInhabits(val, o, byContents) })
	}
	_, isNull := val.(NullValue)
	if nn, ok := t.(hm.NonNullType); ok {
		if isNull || val == nil {
			return false
		}
		t = nn.Type
	} else if isNull {
		return true
	}
	switch want := t.(type) {
	case ListType:
		elem := want.Type
		list, ok := val.(ListValue)
		if !ok {
			return false
		}
		return recordedFits(list.ElemType, elem, byContents, func() bool {
			for _, e := range list.Elements {
				if !valueInhabits(e, elem, byContents) {
					return false
				}
			}
			return true
		})
	case MapType:
		m, ok := val.(MapValue)
		if !ok {
			return false
		}
		return recordedFits(m.ValType, want.Type, byContents, func() bool {
			for _, k := range m.Keys {
				if !valueInhabits(m.Entries[k], want.Type, byContents) {
					return false
				}
			}
			return true
		})
	case DecodedType:
		_, ok := val.(DeferredValue)
		return ok
	case *Type:
		if isRecordModule(want) {
			return recordInhabits(val, want, byContents)
		}
		return matchesType(val, want)
	}
	return false
}

// recordedFits decides whether a list or map that recorded the element type
// recorded holds values of want: by that type, unless it still holds type
// variables (or byContents is set), where elementwise decides.
func recordedFits(recorded, want hm.Type, byContents bool, elementwise func() bool) bool {
	if byContents || holdsTypeVars(recorded) {
		return elementwise()
	}
	return typeFits(recorded, want)
}

// recordInhabits reports whether val is a record with the fields of want,
// each of a type that fits (or, where its type holds type variables, a
// value that inhabits) the pattern's field type.
func recordInhabits(val Value, want *Type, byContents bool) bool {
	obj, ok := val.(*Object)
	if !ok {
		return false
	}
	mod, ok := obj.Mod.(*Type)
	if !ok || !isRecordModule(mod) {
		return false
	}
	have, wantFields := canonicalModule(mod).AsRecord().Fields, want.AsRecord().Fields
	if len(have) != len(wantFields) {
		return false
	}
	for i, field := range have {
		if field.Key != wantFields[i].Key {
			return false
		}
		wt, mono := wantFields[i].Value.Type()
		if !mono {
			return false
		}
		ht, _ := field.Value.Type()
		if !recordedFits(ht, wt, byContents, func() bool {
			fv, found := obj.lookupValue(field.Key)
			return found && valueInhabits(fv, wt, byContents)
		}) {
			return false
		}
	}
	return true
}

// recordedElemType returns the element type a list or map built as a value
// of static type t records (see valueInhabits): the element type of t, a
// list or map type, or nil where t isn't known to be one.
func recordedElemType(t hm.Type) hm.Type {
	if t == nil {
		return nil
	}
	if elem, ok := listElementTarget(t); ok {
		return elem
	}
	if val, ok := mapValueTarget(t); ok {
		return val
	}
	return nil
}

// joinRecordedTypes returns the element type to record for a list holding
// the elements of two lists that recorded a and b: the one the other fits,
// or else what both merge to. An empty side holds no elements, so only the
// other one's type counts. Where neither fits the other and they don't
// merge, or either holds type variables, the result is a type variable, so
// the elements decide.
func joinRecordedTypes(a hm.Type, aEmpty bool, b hm.Type, bEmpty bool) hm.Type {
	switch {
	case aEmpty && bEmpty:
		return a
	case aEmpty:
		return b
	case bEmpty:
		return a
	case holdsTypeVars(a) || holdsTypeVars(b):
		return unknownElemType
	case typeFits(a, b):
		return b
	case typeFits(b, a):
		return a
	}
	if merged, _, err := hm.MergeTypes(a, b); err == nil && !holdsTypeVars(merged) {
		return merged
	}
	return unknownElemType
}

// unknownElemType is the element type a list or map records when its
// elements' static type is unknown: a type variable, so they decide what it
// matches (see recordedFits).
var unknownElemType hm.Type = hm.TypeVariable('a')

// recordedOr returns t, the static type a list or map records for its
// elements, or unknownElemType where it has none.
func recordedOr(t hm.Type) hm.Type {
	if t == nil {
		return unknownElemType
	}
	return t
}

// recordedRuntimeType is recordedOr for a list or map built where ctx is:
// its static element type with weak variables resolved and the type
// variables of the generic calls ctx is in replaced by what they stand for
// (see runtimeType). Variables left over make the elements decide.
func recordedRuntimeType(ctx context.Context, t hm.Type) hm.Type {
	return recordedOr(runtimeType(ctx, t))
}

// concatLists returns the list holding l's elements, then r's. It records
// the element type of static, the concatenation's static type, like a list
// literal does; where that isn't known (or holds type variables), a type both
// sides' recorded types fit.
func concatLists(l, r ListValue, static hm.Type) ListValue {
	combined := make([]Value, len(l.Elements)+len(r.Elements))
	copy(combined, l.Elements)
	copy(combined[len(l.Elements):], r.Elements)
	elem := recordedElemType(static)
	if holdsTypeVars(elem) {
		elem = joinRecordedTypes(l.ElemType, len(l.Elements) == 0, r.ElemType, len(r.Elements) == 0)
	}
	return ListValue{Elements: combined, ElemType: elem}
}
