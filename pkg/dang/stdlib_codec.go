package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// JSON, YAML, and TOML are data-interchange formats, each a builtin scalar
// type whose values are valid text in that format (checked by a Go-native
// new() hook wherever a string becomes one), and whose name, used as a value,
// is the namespace holding its encode/decode, as a scalar's statics are. Both
// are provided to every module as a builtin import, so `JSON!` and
// `JSON.encode` resolve with no import statement (see builtin_imports.go).
// The scalar's type is also the scope of the format's field directives
// (`@JSON.field`). Decoding produces Decoded data, which no format owns. A
// schema's or user's type of the same name is a separate type. See issue #105.
var (
	JSONModule = NewType("JSON", ScalarKind)
	YAMLModule = NewType("YAML", ScalarKind)
	TOMLModule = NewType("TOML", ScalarKind)
)

// registerCodecs registers the JSON/YAML/TOML scalars and their statics, each
// directly with the builtin DSL like the Random/UUID modules (see
// builtin_imports.go for how they are installed).
func registerCodecs() {
	JSONModule.SetTypeDocString("JSON text; as a value, the functions for encoding and decoding JSON")
	JSONModule.SetGoScalarHook(func(raw string) (string, error) {
		if _, err := decodeJSON(raw, false); err != nil {
			return "", err
		}
		return raw, nil
	})
	// encode takes an arbitrary value, so it cannot live on the String type
	// (there is no universal receiver); it belongs to the format's namespace.
	StaticMethod(JSONModule, "encode").
		Doc("serializes a value to a JSON string. " +
			"Fields come out in declaration order (a type's as declared, a record's as written) " +
			"and map entries in insertion order; sortKeys: true sorts every object's keys instead. " +
			"indent pretty-prints with one copy of it per nesting level (e.g. \"  \"); " +
			"escapeHTML: false leaves <, >, and & unescaped in strings.").
		Example(`JSON.encode({{tag: "<b>", ids: [1, 2]}}, indent: "  ", escapeHTML: false)`).
		Params(
			"value", TypeVar('a'),
			"indent", NonNull(StringType), StringValue{Val: ""},
			"escapeHTML", NonNull(BooleanType), BoolValue{Val: true},
			"sortKeys", NonNull(BooleanType), BoolValue{Val: false},
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
		Doc("parses JSON text into an opaque value that is materialized by an expected type; with strict, an object key no field reads, or one repeated, is an error").
		Example(`JSON.decode("[1, 2, 3]") :: [Int!]!`).
		Params(
			"data", NonNull(StringType),
			"strict", BooleanType, BoolValue{Val: false},
		).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			strict := args.GetBool("strict")
			raw, err := decodeJSON(args.GetString("data"), strict)
			if err != nil {
				return nil, fmt.Errorf("JSON.decode: %w", err)
			}
			return DeferredValue{Raw: raw, Naming: jsonNaming, Strict: strict}, nil
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
			raw, err := decodeJSON(args.GetString("data"), false)
			if err != nil {
				return nil, fmt.Errorf("fromJSON: %w", err)
			}
			return DeferredValue{Raw: raw, Naming: jsonNaming}, nil
		})

	YAMLModule.SetTypeDocString("YAML text; as a value, the functions for encoding and decoding YAML")
	YAMLModule.SetGoScalarHook(func(raw string) (string, error) {
		if _, err := decodeYAMLRaw(raw, false); err != nil {
			return "", err
		}
		return raw, nil
	})
	StaticMethod(YAMLModule, "encode").
		Doc("serializes a value to a YAML string. " +
			"Fields come out in declaration order (a type's as declared, a record's as written) " +
			"and map entries in insertion order; sortKeys: true sorts every mapping's keys instead.").
		Example(`YAML.encode({{name: "ci", tags: ["a", "b"]}})`).
		Params(
			"value", TypeVar('a'),
			"sortKeys", NonNull(BooleanType), BoolValue{Val: false},
		).
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			val, _ := args.Get("value")
			out, err := encodeYAML(val, args.GetBool("sortKeys"))
			if err != nil {
				return nil, fmt.Errorf("YAML.encode: %w", err)
			}
			return ToValue(out)
		})
	StaticMethod(YAMLModule, "decode").
		Doc("parses a YAML string into an opaque value that is materialized by an expected type; with strict, a mapping key no field reads, or one repeated, is an error").
		Example(`YAML.decode("[1, 2, 3]") :: [Int!]!`).
		Params(
			"data", NonNull(StringType),
			"strict", BooleanType, BoolValue{Val: false},
		).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			strict := args.GetBool("strict")
			raw, err := decodeYAMLRaw(args.GetString("data"), strict)
			if err != nil {
				return nil, fmt.Errorf("YAML.decode: %w", err)
			}
			return DeferredValue{Raw: raw, Naming: yamlNaming, Strict: strict}, nil
		})
	Builtin("fromYAML").
		Doc("parses a YAML string into an opaque value that is materialized by an expected type. Deprecated: use YAML.decode instead.").
		Deprecated("use YAML.decode instead").
		Replacement("YAML.decode").
		Example(`fromYAML("[a, b, c]") :: [String!]!`).
		Params("data", NonNull(StringType)).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := decodeYAMLRaw(args.GetString("data"), false)
			if err != nil {
				return nil, fmt.Errorf("fromYAML: %w", err)
			}
			return DeferredValue{Raw: raw, Naming: yamlNaming}, nil
		})

	TOMLModule.SetTypeDocString("TOML text (a document); as a value, the functions for encoding and decoding TOML")
	TOMLModule.SetGoScalarHook(func(raw string) (string, error) {
		if _, err := decodeTOML(raw); err != nil {
			return "", err
		}
		return raw, nil
	})
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
		Doc("parses a TOML string into an opaque value that is materialized by an expected type; with strict, a key no field reads is an error (TOML never allows repeated keys)").
		Example("type Settings { count: Int! }\nTOML.decode(\"count = 3\") :: Settings").
		Params(
			"data", NonNull(StringType),
			"strict", BooleanType, BoolValue{Val: false},
		).
		Returns(DecodedType{}).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			raw, err := decodeTOML(args.GetString("data"))
			if err != nil {
				return nil, fmt.Errorf("TOML.decode: %w", err)
			}
			return DeferredValue{Raw: raw, Naming: tomlNaming, Strict: args.GetBool("strict")}, nil
		})

	registerFieldDirectives(jsonNaming)
	registerFieldDirectives(yamlNaming)
	registerFieldDirectives(tomlNaming)
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
	// sortKeys sorts the keys of every object — records, typed objects, maps,
	// and objects inside raw JSON leaves alike — as json.Marshal does for Go
	// maps. Without it, fields keep their declaration order, as json.Marshal
	// does for Go structs, and map entries their insertion order.
	sortKeys bool
}

