package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// JSON, YAML, and TOML are string-shaped data-interchange formats that double as
// their own encode/decode namespace. Each is a builtin scalar — a ScalarKind
// module Dang ships members for. As such they are installed in the Prelude (so
// `:: JSON` and `JSON.encode` resolve with no import), and a schema- or
// user-declared scalar of the same name (e.g. Dagger's `scalar JSON`) merges
// with them through the general scalar-attach mechanism in builtins.go
// (BuiltinScalarModule / attachBuiltinSchemes / attachBuiltinMethods). Nothing
// here is special-cased for encoding — encoding is just the first builtin scalar.
// See issue #105.
var (
	JSONModule = NewType("JSON", ScalarKind)
	YAMLModule = NewType("YAML", ScalarKind)
	TOMLModule = NewType("TOML", ScalarKind)
)

// registerCodecs registers the JSON/YAML/TOML codec namespaces, each directly
// with the builtin DSL like the Random/UUID modules. Being ScalarKind static
// modules, they are installed in the Prelude and merge with a same-named schema
// or user scalar automatically (see builtins.go: BuiltinScalarModule).
func registerCodecs() {
	JSONModule.SetTypeDocString("functions for encoding and decoding JSON")
	// encode takes an arbitrary value, so it cannot live on the String type
	// (there is no universal receiver); it belongs to the format's namespace.
	StaticMethod(JSONModule, "encode").
		Doc("serializes a value to a JSON string. " +
			"indent pretty-prints with one copy of it per nesting level (e.g. \"  \"); " +
			"escapeHTML: false leaves <, >, and & unescaped in strings; " +
			"sortKeys: false emits object and record fields in declaration order instead of alphabetically " +
			"(maps always keep insertion order).").
		Example(`JSON.encode({{tag: "<b>", ids: [1, 2]}}, indent: "  ", escapeHTML: false, sortKeys: false)`).
		Params(
			"value", TypeVar('a'),
			"indent", NonNull(StringType), StringValue{Val: ""},
			"escapeHTML", NonNull(BooleanType), BoolValue{Val: true},
			"sortKeys", NonNull(BooleanType), BoolValue{Val: true},
		).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			out, err := encodeJSON(val, jsonOptions{
				indent:     args.GetString("indent"),
				escapeHTML: args.GetBool("escapeHTML"),
				sortKeys:   args.GetBool("sortKeys"),
			})
			if err != nil {
				return nil, fmt.Errorf("JSON.encode: %w", err)
			}
			return ToValue(out)
		})
	// decode returns an opaque value materialized against the expected type
	// supplied via a `:: T` hint at the call boundary.
	StaticMethod(JSONModule, "decode").
		Doc("parses a JSON string into an opaque value that is materialized by an expected type").
		Example(`JSON.decode("[1, 2, 3]") :: [Int!]!`).
		Params("data", NonNull(StringType)).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := decodeJSON(args.GetString("data"))
			if err != nil {
				return nil, fmt.Errorf("JSON.decode: %w", err)
			}
			return DeferredValue{Raw: raw, Codec: jsonCodec}, nil
		})

	// toJSON / fromJSON / fromYAML are the deprecated top-level predecessors of
	// the JSON.encode / JSON.decode / YAML.decode namespace members, kept so
	// existing scripts keep working. They are marked @deprecated via the builtin
	// DSL; calling one prints a warning pointing at the call site (see
	// FunCall.Eval / WarnAtSource). Each delegates to the same implementation as
	// its namespaced successor, so they honor codec field directives identically.
	// (There was never a top-level toYAML/toTOML/fromTOML to restore.)
	Builtin("toJSON").
		Doc("serializes a value to a JSON string. Deprecated: use JSON.encode instead.").
		Deprecated("use JSON.encode instead").
		Replacement("JSON.encode").
		Example(`toJSON([1, 2, 3])`).
		Params("value", TypeVar('a')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			out, err := encodeJSON(val, defaultJSONOptions)
			if err != nil {
				return nil, fmt.Errorf("toJSON: %w", err)
			}
			return ToValue(out)
		})
	Builtin("fromJSON").
		Doc("parses a JSON string into an opaque value that is materialized by an expected type. Deprecated: use JSON.decode instead.").
		Deprecated("use JSON.decode instead").
		Replacement("JSON.decode").
		Example(`fromJSON("[1, 2, 3]") :: [Int!]!`).
		Params("data", NonNull(StringType)).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := decodeJSON(args.GetString("data"))
			if err != nil {
				return nil, fmt.Errorf("fromJSON: %w", err)
			}
			return DeferredValue{Raw: raw, Codec: jsonCodec}, nil
		})

	YAMLModule.SetTypeDocString("functions for encoding and decoding YAML")
	StaticMethod(YAMLModule, "encode").
		Doc("serializes a value to a YAML string").
		Example(`YAML.encode([1, 2, 3])`).
		Params("value", TypeVar('a')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			out, err := encodeYAML(val)
			if err != nil {
				return nil, fmt.Errorf("YAML.encode: %w", err)
			}
			return ToValue(out)
		})
	StaticMethod(YAMLModule, "decode").
		Doc("parses a YAML string into an opaque value that is materialized by an expected type").
		Example(`YAML.decode("[1, 2, 3]") :: [Int!]!`).
		Params("data", NonNull(StringType)).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := decodeYAMLRaw(args.GetString("data"))
			if err != nil {
				return nil, fmt.Errorf("YAML.decode: %w", err)
			}
			return DeferredValue{Raw: raw, Codec: yamlCodec}, nil
		})
	Builtin("fromYAML").
		Doc("parses a YAML string into an opaque value that is materialized by an expected type. Deprecated: use YAML.decode instead.").
		Deprecated("use YAML.decode instead").
		Replacement("YAML.decode").
		Example(`fromYAML("[a, b, c]") :: [String!]!`).
		Params("data", NonNull(StringType)).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := decodeYAMLRaw(args.GetString("data"))
			if err != nil {
				return nil, fmt.Errorf("fromYAML: %w", err)
			}
			return DeferredValue{Raw: raw, Codec: yamlCodec}, nil
		})

	TOMLModule.SetTypeDocString("functions for encoding and decoding TOML")
	StaticMethod(TOMLModule, "encode").
		Doc("serializes a value to a TOML string").
		Example(`TOML.encode({{enabled: true, count: 3}})`).
		Params("value", TypeVar('a')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			out, err := encodeTOML(val)
			if err != nil {
				return nil, fmt.Errorf("TOML.encode: %w", err)
			}
			return ToValue(out)
		})
	StaticMethod(TOMLModule, "decode").
		Doc("parses a TOML string into an opaque value that is materialized by an expected type").
		Example("type Settings { count: Int! }\nTOML.decode(\"count = 3\") :: Settings").
		Params("data", NonNull(StringType)).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := decodeTOML(args.GetString("data"))
			if err != nil {
				return nil, fmt.Errorf("TOML.decode: %w", err)
			}
			return DeferredValue{Raw: raw, Codec: tomlCodec}, nil
		})

	registerCodecFieldDirectives()
}

