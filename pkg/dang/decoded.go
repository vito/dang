package dang

import (
	"encoding/json"

	"github.com/vito/dang/v2/pkg/hm"
)

// Decoded data — a DeferredValue — holds the neutral tree a format decoded:
// map[string]any, []any, string, bool, json.Number or nil. Its numbers keep
// the spelling the format wrote them in, which for YAML need not be a JSON
// number (`-.5`, `+1`, `0755`); these helpers see the tree with numbers
// spelled canonically, as `:: Int!` or `:: Float!` would read them.

// canonicalDecoded returns d's data with canonical numbers, copied, never
// modified.
func canonicalDecoded(d DeferredValue) (any, error) {
	return canonicalJSONNumbers(d.Raw)
}

// decodedJSON is d's data as JSON, for printing it.
func decodedJSON(d DeferredValue) ([]byte, error) {
	raw, err := canonicalDecoded(d)
	if err != nil {
		return nil, err
	}
	return json.Marshal(raw)
}

// decodedEqual compares two Decoded values as data: objects by their keys
// and values in any order, lists element by element, numbers by value
// (`1.50` is `1.5`, YAML's `-.5` is `-0.5`; a number with no exact value,
// like YAML's `.inf`, by its spelling). Which format decoded them, and
// whether strictly, does not matter.
func decodedEqual(l, r DeferredValue) bool {
	return canonicalJSONKey(l.Raw) == canonicalJSONKey(r.Raw)
}

// isNullDecoded reports whether v is Decoded data holding null, which equals
// null wherever it came from.
func isNullDecoded(v Value) bool {
	d, ok := v.(DeferredValue)
	return ok && d.Raw == nil
}

// decodedComparable reports whether an equality between left and right
// compares Decoded data with Decoded data of the same shape (up to
// nullability, through lists, maps and records), with an empty literal of
// that shape, or with the null literal. Comparing Decoded data with any other
// type stays an error, as does a union that only might be Decoded: the data
// has no type to compare at.
func decodedComparable(left Node, lt hm.Type, right Node, rt hm.Type) bool {
	if isNullNode(left) {
		return isDecodedShape(rt)
	}
	if isNullNode(right) {
		return isDecodedShape(lt)
	}
	return decodedShapesMatch(lt, rt)
}

// isDecodedShape reports whether t is Decoded, or a list, map or record built
// only of Decoded.
func isDecodedShape(t hm.Type) bool {
	return decodedShapesMatch(t, t)
}

// decodedShapesMatch reports whether a and b are the same shape built of
// Decoded, up to nullability: Decoded itself, or lists, maps or records of
// it. A type variable (an empty literal's element) matches any such shape.
func decodedShapesMatch(a, b hm.Type) bool {
	a, b = unwrapNonNull(a), unwrapNonNull(b)
	aVar, bVar := isTypeVar(a), isTypeVar(b)
	switch {
	case aVar && bVar:
		return false
	case aVar:
		return isDecodedShape(b)
	case bVar:
		return isDecodedShape(a)
	}
	switch av := a.(type) {
	case DecodedType:
		_, ok := b.(DecodedType)
		return ok
	case ListType:
		bv, ok := b.(ListType)
		return ok && decodedShapesMatch(av.Type, bv.Type)
	case MapType:
		bv, ok := b.(MapType)
		return ok && decodedShapesMatch(av.Type, bv.Type)
	case *Type:
		bv, ok := b.(*Type)
		if !ok || av.Kind != ObjectKind || bv.Kind != ObjectKind || av.Named != "" || bv.Named != "" {
			return false
		}
		af, bf := av.AsRecord().Fields, bv.AsRecord().Fields
		if len(af) != len(bf) {
			return false
		}
		byName := make(map[string]*hm.Scheme, len(bf))
		for _, f := range bf {
			byName[f.Key] = f.Value
		}
		for _, f := range af {
			other, ok := byName[f.Key]
			if !ok {
				return false
			}
			at, mono := f.Value.Type()
			bt, bmono := other.Type()
			if !mono || !bmono || !decodedShapesMatch(at, bt) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isTypeVar(t hm.Type) bool {
	switch t.(type) {
	case hm.TypeVariable, hm.NullableTypeVariable:
		return true
	default:
		return false
	}
}
