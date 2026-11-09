package dang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Encoding turns a Dang value into a neutral data tree first, the mirror of
// the tree decoding produces, and each format renders that tree. The tree's
// nodes are nil, bool, string, DataNumber, []any, *dataObject (an object
// whose keys keep their order) and map[string]any (decoded data, whose keys
// have no order and render sorted).

// dataObject is an object in an encoded tree, its entries in order: a type's
// fields as the type declares them, a record's as written, a map's in
// insertion order.
type dataObject struct {
	entries []dataEntry
}

type dataEntry struct {
	key string
	val any
}

// encodeValue converts a Dang value into a data tree for a codec, applying
// that codec's field directives at every object boundary: @FORMAT.field
// renames the key and omits it when null or empty, @FORMAT.ignore drops it
// entirely. Renames are a codec concern only — Object.MarshalJSON, used for
// internal state retention, keeps canonical field names.
func encodeValue(val Value, c Codec) (any, error) {
	return valueEncoder{codec: c}.encode(val)
}

// valueEncoder is encodeValue with its options.
type valueEncoder struct {
	codec Codec
	// rawFields encodes every object field under its own name, ignoring the
	// codec's field directives, as toString does.
	rawFields bool
}

func (e valueEncoder) encode(val Value) (any, error) {
	switch v := val.(type) {
	case NullValue:
		return nil, nil
	case DeferredValue:
		// Decoded data is a neutral tree already: embed it as the data it
		// is, under its own keys, whichever format decoded it (field
		// directives belong to types, and this data has none). Its copy has
		// canonical numbers.
		return canonicalDecoded(v)
	case *Object:
		obj := &dataObject{}
		for _, kv := range objectBindings(v) {
			if _, isFn := kv.Value.(FunctionValue); isFn {
				continue
			}
			var opts codecFieldOpts
			if v.Mod != nil && !e.rawFields {
				opts = e.codec.options(v.Mod.GetDirectives(kv.Key))
			}
			if opts.ignore {
				continue
			}
			if opts.omitNull {
				if _, isNull := kv.Value.(NullValue); isNull || isNullDecoded(kv.Value) {
					continue
				}
			}
			if opts.omitEmpty && isEmptyValue(kv.Value) {
				continue
			}
			encoded, err := e.encode(kv.Value)
			if err != nil {
				return nil, err
			}
			obj.entries = append(obj.entries, dataEntry{key: opts.key(kv.Key), val: encoded})
		}
		return obj, nil
	case ListValue:
		// A non-nil slice, so an empty list encodes as [] rather than null.
		items := make([]any, len(v.Elements))
		for i, elem := range v.Elements {
			encoded, err := e.encode(elem)
			if err != nil {
				return nil, err
			}
			items[i] = encoded
		}
		return items, nil
	case MapValue:
		// Map keys are runtime data and are never renamed, but the values may
		// be objects with field directives, so recurse into them.
		obj := &dataObject{entries: make([]dataEntry, len(v.Keys))}
		for i, k := range v.Keys {
			encoded, err := e.encode(v.Entries[k])
			if err != nil {
				return nil, err
			}
			obj.entries[i] = dataEntry{key: k, val: encoded}
		}
		return obj, nil
	case StringValue:
		return v.Val, nil
	case EnumValue:
		return v.Val, nil
	case BoolValue:
		return v.Val, nil
	case IntValue:
		return DataNumber(strconv.Itoa(v.Val)), nil
	case FloatValue:
		return floatData(v.Val)
	case ScalarValue:
		if v.Encoded {
			// A schema scalar holding a non-string value keeps it as the
			// JSON its server sent; it encodes as that data.
			return decodeOrderedJSON([]byte(v.Val))
		}
		return v.Val, nil
	default:
		// A value with no data form of its own (a GraphQL object, by its ID)
		// encodes as what it marshals to.
		jsonBytes, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}
		return decodeOrderedJSON(jsonBytes)
	}
}

// floatData spells a float as a number the way every format reads one back:
// plainly, switching to an exponent for very large or small magnitudes.
func floatData(f float64) (any, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, fmt.Errorf("%v is not a finite number", f)
	}
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	s := strconv.FormatFloat(f, format, -1, 64)
	if format == 'e' {
		// 1e-07 reads better as 1e-7.
		if n := len(s); n >= 4 && s[n-4] == 'e' && s[n-3] == '-' && s[n-2] == '0' {
			s = s[:n-2] + s[n-1:]
		}
	}
	return DataNumber(s), nil
}

// objectBindings returns an object's bindings in the order its fields are
// encoded: the order its own type declares its members — a type's fields, a
// record literal's fields as written, a selection's as selected — with
// anything the type does not declare after them, by name. That is the type
// the value was built with: a record literal annotated with a record type
// listing its fields differently keeps the literal's order.
func objectBindings(obj *Object) []Keyed[Value] {
	bindings := obj.Bindings(PrivateVisibility)
	rank := map[string]int{}
	if obj.Mod != nil {
		for name := range obj.Mod.Bindings(PrivateVisibility) {
			if _, seen := rank[name]; !seen {
				rank[name] = len(rank)
			}
		}
	}
	sort.SliceStable(bindings, func(i, j int) bool {
		ri, iDeclared := rank[bindings[i].Key]
		rj, jDeclared := rank[bindings[j].Key]
		switch {
		case iDeclared && jDeclared:
			return ri < rj
		case iDeclared != jDeclared:
			return iDeclared
		default:
			return bindings[i].Key < bindings[j].Key
		}
	})
	return bindings
}