// --- JSON ---

// jsonOptions controls JSON.encode's output layout; the zero value is not the
// default (escapeHTML defaults to true), so use defaultJSONOptions.
type jsonOptions struct {
	// indent, when non-empty, pretty-prints the output with one copy of it per
	// nesting level, like json.MarshalIndent(v, "", indent).
	indent string
	// escapeHTML escapes <, >, and & inside strings as \u003c, \u003e, and
	// \u0026, as json.Marshal does; false writes them verbatim, like a
	// json.Encoder with SetEscapeHTML(false).
	escapeHTML bool
	// sortKeys emits object and record fields sorted by key, as json.Marshal
	// does for Go maps; false keeps their declaration order, as it does for
	// Go structs. Maps keep their insertion order either way.
	sortKeys bool
}

var defaultJSONOptions = jsonOptions{escapeHTML: true, sortKeys: true}

func encodeJSON(val Value, opts jsonOptions) (string, error) {
	if strings.Trim(opts.indent, " \t") != "" {
		return "", fmt.Errorf("indent must contain only spaces and tabs, got %q", opts.indent)
	}
	// encodeValue applies @JSON.field / @JSON.ignore at object boundaries;
	// json.Marshal then produces canonical output (json.Number leaves render as
	// numbers, object keys sort unless they were assembled in order).
	tree, err := valueEncoder{codec: jsonCodec, declaredOrder: !opts.sortKeys}.encode(val)
	if err != nil {
		return "", err
	}
	jsonBytes, err := json.Marshal(tree)
	if err != nil {
		return "", err
	}
	// Layout is applied to the compact output as a whole rather than through
	// json.MarshalIndent or a json.Encoder, because parts of the tree are
	// already-encoded json.RawMessage (ordered maps, leaves) that were
	// HTML-escaped when they were marshaled. Both passes leave every other
	// byte alone, so the defaults are exactly json.Marshal's output.
	if !opts.escapeHTML {
		jsonBytes = unescapeJSONHTML(jsonBytes)
	}
	if opts.indent != "" {
		var buf bytes.Buffer
		if err := json.Indent(&buf, jsonBytes, "", opts.indent); err != nil {
			return "", err
		}
		jsonBytes = buf.Bytes()
	}
	return string(jsonBytes), nil
}

