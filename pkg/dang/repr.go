package dang

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Repr renders a value as Dang source syntax: strings are quoted, and lists,
// maps, records and decoded data recurse so their nested strings are quoted
// too. It's the form used to echo REPL results, where the output should read
// back as the literal that produced it (and highlight under the same
// grammar), and, through Display, how toString, List.join, interpolation and
// print render a value.
//
//   - a record or object is a record literal of its fields, `{{a: 1}}`, in
//     the order encoding uses (see objectBindings);
//   - decoded data is the literal of the data it holds: an object is a map
//     literal with sorted keys, `["a": 1]`;
//   - a custom scalar nested in a collection is the string literal it
//     refines from, `"https://…"`.
//
// Types without a distinct literal form — modules, functions, enums — fall
// back to String().
func Repr(v Value) string {
	var b strings.Builder
	writeRepr(&b, v)
	return b.String()
}

// Display renders a value as text, the way toString, List.join, string
// interpolation and print show it: a string, an enum value or a custom
// scalar is its own text, bare; anything else is its Repr.
func Display(v Value) string {
	switch v := v.(type) {
	case StringValue:
		return v.Val
	case EnumValue:
		return v.Val
	case ScalarValue:
		return v.Val
	default:
		return Repr(v)
	}
}

func writeRepr(b *strings.Builder, v Value) {
	switch v := v.(type) {
	case StringValue:
		b.WriteString(strconv.Quote(v.Val))
	case ScalarValue:
		if v.Encoded {
			// A schema scalar holding a non-string value is that data.
			if data, err := decodeOrderedJSON([]byte(v.Val)); err == nil {
				writeDataRepr(b, data)
				return
			}
		}
		b.WriteString(strconv.Quote(v.Val))
	case ListValue:
		b.WriteString("[")
		for i, elem := range v.Elements {
			if i > 0 {
				b.WriteString(", ")
			}
			writeRepr(b, elem)
		}
		b.WriteString("]")
	case MapValue:
		if len(v.Keys) == 0 {
			b.WriteString("[:]")
			return
		}
		b.WriteString("[")
		for i, k := range v.Keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(k))
			b.WriteString(": ")
			writeRepr(b, v.Entries[k])
		}
		b.WriteString("]")
	case *Object:
		if mod, ok := v.Mod.(*Type); ok && mod.IsMeta() {
			// A type's own name, not a value of it.
			b.WriteString(v.String())
			return
		}
		b.WriteString("{{")
		first := true
		for _, kv := range objectBindings(v) {
			if _, isFn := kv.Value.(FunctionValue); isFn {
				continue
			}
			if !first {
				b.WriteString(", ")
			}
			first = false
			b.WriteString(kv.Key)
			b.WriteString(": ")
			writeRepr(b, kv.Value)
		}
		b.WriteString("}}")
	case DeferredValue:
		raw, err := canonicalDecoded(v)
		if err != nil {
			// A number with no plain spelling (YAML's .inf) is shown as
			// written.
			raw = v.Raw
		}
		writeDataRepr(b, raw)
	default:
		b.WriteString(v.String())
	}
}

// writeDataRepr writes decoded data as the Dang literal of the same data.
func writeDataRepr(b *strings.Builder, raw any) {
	switch x := raw.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		b.WriteString(strconv.Quote(x))
	case DataNumber:
		b.WriteString(x.String())
	case []any:
		b.WriteString("[")
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writeDataRepr(b, e)
		}
		b.WriteString("]")
	case map[string]any:
		if len(x) == 0 {
			b.WriteString("[:]")
			return
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("[")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(k))
			b.WriteString(": ")
			writeDataRepr(b, x[k])
		}
		b.WriteString("]")
	case *dataObject:
		if len(x.entries) == 0 {
			b.WriteString("[:]")
			return
		}
		b.WriteString("[")
		for i, e := range x.entries {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strconv.Quote(e.key))
			b.WriteString(": ")
			writeDataRepr(b, e.val)
		}
		b.WriteString("]")
	default:
		fmt.Fprintf(b, "%v", x)
	}
}
