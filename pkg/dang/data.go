package dang

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// DataNumber is a number in decoded data, as the format spelled it: the
// number leaf of the neutral tree every codec decodes into (alongside
// map[string]any, []any, string, bool and nil). A YAML number keeps its
// source text (`0755`, `+1`, `-.5`); canonicalNumbers respells such numbers
// as the value `:: Int!` or `:: Float!` reads.
type DataNumber string

func (n DataNumber) String() string { return string(n) }

// Int64 is the number as an int64, if it is spelled as one.
func (n DataNumber) Int64() (int64, error) {
	return strconv.ParseInt(string(n), 10, 64)
}

// Float64 is the number as a float64.
func (n DataNumber) Float64() (float64, error) {
	return strconv.ParseFloat(string(n), 64)
}

// MarshalJSON writes the number as itself. A spelling that is not a JSON
// number is an error: canonicalNumbers respells numbers first where that
// matters.
func (n DataNumber) MarshalJSON() ([]byte, error) {
	if !json.Valid([]byte(n)) {
		return nil, fmt.Errorf("number %s has no JSON spelling", string(n))
	}
	return []byte(n), nil
}

// dataFromJSON turns JSON decoded with UseNumber into the neutral tree,
// in place: json.Number becomes DataNumber.
func dataFromJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		return DataNumber(x)
	case []any:
		for i := range x {
			x[i] = dataFromJSON(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = dataFromJSON(x[k])
		}
		return x
	default:
		return v
	}
}

// canonicalDataKey renders decoded data so that equal data renders the
// same: object keys sorted, strings quoted the same way, and numbers by value
// (as a fraction, so `1.50`, `1.5` and `15e-1` agree).
func canonicalDataKey(raw any) string {
	var b strings.Builder
	writeCanonicalData(&b, raw)
	return b.String()
}

func writeCanonicalData(b *strings.Builder, raw any) {
	switch x := raw.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(k))
			b.WriteByte(':')
			writeCanonicalData(b, x[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonicalData(b, e)
		}
		b.WriteByte(']')
	case DataNumber:
		b.WriteString(canonicalNumber(x))
	case string:
		b.WriteString(strconv.Quote(x))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case nil:
		b.WriteString("null")
	default:
		fmt.Fprintf(b, "%v", x)
	}
}

// canonicalNumber spells a number by its value. A huge exponent is left as
// written rather than expanded, which only costs equality across spellings of
// such numbers.
func canonicalNumber(n DataNumber) string {
	s := n.String()
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exp, err := strconv.Atoi(strings.TrimPrefix(s[i+1:], "+"))
		if err != nil || exp > 1000 || exp < -1000 {
			return "#" + s
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "#" + s
	}
	return r.RatString()
}

// canonicalNumbers returns raw with every number spelled plainly: an
// integer or a decimal float, the way JSON and TOML decode them already.
// YAML keeps a number's source text (`0755`, `+1`, `-.5`); those are
// respelled as the value `:: Int!` or `:: Float!` would read. A number with
// no finite value (YAML's `.inf`) is an error. Containers are copied, never
// modified: decoded data may be materialized more than once.
func canonicalNumbers(raw any) (any, error) {
	switch x := raw.(type) {
	case DataNumber:
		if json.Valid([]byte(x)) {
			return x, nil
		}
		if i, err := decodedNumberToInt(x); err == nil {
			return DataNumber(strconv.Itoa(i)), nil
		}
		f, err := x.Float64()
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("number %s has no finite value", x)
		}
		return DataNumber(strconv.FormatFloat(f, 'g', -1, 64)), nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			c, err := canonicalNumbers(e)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			c, err := canonicalNumbers(e)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	default:
		return raw, nil
	}
}