// unescapeJSONHTML rewrites the \u003c, \u003e, and \u0026 escapes that
// json.Marshal writes for <, >, and & back into the bare characters, which is
// what a json.Encoder with SetEscapeHTML(false) writes. src must be valid JSON:
// escapes only occur inside strings there, and walking them pairwise keeps an
// escaped backslash followed by "u003c" (the text \u003c) intact.
func unescapeJSONHTML(src []byte) []byte {
	if !bytes.Contains(src, []byte(`\u00`)) {
		return src
	}
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		if src[i] != '\\' || i+1 >= len(src) {
			out = append(out, src[i])
			continue
		}
		if src[i+1] == 'u' && i+6 <= len(src) {
			switch string(src[i+2 : i+6]) {
			case "003c", "003C":
				out = append(out, '<')
				i += 5
				continue
			case "003e", "003E":
				out = append(out, '>')
				i += 5
				continue
			case "0026":
				out = append(out, '&')
				i += 5
				continue
			}
		}
		// Any other escape: copy both bytes so the second is never mistaken
		// for the start of an escape.
		out = append(out, src[i], src[i+1])
		i++
	}
	return out
}

func decodeJSON(data string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()

	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		return nil, fmt.Errorf("invalid JSON: trailing data")
	}

	return raw, nil
}

// --- YAML ---

func encodeYAML(val Value) (string, error) {
	tree, err := encodeValue(val, yamlCodec)
	if err != nil {
		return "", err
	}
	out, err := yaml.Marshal(quoteYAMLMergeKeys(numbersToGo(tree)))
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// yamlQuotedKey is a map key yaml.v3 must emit double-quoted. yaml.v3 writes a
// "<<" string key bare, which any YAML reader — YAML.decode included — takes
// as a merge key, so YAML.encode's output would not round-trip.
type yamlQuotedKey string

func (k yamlQuotedKey) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(k), Style: yaml.DoubleQuotedStyle}, nil
}

