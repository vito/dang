package dang

import (
	"fmt"
	"math"
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
//   - a record or object is a record literal of its public data fields,
//     `{{a: 1}}`, in the order encoding uses (see objectBindings), `{{}}`
//     when all of them are private; one with no data fields at all (a
//     namespace, a type of methods only) shows by name;
//   - decoded data is the literal of the data it holds: an object is a map
//     literal with sorted keys, `["a": 1]`;
//   - a float always reads back as a float (`1.0`), an enum value nested in
//     a collection is `Color.RED`, a custom scalar the string literal it
//     refines from, and a GraphQL object the lazy query it is, its type and
//     the chain that produces it with argument names but not values,
//     `<Container: container.from(address: …)>` (see renderHandle); nothing
//     is sent to show it, and the brackets keep it from reading as source.
//
// Types without a distinct literal form — modules, functions — fall back to
// String().
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
		if mod, ok := v.Mod.(*Type); ok && (mod.IsMeta() || mod.Kind != ObjectKind) {
			// A type's own name or an enum's namespace, not a value.
			b.WriteString(v.String())
			return
		}
		// Data fields only: methods and builtins are not data, and a private
		// (`let`) field is the type's own business.
		var fields []Keyed[Value]
		hasData := false
		for _, kv := range objectBindings(v) {
			if _, isCallable := kv.Value.(Callable); isCallable {
				continue
			}
			hasData = true
			if fieldVisibility(v, kv.Key) == PrivateVisibility {
				continue
			}
			fields = append(fields, kv)
		}
		if !hasData {
			if mod, ok := v.Mod.(*Type); ok && mod.Named != "" {
				// A namespace or a type of methods only shows by name.
				b.WriteString(v.String())
				return
			}
		}
		b.WriteString("{{")
		for i, kv := range fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(kv.Key)
			b.WriteString(": ")
			writeRepr(b, kv.Value)
		}
		b.WriteString("}}")
	case EnumValue:
		if t, ok := unwrapNonNull(v.EnumType).(*Type); ok && t.Named != "" {
			b.WriteString(t.Named)
			b.WriteString(".")
		}
		b.WriteString(v.Val)
	case FloatValue:
		b.WriteString(floatRepr(v.Val))
	case GraphQLValue:
		// A GraphQL object is the lazy query that produces it, read off the
		// handle: nothing is sent, so showing a mutation's result doesn't
		// run it again.
		b.WriteString(renderHandle(v))
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

// fieldVisibility is a field's visibility as the object's type declares it
// (a `let` field is private, any other public), or, for a field the type
// doesn't declare, as the object holds it.
func fieldVisibility(obj *Object, name string) Visibility {
	if mod, ok := obj.Mod.(*Type); ok {
		if _, declared := mod.LocalSchemeOf(name); declared {
			return mod.Visibility(name)
		}
	}
	return obj.Visibility(name)
}

// floatRepr spells a float as a Dang float literal: plainly, with an
// exponent only for very large or small magnitudes, and always with a
// fraction or exponent so it does not read back as an Int.
func floatRepr(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	abs := math.Abs(f)
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
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
