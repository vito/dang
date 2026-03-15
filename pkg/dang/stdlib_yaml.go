package dang

import (
	"context"
	"fmt"
	"strings"

	"github.com/vito/dang/pkg/hm"
	"gopkg.in/yaml.v3"
)

// YAMLModule is the type for parsed YAML values
var YAMLModule = NewModule("YAML", ObjectKind)

// YAMLValue wraps a parsed YAML document (typically a mapping)
type YAMLValue struct {
	Data any    // The parsed YAML data (map[string]any, []any, or scalar)
	Body string // The content after frontmatter (only set when frontmatter: true)
}

var _ Value = YAMLValue{}

func (y YAMLValue) Type() hm.Type {
	return hm.NonNullType{Type: YAMLModule}
}

func (y YAMLValue) String() string {
	out, err := yaml.Marshal(y.Data)
	if err != nil {
		return fmt.Sprintf("<YAML: %v>", y.Data)
	}
	return strings.TrimSpace(string(out))
}

func (y YAMLValue) MarshalJSON() ([]byte, error) {
	// YAML values serialize as their string representation
	out, err := yaml.Marshal(y.Data)
	if err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("%q", strings.TrimSpace(string(out)))), nil
}

// asMap returns the data as a string-keyed map, or nil if not a mapping
func (y YAMLValue) asMap() map[string]any {
	if m, ok := y.Data.(map[string]any); ok {
		return m
	}
	return nil
}

