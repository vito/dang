package dang

import (
	"strconv"
	"strings"
)

// Repr renders a value as Dang source syntax: strings are quoted, and lists
// and maps recurse so their nested strings are quoted too. It's the form used
// to echo REPL results, where the output should read back as the literal that
// produced it (and highlight under the same grammar).
//
// This differs from String(), which renders strings bare (so print and string
// interpolation emit raw text). Types without a distinct literal form — modules,
// functions, enums, scalars — fall back to String().
func Repr(v Value) string {
	switch v := v.(type) {
	case StringValue:
		return strconv.Quote(v.Val)
	case ListValue:
		var b strings.Builder
		b.WriteString("[")
		for i, elem := range v.Elements {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(Repr(elem))
		}
		b.WriteString("]")
		return b.String()
	case MapValue:
		if len(v.Keys) == 0 {
			return "[:]"
		}
		var b strings.Builder
		b.WriteString("[")
		for i, k := range v.Keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(k))
			b.WriteString(": ")
			b.WriteString(Repr(v.Entries[k]))
		}
		b.WriteString("]")
		return b.String()
	default:
		return v.String()
	}
}
