package dang

import (
	"encoding/json"
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
