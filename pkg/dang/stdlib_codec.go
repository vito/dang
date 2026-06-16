package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/vito/dang/v2/pkg/hm"
	"gopkg.in/yaml.v3"
)

// Codec scalars (JSON, YAML, TOML) are string-shaped data-interchange formats
// that double as their own encode/decode namespace. Dang *owns* them: each is
// installed as a ScalarKind type in both the type namespace (so `:: JSON`
// resolves in type position) and the value namespace (so `JSON.encode` /
// `JSON.decode` resolve), see env.go init.
//
// When a schema imports — or a user declares — a scalar of the same name (e.g.
// Dagger's `scalar JSON`), it shadows the owned scalar and is grafted with the
// identical encode/decode via the format-codec merge below, so the two *merge*
// into one entity rather than colliding: `:: JSON` resolves the scalar type
// while `JSON.encode` resolves the grafted method. This is uniform across all
// three formats — JSON happens to have a Dagger scalar, YAML/TOML do not, but
// the owned-scalar default means they all behave the same. See issue #105.
var (
	JSONModule = NewType("JSON", ScalarKind)
	YAMLModule = NewType("YAML", ScalarKind)
	TOMLModule = NewType("TOML", ScalarKind)
)

// formatCodecs maps a scalar type name to the module whose static methods are
// grafted onto a same-named scalar in scope, so an imported or declared scalar
// of that name merges with the codec instead of shadowing it.
var formatCodecs = map[string]*Type{}

// codec bundles a format's marshal funcs with runnable doc examples. Adding a
// format is a single entry here plus its encode/decode funcs.
type codec struct {
	mod           *Type
	name          string
	encode        func(Value) (string, error)
	decode        func(string) (any, error)
	encodeExample string
	decodeExample string
}

func registerCodecs() {
	for _, c := range []codec{
		{
			mod: JSONModule, name: "JSON", encode: encodeJSON, decode: decodeJSON,
			encodeExample: `JSON.encode([1, 2, 3])`,
			decodeExample: `JSON.decode("[1, 2, 3]") :: [Int!]!`,
		},
		{
			mod: YAMLModule, name: "YAML", encode: encodeYAML, decode: decodeYAMLRaw,
			encodeExample: `YAML.encode([1, 2, 3])`,
			decodeExample: `YAML.decode("[1, 2, 3]") :: [Int!]!`,
		},
		{
			mod: TOMLModule, name: "TOML", encode: encodeTOML, decode: decodeTOML,
			encodeExample: `TOML.encode({{enabled: true, count: 3}})`,
			decodeExample: "type Settings { count: Int! }\nTOML.decode(\"count = 3\") :: Settings",
		},
	} {
		registerCodec(c)
	}
}

// registerCodec installs encode/decode static methods on a codec scalar and
// records it in formatCodecs.
func registerCodec(c codec) {
	c.mod.SetTypeDocString("functions for encoding and decoding " + c.name)

	// <Format>.encode(value: a) -> String!
	//
	// Encode takes an arbitrary value, so it cannot live on the String type
	// (there is no universal receiver); it belongs to the format's namespace.
	StaticMethod(c.mod, "encode").
		Doc("serializes a value to a "+c.name+" string").
		Example(c.encodeExample).
		Params("value", TypeVar('a')).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			out, err := c.encode(val)
			if err != nil {
				return nil, fmt.Errorf("%s.encode: %w", c.name, err)
			}
			return ToValue(out)
		})

	// <Format>.decode(data: String!) -> a
	//
	// Returns an opaque value that is materialized against the expected type
	// supplied via a `:: T` hint at the call boundary.
	StaticMethod(c.mod, "decode").
		Doc("parses a "+c.name+" string into an opaque value that is materialized by an expected type").
		Example(c.decodeExample).
		Params("data", NonNull(StringType)).
		Returns(TypeVar('a')).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := c.decode(args.GetString("data"))
			if err != nil {
				return nil, fmt.Errorf("%s.decode: %w", c.name, err)
			}
			return DeferredValue{Raw: raw}, nil
		})

	formatCodecs[c.name] = c.mod
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

// --- codec/scalar merge ---

// graftCodecSchemes adds a registered codec's static-method type schemes onto a
// scalar type of the same name, so `Name.encode` / `Name.decode` type-check
// when `Name` resolves to a user-declared or imported scalar. No-op for
// non-scalars and unregistered names; idempotent.
func graftCodecSchemes(scalarType *Type) {
	if scalarType == nil || scalarType.Kind != ScalarKind {
		return
	}
	host, ok := formatCodecs[scalarType.Named]
	if !ok {
		return
	}
	ForEachStaticMethod(host, func(def BuiltinDef) {
		if _, exists := scalarType.LocalSchemeOf(def.Name); exists {
			return
		}
		scalarType.Add(def.Name, hm.NewScheme(nil, createFunctionTypeFromDef(def)))
		scalarType.SetVisibility(def.Name, PublicVisibility)
		if def.Doc != "" {
			scalarType.SetDocString(def.Name, def.Doc)
		}
	})
}

// graftCodecMethods binds a registered codec's static-method implementations
// onto a scalar's runtime object, so `Name.encode` / `Name.decode` resolve at
// eval. No-op for non-scalars and unregistered names.
func graftCodecMethods(obj *Object, scalarType TypeScope) {
	mod, ok := scalarType.(*Type)
	if !ok || mod.Kind != ScalarKind {
		return
	}
	host, found := formatCodecs[mod.Named]
	if !found {
		return
	}
	ForEachStaticMethod(host, func(def BuiltinDef) {
		obj.Bind(def.Name, BuiltinFunction{
			Name:         def.Name,
			FnType:       createFunctionTypeFromDef(def),
			AllDefaulted: allParamsDefaulted(def),
			CallFn: func(ctx context.Context, scope ValueScope, args map[string]Value) (Value, error) {
				return def.Impl(ctx, nil, Args{Values: applyDefaults(args, def)})
			},
		}, PublicVisibility)
	})
}