var defaultJSONOptions = jsonOptions{escapeHTML: true}

// marshalOrderedJSON encodes val with e and renders the tree as compact JSON.
func marshalOrderedJSON(e valueEncoder, val Value, sortKeys bool) ([]byte, error) {
	tree, err := e.encode(val)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := renderJSON(&buf, tree, sortKeys); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeJSON(val Value, opts jsonOptions) (string, error) {
	if strings.Trim(opts.indent, " \t") != "" {
		return "", fmt.Errorf("indent must contain only spaces and tabs, got %q", opts.indent)
	}
	// The encoder applies @JSON.field / @JSON.ignore at object boundaries
	// and keeps objects in order; the renderer writes compact output.
	jsonBytes, err := marshalOrderedJSON(valueEncoder{naming: jsonNaming}, val, opts.sortKeys)
	if err != nil {
		return "", err
	}
	// Layout is applied to the compact output as a whole. Both passes leave
	// every other byte alone, so the defaults are exactly json.Marshal's
	// output.
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

func decodeJSON(data string, strict bool) (any, error) {
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

	if strict {
		// encoding/json keeps the last of a repeated key without a word, so
		// strict mode walks the tokens itself to find one.
		if err := checkJSONDuplicateKeys(data); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
	}

	return dataFromJSON(raw), nil
}

// checkJSONDuplicateKeys reports the first object in data that repeats a key,
// with its path and line, worded like checkYAMLDuplicateKeys. data must
// already be known to hold exactly one valid JSON value.
func checkJSONDuplicateKeys(data string) error {
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.UseNumber()
	return checkJSONValueDuplicateKeys(decoder, data, "")
}

func checkJSONValueDuplicateKeys(decoder *json.Decoder, data, path string) error {
	tok, err := decoder.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for decoder.More() {
			keyTok, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("unexpected object key %v", keyTok)
			}
			if seen[key] {
				// The offset is just past the key, so on its line.
				line := 1 + strings.Count(data[:decoder.InputOffset()], "\n")
				return materializeDeferredError(path, "line %d: field %s already set", line, key)
			}
			seen[key] = true
			if err := checkJSONValueDuplicateKeys(decoder, data, joinFieldPath(path, key)); err != nil {
				return err
			}
		}
		_, err := decoder.Token() // '}'
		return err
	case json.Delim('['):
		for i := 0; decoder.More(); i++ {
			if err := checkJSONValueDuplicateKeys(decoder, data, joinIndexPath(path, i)); err != nil {
				return err
			}
		}
		_, err := decoder.Token() // ']'
		return err
	default:
		return nil
	}
}