// quoteYAMLMergeKeys rewrites every map holding a "<<" key so that key
// marshals quoted (see yamlQuotedKey). Other maps, and "<<" values, which
// cannot merge, are left exactly as yaml.Marshal renders them.
func quoteYAMLMergeKeys(v any) any {
	switch x := v.(type) {
	case []any:
		for i := range x {
			x[i] = quoteYAMLMergeKeys(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = quoteYAMLMergeKeys(x[k])
		}
		if _, ok := x["<<"]; !ok {
			return x
		}
		quoted := make(map[any]any, len(x))
		for k, val := range x {
			if k == "<<" {
				quoted[yamlQuotedKey(k)] = val
			} else {
				quoted[k] = val
			}
		}
		return quoted
	default:
		return v
	}
}

// decodeYAMLRaw wraps decodeYAML (deferred_yaml.go) with the "invalid YAML:"
// prefix, matching decodeJSON/decodeTOML so every format reports decode errors
// the same way.
func decodeYAMLRaw(data string) (any, error) {
	raw, err := decodeYAML(data)
	if err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return raw, nil
}

// --- TOML ---

func encodeTOML(val Value) (string, error) {
	tree, err := encodeValue(val, tomlCodec)
	if err != nil {
		return "", err
	}
	plain := numbersToGo(tree)
	// TOML's top level must be a table; a bare scalar or array has no TOML
	// representation. Report that in Dang terms rather than emitting something
	// surprising or leaking a Go type name.
	if _, ok := plain.(map[string]any); !ok {
		return "", fmt.Errorf("TOML requires a table (record) at the top level, got %s", plainKindName(plain))
	}
	if err := tomlRejectNullInList(plain); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(plain); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func decodeTOML(data string) (any, error) {
	var m map[string]any
	if _, err := toml.Decode(data, &m); err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	// Bridge through JSON so the decoded shape matches what the materializer
	// already handles for JSON/YAML decode — in particular, numbers arrive as
	// json.Number rather than TOML's native int64/float64.
	jsonBytes, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return jsonBytesToRaw(jsonBytes)
}

// --- shared value conversion ---

// encodeValue converts a Dang Value into a plain Go value (map / slice / string
// / bool / json.Number / nil) for a codec, applying that codec's field
// directives at every object boundary: @FORMAT.field renames the key and omits
// it when null, @FORMAT.ignore drops it entirely. Numbers stay as json.Number so
// each codec can render them (json.Marshal directly; yaml/toml after
// numbersToGo). Renames are a codec concern only — Object.MarshalJSON, used for
// internal state retention, keeps canonical field names.
func encodeValue(val Value, c Codec) (any, error) {
	return valueEncoder{codec: c}.encode(val)
}

// valueEncoder is encodeValue with its options.
type valueEncoder struct {
	codec Codec
	// declaredOrder emits object and record fields in declaration order
	// rather than sorted by key, like encoding/json does for struct fields.
	// Only JSON honors it: its objects are assembled as ordered
	// json.RawMessage, while YAML/TOML render Go maps, which they sort.
	declaredOrder bool
}

func (e valueEncoder) encode(val Value) (any, error) {
	c := e.codec
	switch v := val.(type) {
	case NullValue:
		return nil, nil
	case *Object:
		var entries []jsonEntry
		for _, kv := range e.objectBindings(v) {
			if _, isFn := kv.Value.(FunctionValue); isFn {
				continue
			}
			var directives []*DirectiveApplication
			if v.Mod != nil {
				directives = v.Mod.GetDirectives(kv.Key)
			}
			opts := c.options(directives)
			if opts.ignore {
				continue
			}
			if opts.omitNull {
				if _, isNull := kv.Value.(NullValue); isNull {
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
			entries = append(entries, jsonEntry{key: opts.key(kv.Key), val: encoded})
		}
		if e.declaredOrder && c == jsonCodec {
			return marshalJSONObject(entries)
		}
		result := make(map[string]any, len(entries))
		for _, entry := range entries {
			result[entry.key] = entry.val
		}
		return result, nil
	case ListValue:
		// Use a non-nil slice so an empty list encodes as [] rather than null.
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
		// Map keys are runtime data and are never renamed, but the values may be
		// objects with field directives, so recurse into them. JSON preserves key
		// insertion order, which a Go map would lose (encoding/json sorts keys),
		// so assemble it directly; YAML/TOML sort keys regardless, so a plain map
		// suffices.
		if c == jsonCodec {
			entries := make([]jsonEntry, len(v.Keys))
			for i, k := range v.Keys {
				encoded, err := e.encode(v.Entries[k])
				if err != nil {
					return nil, err
				}
				entries[i] = jsonEntry{key: k, val: encoded}
			}
			return marshalJSONObject(entries)
		}
		result := make(map[string]any, len(v.Keys))
		for _, k := range v.Keys {
			encoded, err := e.encode(v.Entries[k])
			if err != nil {
				return nil, err
			}
			result[k] = encoded
		}
		return result, nil
	default:
		// Scalars, enums, and other leaves have no renamable fields of their own,
		// so they keep their canonical encoding.
		return c.encodeLeaf(val)
	}
}

// objectBindings returns an object's bindings in the order its fields are
// encoded. By default that order does not matter (they are collected into a
// map), so they come in whatever order the object holds them. With
// declaredOrder they follow the order the object's type declares its members
// — a type's fields, a record literal's fields as written — with anything the
// type does not declare after them, by name.
func (e valueEncoder) objectBindings(obj *Object) []Keyed[Value] {
	bindings := obj.Bindings(PrivateVisibility)
	if !e.declaredOrder || obj.Mod == nil {
		return bindings
	}
	rank := map[string]int{}
	for name := range obj.Mod.Bindings(PrivateVisibility) {
		if _, seen := rank[name]; !seen {
			rank[name] = len(rank)
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

// jsonEntry is one member of a JSON object assembled in a chosen order.
type jsonEntry struct {
	key string
	val any
}

// marshalJSONObject renders entries as a JSON object in the order given — a Go
// map would be re-sorted by encoding/json. The result is a json.RawMessage so
// the final json.Marshal emits it verbatim.
func marshalJSONObject(entries []jsonEntry) (any, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, entry := range entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(entry.key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		valBytes, err := json.Marshal(entry.val)
		if err != nil {
			return nil, err
		}
		buf.Write(valBytes)
	}
	buf.WriteByte('}')
	return json.RawMessage(buf.Bytes()), nil
}

// isEmptyValue reports whether a value counts as empty for @FORMAT.field's
// omitEmpty, matching Go's encoding/json omitempty: null, the empty string, a
// zero number, false, and an empty list or map. Objects and other values are
// never considered empty.
func isEmptyValue(v Value) bool {
	switch x := v.(type) {
	case NullValue:
		return true
	case StringValue:
		return x.Val == ""
	case IntValue:
		return x.Val == 0
	case FloatValue:
		return x.Val == 0
	case BoolValue:
		return !x.Val
	case ListValue:
		return len(x.Elements) == 0
	case MapValue:
		return len(x.Keys) == 0
	default:
		return false
	}
}

// encodeLeaf converts a leaf Dang Value to its canonical form for this codec.
// JSON preserves the value's own MarshalJSON output verbatim (as
// json.RawMessage), so map keys keep their insertion order through the final
// marshal. YAML/TOML produce a plain Go value (numbers as json.Number) that
// those encoders can render — they sort map keys regardless.
func (c Codec) encodeLeaf(val Value) (any, error) {
	jsonBytes, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	if c == jsonCodec {
		return json.RawMessage(jsonBytes), nil
	}
	return jsonBytesToRaw(jsonBytes)
}

// jsonBytesToRaw decodes JSON bytes into a generic value, preserving numbers as
// json.Number — the shape the materializer expects from a decode.
func jsonBytesToRaw(jsonBytes []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(jsonBytes))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// tomlRejectNullInList reports a Dang-framed error for a null list element,
// which TOML cannot represent. Without this, BurntSushi surfaces its own
// internal message ("toml: cannot encode array with nil element"), unlike the
// rest of the encoder which keeps errors in Dang terms (see plainKindName). A
// null *table* value the encoder drops silently, which we leave as-is.
func tomlRejectNullInList(v any) error {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if e == nil {
				return fmt.Errorf("TOML cannot represent null inside a list")
			}
			if err := tomlRejectNullInList(e); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, e := range x {
			if err := tomlRejectNullInList(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// plainKindName names a plain Go value (as produced by plainFromValue) in
// Dang-facing terms, so error messages describe what the user passed rather
// than leaking a Go type name like []interface {} or int64.
func plainKindName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case string:
		return "a string"
	case int64:
		return "an integer"
	case float64:
		return "a float"
	case []any:
		return "a list"
	case map[string]any:
		return "a record"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// numbersToGo walks a decoded structure and converts json.Number to int64 or
// float64 so non-JSON encoders render numbers as numbers.
func numbersToGo(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case []any:
		for i := range x {
			x[i] = numbersToGo(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = numbersToGo(x[k])
		}
		return x
	default:
		return v
	}
}
