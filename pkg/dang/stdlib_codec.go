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
			return DeferredValue{Raw: raw}, nil
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
			return DeferredValue{Raw: raw}, nil
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
			return DeferredValue{Raw: raw}, nil
		})
}

// --- JSON ---

func encodeJSON(val Value) (string, error) {
	// Dang values implement MarshalJSON, so json.Marshal produces canonical
	// output directly (correct int/float distinction, sorted object keys).
	jsonBytes, err := json.Marshal(val)
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
	plain, err := plainFromValue(val)
	if err != nil {
		return "", err
	}
	out, err := yaml.Marshal(plain)
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
	plain, err := plainFromValue(val)
	if err != nil {
		return "", err
	}
	// TOML's top level must be a table; a bare scalar or array has no TOML
	// representation. Report that in Dang terms rather than emitting something
	// surprising or leaking a Go type name.
	if _, ok := plain.(map[string]any); !ok {
		return "", fmt.Errorf("TOML requires a table (record) at the top level, got %s", plainKindName(plain))
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

// plainFromValue converts a Dang Value to a plain Go value (map / slice /
// string / bool / int64 / float64 / nil) suitable for yaml.Marshal and
// toml.Encode. It routes through the value's canonical JSON form so every
// Value type that implements MarshalJSON is handled uniformly, then converts
// json.Number back to int64/float64 (yaml/toml would otherwise emit numbers as
// quoted strings).
func plainFromValue(val Value) (any, error) {
	jsonBytes, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	raw, err := jsonBytesToRaw(jsonBytes)
	if err != nil {
		return nil, err
	}
	return numbersToGo(raw), nil
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