// --- YAML ---

func encodeYAML(val Value, sortKeys bool) (string, error) {
	tree, err := encodeValue(val, yamlNaming)
	if err != nil {
		return "", err
	}
	node, err := yamlNode(tree, sortKeys)
	if err != nil {
		return "", err
	}
	out, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// decodeYAMLRaw wraps decodeYAML (deferred_yaml.go) with the "invalid YAML:"
// prefix, matching decodeJSON/decodeTOML so every format reports decode errors
// the same way. strict is decodeYAML's.
func decodeYAMLRaw(data string, strict bool) (any, error) {
	raw, err := decodeYAML(data, strict)
	if err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return raw, nil
}

// --- TOML ---

func encodeTOML(val Value) (string, error) {
	tree, err := encodeValue(val, tomlNaming)
	if err != nil {
		return "", err
	}
	// TOML's top level must be a table; a bare scalar or array has no TOML
	// representation. Report that in Dang terms rather than emitting something
	// surprising or leaking a Go type name.
	if _, ok := dataEntries(tree, false); !ok {
		return "", fmt.Errorf("TOML requires a table (record) at the top level, got %s", dataKindName(tree))
	}
	// The TOML encoder takes Go maps, whose keys it sorts.
	plain := plainData(tree)
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
	if m == nil {
		m = map[string]any{}
	}
	raw, err := tomlToData(m)
	if err != nil {
		return nil, fmt.Errorf("invalid TOML: %w", err)
	}
	return raw, nil
}

// tomlToData converts what the TOML decoder produces into the neutral tree:
// integers and floats become DataNumber, and a date-time becomes its
// RFC 3339 text.
func tomlToData(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			c, err := tomlToData(e)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = c
		}
		return out, nil
	case []map[string]any:
		out := make([]any, len(x))
		for i, e := range x {
			c, err := tomlToData(e)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			c, err := tomlToData(e)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case int64:
		return DataNumber(strconv.FormatInt(x, 10)), nil
	case float64:
		// nan and inf keep their spelling, as YAML's .inf does: they fail
		// only where a finite number is needed.
		if math.IsInf(x, 0) || math.IsNaN(x) {
			return DataNumber(strconv.FormatFloat(x, 'g', -1, 64)), nil
		}
		return floatData(x)
	case time.Time:
		return x.Format(time.RFC3339Nano), nil
	default:
		return v, nil
	}
}

// --- shared value conversion ---

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
	case DeferredValue:
		// Decoded data is empty when the data it holds is.
		return isEmptyData(x.Raw)
	default:
		return false
	}
}

// isEmptyData reports whether decoded data counts as empty for omitEmpty,
// as isEmptyValue does for the values it decodes to.
func isEmptyData(raw any) bool {
	switch x := raw.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case DataNumber:
		f, err := x.Float64()
		return err == nil && f == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

// jsonBytesToRaw decodes JSON bytes into a generic value, preserving numbers as
// json.Number, the shape GraphQL clients send and receive.
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
// rest of the encoder which keeps errors in Dang terms (see dataKindName). A
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