// dataEntries returns an object node's entries: a dataObject's in order, or
// sorted when sortKeys is set; decoded data's always sorted, having no order.
func dataEntries(node any, sortKeys bool) ([]dataEntry, bool) {
	switch x := node.(type) {
	case *dataObject:
		if !sortKeys {
			return x.entries, true
		}
		entries := append([]dataEntry(nil), x.entries...)
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		return entries, true
	case map[string]any:
		entries := make([]dataEntry, 0, len(x))
		for k, v := range x {
			entries = append(entries, dataEntry{key: k, val: v})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
		return entries, true
	default:
		return nil, false
	}
}

// decodeOrderedJSON reads JSON text into a data tree whose objects keep
// their keys' order, for values that hold JSON text of their own.
func decodeOrderedJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return readOrderedJSON(decoder)
}

func readOrderedJSON(decoder *json.Decoder) (any, error) {
	tok, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := &dataObject{}
			for decoder.More() {
				keyTok, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyTok.(string)
				val, err := readOrderedJSON(decoder)
				if err != nil {
					return nil, err
				}
				obj.entries = append(obj.entries, dataEntry{key: key, val: val})
			}
			_, err := decoder.Token()
			return obj, err
		case '[':
			items := []any{}
			for decoder.More() {
				item, err := readOrderedJSON(decoder)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
			_, err := decoder.Token()
			return items, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	case json.Number:
		return DataNumber(t), nil
	default:
		return tok, nil
	}
}

// --- JSON rendering ---

// renderJSON writes a data tree as compact JSON, escaping <, > and & in
// strings as json.Marshal does.
func renderJSON(buf *bytes.Buffer, node any, sortKeys bool) error {
	if entries, ok := dataEntries(node, sortKeys); ok {
		buf.WriteByte('{')
		for i, entry := range entries {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := renderJSON(buf, entry.key, sortKeys); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := renderJSON(buf, entry.val, sortKeys); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	}
	switch x := node.(type) {
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := renderJSON(buf, item, sortKeys); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case nil:
		buf.WriteString("null")
		return nil
	default:
		out, err := json.Marshal(x)
		if err != nil {
			return err
		}
		buf.Write(out)
		return nil
	}
}

// --- YAML rendering ---

// yamlNode builds the YAML node of a data tree, so mappings keep their
// entries' order.
func yamlNode(node any, sortKeys bool) (*yaml.Node, error) {
	if entries, ok := dataEntries(node, sortKeys); ok {
		mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, entry := range entries {
			key, err := yamlLeaf(entry.key)
			if err != nil {
				return nil, err
			}
			if entry.key == "<<" {
				// Written bare, "<<" is a merge key to any YAML reader,
				// YAML.decode included, so the output would not read back.
				key = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "<<", Style: yaml.DoubleQuotedStyle}
			}
			val, err := yamlNode(entry.val, sortKeys)
			if err != nil {
				return nil, err
			}
			mapping.Content = append(mapping.Content, key, val)
		}
		return mapping, nil
	}
	if items, ok := node.([]any); ok {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range items {
			n, err := yamlNode(item, sortKeys)
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, n)
		}
		return seq, nil
	}
	return yamlLeaf(plainNumber(node))
}

// yamlLeaf encodes a scalar as yaml.Marshal would, quoting a string that
// would otherwise read back as something else.
func yamlLeaf(v any) (*yaml.Node, error) {
	if s, ok := v.(string); ok {
		// Tagged !!str, the emitter quotes a plain string that would read
		// back as another type, and writes a multi-line one as a literal
		// block. yaml.Marshal also quotes YAML 1.1's booleans and base-60
		// numbers, for older readers, and writes "<<" bare.
		n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
		switch {
		case s == "<<":
			n.Tag = ""
		case yamlOldBool[s] || yamlBase60Float.MatchString(s):
			n.Style = yaml.DoubleQuotedStyle
		}
		return n, nil
	}
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}

var yamlOldBool = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true,
	"n": true, "N": true, "no": true, "No": true, "NO": true,
	"on": true, "On": true, "ON": true,
	"off": true, "Off": true, "OFF": true,
}

var yamlBase60Float = regexp.MustCompile(`^[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+(?:\.[0-9_]*)?$`)

// --- plain Go values, for encoders that take them ---

// plainNumber converts a DataNumber to an int64 or float64 so encoders that
// take Go values render it as a number.
func plainNumber(v any) any {
	x, ok := v.(DataNumber)
	if !ok {
		return v
	}
	if i, err := x.Int64(); err == nil {
		return i
	}
	if f, err := x.Float64(); err == nil {
		return f
	}
	return x.String()
}

// plainData converts a data tree to plain Go maps, slices and leaves, for
// encoders that order keys themselves.
func plainData(node any) any {
	if entries, ok := dataEntries(node, false); ok {
		out := make(map[string]any, len(entries))
		for _, entry := range entries {
			out[entry.key] = plainData(entry.val)
		}
		return out
	}
	if items, ok := node.([]any); ok {
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = plainData(item)
		}
		return out
	}
	return plainNumber(node)
}

// dataKindName names a data tree node in Dang terms, so error messages
// describe what the user passed rather than a Go type.
func dataKindName(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case string:
		return "a string"
	case DataNumber:
		if _, err := x.Int64(); err == nil {
			return "an integer"
		}
		return "a float"
	case []any:
		return "a list"
	case *dataObject, map[string]any:
		return "a record"
	default:
		return fmt.Sprintf("%T", v)
	}
}