func registerYAML() {
	YAMLModule.SetModuleDocString("a parsed YAML value with key-based accessors")

	// parseYAML(data: String!, frontmatter: Boolean! = false) -> YAML!
	Builtin("parseYAML").
		Doc("parses a YAML string into a YAML value. When frontmatter is true, extracts YAML from between --- markers and makes the remaining content available via .body").
		Params("data", NonNull(StringType), "frontmatter", NonNull(BooleanType), BoolValue{Val: false}).
		Returns(NonNull(YAMLModule)).
		Impl(func(ctx context.Context, args Args) (Value, error) {
			data := args.GetString("data")
			frontmatter := args.GetBool("frontmatter")

			var toParse string
			var body string
			if frontmatter {
				fm := extractFrontmatter(data)
				if fm == nil {
					// No frontmatter found — return empty YAML with full string as body
					return YAMLValue{Data: map[string]any{}, Body: data}, nil
				}
				toParse = *fm
				body = extractFrontmatterBody(data)
			} else {
				toParse = data
			}

			var parsed any
			if err := yaml.Unmarshal([]byte(toParse), &parsed); err != nil {
				return nil, fmt.Errorf("parseYAML: %w", err)
			}
			if parsed == nil {
				parsed = map[string]any{}
			}
			return YAMLValue{Data: parsed, Body: body}, nil
		})

	// YAML.string(key: String!) -> String (nullable)
	Method(YAMLModule, "string").
		Doc("returns the string value for the given key, or null if the key is missing or not a string").
		Params("key", NonNull(StringType)).
		Returns(StringType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			key := args.GetString("key")
			m := y.asMap()
			if m == nil {
				return NullValue{}, nil
			}
			val, ok := m[key]
			if !ok || val == nil {
				return NullValue{}, nil
			}
			str, ok := val.(string)
			if !ok {
				// Coerce non-string scalars to string
				str = fmt.Sprintf("%v", val)
			}
			return StringValue{Val: str}, nil
		})

	// YAML.int(key: String!) -> Int (nullable)
	Method(YAMLModule, "int").
		Doc("returns the integer value for the given key, or null if the key is missing or not an integer").
		Params("key", NonNull(StringType)).
		Returns(IntType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			key := args.GetString("key")
			m := y.asMap()
			if m == nil {
				return NullValue{}, nil
			}
			val, ok := m[key]
			if !ok || val == nil {
				return NullValue{}, nil
			}
			switch v := val.(type) {
			case int:
				return IntValue{Val: v}, nil
			case int64:
				return IntValue{Val: int(v)}, nil
			case float64:
				return IntValue{Val: int(v)}, nil
			default:
				return NullValue{}, nil
			}
		})

	// YAML.bool(key: String!) -> Boolean (nullable)
	Method(YAMLModule, "bool").
		Doc("returns the boolean value for the given key, or null if the key is missing or not a boolean").
		Params("key", NonNull(StringType)).
		Returns(BooleanType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			key := args.GetString("key")
			m := y.asMap()
			if m == nil {
				return NullValue{}, nil
			}
			val, ok := m[key]
			if !ok || val == nil {
				return NullValue{}, nil
			}
			b, ok := val.(bool)
			if !ok {
				return NullValue{}, nil
			}
			return BoolValue{Val: b}, nil
		})

	// YAML.yaml(key: String!) -> YAML (nullable)
	Method(YAMLModule, "yaml").
		Doc("returns the nested YAML value for the given key, or null if the key is missing").
		Params("key", NonNull(StringType)).
		Returns(YAMLModule).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			key := args.GetString("key")
			m := y.asMap()
			if m == nil {
				return NullValue{}, nil
			}
			val, ok := m[key]
			if !ok || val == nil {
				return NullValue{}, nil
			}
			return YAMLValue{Data: val}, nil
		})

	// YAML.list(key: String!) -> [YAML!] (nullable)
	Method(YAMLModule, "list").
		Doc("returns a list of YAML values for the given key, or null if the key is missing or not a list").
		Params("key", NonNull(StringType)).
		Returns(ListOf(NonNull(YAMLModule))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			key := args.GetString("key")
			m := y.asMap()
			if m == nil {
				return NullValue{}, nil
			}
			val, ok := m[key]
			if !ok || val == nil {
				return NullValue{}, nil
			}
			items, ok := val.([]any)
			if !ok {
				return NullValue{}, nil
			}
			elements := make([]Value, len(items))
			for i, item := range items {
				elements[i] = YAMLValue{Data: item}
			}
			return ListValue{
				Elements: elements,
				ElemType: NonNull(YAMLModule),
			}, nil
		})

	// YAML.keys -> [String!]!
	Method(YAMLModule, "keys").
		Doc("returns all top-level keys in the YAML mapping").
		Returns(NonNull(ListOf(NonNull(StringType)))).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			m := y.asMap()
			if m == nil {
				return ListValue{
					Elements: []Value{},
					ElemType: NonNull(StringType),
				}, nil
			}
			elements := make([]Value, 0, len(m))
			for k := range m {
				elements = append(elements, StringValue{Val: k})
			}
			return ListValue{
				Elements: elements,
				ElemType: NonNull(StringType),
			}, nil
		})

	// YAML.has(key: String!) -> Boolean!
	Method(YAMLModule, "has").
		Doc("returns true if the YAML mapping contains the given key").
		Params("key", NonNull(StringType)).
		Returns(NonNull(BooleanType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			key := args.GetString("key")
			m := y.asMap()
			if m == nil {
				return BoolValue{Val: false}, nil
			}
			_, ok := m[key]
			return BoolValue{Val: ok}, nil
		})

	// YAML.asString -> String (nullable)
	// Returns the scalar value as a string, for YAML values that are not mappings.
	Method(YAMLModule, "asString").
		Doc("returns the value as a string if it is a scalar, or null for mappings and lists").
		Returns(StringType).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			switch v := y.Data.(type) {
			case string:
				return StringValue{Val: v}, nil
			case int:
				return StringValue{Val: fmt.Sprintf("%d", v)}, nil
			case int64:
				return StringValue{Val: fmt.Sprintf("%d", v)}, nil
			case float64:
				return StringValue{Val: fmt.Sprintf("%g", v)}, nil
			case bool:
				if v {
					return StringValue{Val: "true"}, nil
				}
				return StringValue{Val: "false"}, nil
			case nil:
				return NullValue{}, nil
			default:
				return NullValue{}, nil
			}
		})

	// YAML.body -> String!
	// Returns the content after the frontmatter (only meaningful when parsed with frontmatter: true)
	Method(YAMLModule, "body").
		Doc("returns the content after the frontmatter block. Only populated when parseYAML was called with frontmatter: true.").
		Returns(NonNull(StringType)).
		Impl(func(ctx context.Context, self Value, args Args) (Value, error) {
			y := self.(YAMLValue)
			return StringValue{Val: y.Body}, nil
		})
}

// extractFrontmatter extracts the YAML between --- markers.
// Returns nil if no valid frontmatter block is found.
func extractFrontmatter(s string) *string {
	// Must start with ---
	if !strings.HasPrefix(s, "---") {
		return nil
	}

	// Find the closing ---
	rest := s[3:]
	// Skip the newline after opening ---
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	} else if len(rest) == 0 {
		// Just "---" with nothing after
		return nil
	} else {
		// No newline after opening --- means it's not frontmatter
		return nil
	}

	// Find closing ---
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil
	}

	fm := rest[:idx]
	return &fm
}

// extractFrontmatterBody returns the content after the frontmatter block.
// If no frontmatter is found, returns the full string.
func extractFrontmatterBody(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}

	rest := s[3:]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	} else {
		return s
	}

	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return s
	}

	after := rest[idx+4:] // skip "\n---"
	// Skip optional newline after closing ---
	if len(after) > 0 && after[0] == '\n' {
		after = after[1:]
	} else if len(after) > 1 && after[0] == '\r' && after[1] == '\n' {
		after = after[2:]
	}

	return after
}
