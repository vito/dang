package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		Doc("serializes a value to a JSON string").
		Example(`JSON.encode([1, 2, 3])`).
		Params("value", TypeVar('a')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			out, err := encodeJSON(val)
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
		Returns(TypeVar('a')).
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
			out, err := encodeJSON(val)
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
		Returns(TypeVar('a')).
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
		Returns(TypeVar('a')).
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
		Returns(TypeVar('a')).
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
		Returns(TypeVar('a')).
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

func encodeJSON(val Value) (string, error) {
	// encodeValue applies @JSON.field / @JSON.ignore at object boundaries;
	// json.Marshal then produces canonical output (json.Number leaves render as
	// numbers, object keys sort).
	tree, err := encodeValue(val, jsonCodec)
	if err != nil {
		return "", err
	}
	jsonBytes, err := json.Marshal(tree)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
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
	out, err := yaml.Marshal(numbersToGo(tree))
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
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
	switch v := val.(type) {
	case NullValue:
		return nil, nil
	case *Object:
		result := make(map[string]any, len(v.Values))
		for _, kv := range v.Bindings(PrivateVisibility) {
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
			encoded, err := encodeValue(kv.Value, c)
			if err != nil {
				return nil, err
			}
			result[opts.key(kv.Key)] = encoded
		}
		return result, nil
	case ListValue:
		// Use a non-nil slice so an empty list encodes as [] rather than null.
		items := make([]any, len(v.Elements))
		for i, elem := range v.Elements {
			encoded, err := encodeValue(elem, c)
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
			return c.encodeOrderedMap(v)
		}
		result := make(map[string]any, len(v.Keys))
		for _, k := range v.Keys {
			encoded, err := encodeValue(v.Entries[k], c)
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

// encodeOrderedMap renders a MapValue as a JSON object preserving key insertion
// order — a Go map would be re-sorted by encoding/json. It mirrors
// MapValue.MarshalJSON but encodes each value through encodeValue, so field
// directives reach objects nested as map values. The result is a json.RawMessage
// so the final json.Marshal emits it verbatim.
func (c Codec) encodeOrderedMap(m MapValue) (any, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range m.Keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyBytes, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')
		encoded, err := encodeValue(m.Entries[k], c)
		if err != nil {
			return nil, err
		}
		valBytes, err := json.Marshal(encoded)
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
