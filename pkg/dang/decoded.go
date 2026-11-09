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
// (`1.50` is `1.5`). Which format decoded them, and whether strictly, does
// not matter.
func decodedEqual(l, r DeferredValue) bool {
	lr, err := canonicalDecoded(l)
	if err != nil {
		return false
	}
	rr, err := canonicalDecoded(r)
	if err != nil {
		return false
	}
	return canonicalJSONKey(lr) == canonicalJSONKey(rr)
}

// isNullDecoded reports whether v is Decoded data holding null, which equals
// null wherever it came from.
func isNullDecoded(v Value) bool {
	d, ok := v.(DeferredValue)
	return ok && d.Raw == nil
}

// decodedComparable reports whether an equality between left and right
// compares Decoded data with Decoded data of the same shape (up to
// nullability), or with the null literal. Comparing Decoded data with any
// other type stays an error: the data has no type to compare at.
func decodedComparable(left Node, lt hm.Type, right Node, rt hm.Type) bool {
	if isNullNode(left) || isNullNode(right) {
		return true
	}
	return stripNonNulls(lt).Eq(stripNonNulls(rt))
}

// stripNonNulls removes non-null wrappers from t and its list and map
// element types.
func stripNonNulls(t hm.Type) hm.Type {
	switch v := t.(type) {
	case hm.NonNullType:
		return stripNonNulls(v.Type)
	case ListType:
		return ListType{stripNonNulls(v.Type)}
	case MapType:
		return MapType{stripNonNulls(v.Type)}
	default:
		return t
	}
}
