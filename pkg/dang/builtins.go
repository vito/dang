package dang

import (
	"context"
	"fmt"
	"strings"

	"github.com/vito/dang/pkg/hm"
)

// Args provides type-safe access to function arguments
type Args struct {
	Values map[string]Value
}

// Get retrieves an argument value by name
func (a Args) Get(name string) (Value, bool) {
	val, ok := a.Values[name]
	return val, ok
}

// GetString retrieves a string argument
func (a Args) GetString(name string) string {
	val, ok := a.Values[name]
	if !ok {
		return ""
	}
	if strVal, ok := val.(StringValue); ok {
		return strVal.Val
	}
	return ""
}

// GetInt retrieves an integer argument
func (a Args) GetInt(name string) int {
	val, ok := a.Values[name]
	if !ok {
		return 0
	}
	if intVal, ok := val.(IntValue); ok {
		return intVal.Val
	}
	return 0
}

// GetBool retrieves a boolean argument
func (a Args) GetBool(name string) bool {
	val, ok := a.Values[name]
	if !ok {
		return false
	}
	if boolVal, ok := val.(BoolValue); ok {
		return boolVal.Val
	}
	return false
}

// GetList retrieves a list argument
func (a Args) GetList(name string) []Value {
	val, ok := a.Values[name]
	if !ok {
		return nil
	}
	if listVal, ok := val.(ListValue); ok {
		return listVal.Elements
	}
	return nil
}

// Require retrieves an argument or panics if not found
func (a Args) Require(name string) Value {
	val, ok := a.Values[name]
	if !ok {
		panic(fmt.Sprintf("required argument %q not found", name))
	}
	return val
}

// ToValue converts a Go value to a Dang Value
func ToValue(v any) (Value, error) {
	if v == nil {
		return NullValue{}, nil
	}

	switch val := v.(type) {
	case Value:
		// Already a Dang value
		return val, nil

	case string:
		return StringValue{Val: val}, nil

	case int:
		return IntValue{Val: val}, nil
	case int8:
		return IntValue{Val: int(val)}, nil
	case int16:
		return IntValue{Val: int(val)}, nil
	case int32:
		return IntValue{Val: int(val)}, nil
	case int64:
		return IntValue{Val: int(val)}, nil
	case uint:
		return IntValue{Val: int(val)}, nil
	case uint8:
		return IntValue{Val: int(val)}, nil
	case uint16:
		return IntValue{Val: int(val)}, nil
	case uint32:
		return IntValue{Val: int(val)}, nil
	case uint64:
		return IntValue{Val: int(val)}, nil

	case float32:
		return FloatValue{Val: float64(val)}, nil
	case float64:
		return FloatValue{Val: val}, nil

	case bool:
		return BoolValue{Val: val}, nil

	case []string:
		values := make([]Value, len(val))
		for i, s := range val {
			values[i] = StringValue{Val: s}
		}
		return ListValue{
			Elements: values,
			ElemType: hm.NonNullType{Type: StringType},
		}, nil

	case []int:
		values := make([]Value, len(val))
		for i, n := range val {
			values[i] = IntValue{Val: n}
		}
		return ListValue{
			Elements: values,
			ElemType: hm.NonNullType{Type: IntType},
		}, nil

	case []bool:
		values := make([]Value, len(val))
		for i, b := range val {
			values[i] = BoolValue{Val: b}
		}
		return ListValue{
			Elements: values,
			ElemType: hm.NonNullType{Type: BooleanType},
		}, nil

	case []float64:
		values := make([]Value, len(val))
		for i, f := range val {
			values[i] = FloatValue{Val: f}
		}
		return ListValue{
			Elements: values,
			ElemType: hm.NonNullType{Type: FloatType},
		}, nil

	case []any:
		if len(val) == 0 {
			// Empty slice - use a type variable
			return ListValue{
				Elements: []Value{},
				ElemType: hm.TypeVariable('a'),
			}, nil
		}

		// Convert all elements and infer common type
		values := make([]Value, len(val))
		var elemType hm.Type
		for i, item := range val {
			converted, err := ToValue(item)
			if err != nil {
				return nil, fmt.Errorf("converting list element %d: %w", i, err)
			}
			values[i] = converted

			if elemType == nil {
				elemType = converted.Type()
			} else {
				// Try to unify types - if they don't match, fall back to type variable
				if _, err := hm.Assignable(converted.Type(), elemType); err != nil {
					elemType = hm.TypeVariable('a')
				}
			}
		}

		return ListValue{
			Elements: values,
			ElemType: elemType,
		}, nil

	default:
		return nil, fmt.Errorf("cannot convert Go type %T to Dang Value", v)
	}
}

// BuiltinBuilder provides a fluent API for defining builtin functions
type BuiltinBuilder struct {
	def BuiltinDef
}

// Builtin creates a new builtin function builder
func Builtin(name string) *BuiltinBuilder {
	return &BuiltinBuilder{
		def: BuiltinDef{
			Name:       name,
			IsMethod:   false,
			ParamTypes: []ParamDef{},
		},
	}
}

// Doc sets the documentation string
func (b *BuiltinBuilder) Doc(doc string) *BuiltinBuilder {
	b.def.Doc = doc
	return b
}

// Params adds parameters to the function
// Usage: Params("name", type) or Params("name", type, defaultValue, "name2", type2, ...)
func (b *BuiltinBuilder) Params(pairs ...any) *BuiltinBuilder {
	for i := 0; i < len(pairs); {
		if i+1 >= len(pairs) {
			panic(fmt.Sprintf("Params: missing type for parameter at position %d", i))
		}

		name, ok := pairs[i].(string)
		if !ok {
			panic(fmt.Sprintf("Params: expected string at position %d, got %T", i, pairs[i]))
		}

		typ, ok := pairs[i+1].(hm.Type)
		if !ok {
			panic(fmt.Sprintf("Params: expected hm.Type at position %d, got %T", i+1, pairs[i+1]))
		}

		param := ParamDef{
			Name: name,
			Type: typ,
		}

		// Check if there's a default value
		if i+2 < len(pairs) {
			// Peek ahead - if next item is not a string, it's a default value
			if _, isString := pairs[i+2].(string); !isString {
				if val, isValue := pairs[i+2].(Value); isValue {
					param.DefaultValue = val
					i += 3
				} else {
					panic(fmt.Sprintf("Params: expected Value at position %d, got %T", i+2, pairs[i+2]))
				}
			} else {
				i += 2
			}
		} else {
			i += 2
		}

		b.def.ParamTypes = append(b.def.ParamTypes, param)
	}
	return b
}

// Returns sets the return type
func (b *BuiltinBuilder) Returns(typ hm.Type) *BuiltinBuilder {
	b.def.ReturnType = typ
	return b
}

// Impl sets the implementation and registers the builtin
func (b *BuiltinBuilder) Impl(fn func(context.Context, Args) (Value, error)) {
	// Wrap to match the internal signature (functions ignore self)
	b.def.Impl = func(ctx context.Context, self Value, args Args) (Value, error) {
		return fn(ctx, args)
	}
	Register(b.def)
}

// MethodBuilder provides a fluent API for defining methods
type MethodBuilder struct {
	def BuiltinDef
}

// Method creates a new method builder
func Method(receiverType *Module, name string) *MethodBuilder {
	return &MethodBuilder{
		def: BuiltinDef{
			Name:         name,
			IsMethod:     true,
			ReceiverType: receiverType,
			ParamTypes:   []ParamDef{},
		},
	}
}

// Doc sets the documentation string
func (b *MethodBuilder) Doc(doc string) *MethodBuilder {
	b.def.Doc = doc
	return b
}

// Params adds parameters to the method
func (b *MethodBuilder) Params(pairs ...any) *MethodBuilder {
	for i := 0; i < len(pairs); {
		if i+1 >= len(pairs) {
			panic(fmt.Sprintf("Params: missing type for parameter at position %d", i))
		}

		name, ok := pairs[i].(string)
		if !ok {
			panic(fmt.Sprintf("Params: expected string at position %d, got %T", i, pairs[i]))
		}

		typ, ok := pairs[i+1].(hm.Type)
		if !ok {
			panic(fmt.Sprintf("Params: expected hm.Type at position %d, got %T", i+1, pairs[i+1]))
		}

		param := ParamDef{
			Name: name,
			Type: typ,
		}

		// Check if there's a default value
		if i+2 < len(pairs) {
			// Peek ahead - if next item is not a string, it's a default value
			if _, isString := pairs[i+2].(string); !isString {
				if val, isValue := pairs[i+2].(Value); isValue {
					param.DefaultValue = val
					i += 3
				} else {
					panic(fmt.Sprintf("Params: expected Value at position %d, got %T", i+2, pairs[i+2]))
				}
			} else {
				i += 2
			}
		} else {
			i += 2
		}

		b.def.ParamTypes = append(b.def.ParamTypes, param)
	}
	return b
}

// Returns sets the return type
func (b *MethodBuilder) Returns(typ hm.Type) *MethodBuilder {
	b.def.ReturnType = typ
	return b
}

// Impl sets the implementation and registers the method
func (b *MethodBuilder) Impl(fn func(context.Context, Value, Args) (Value, error)) {
	b.def.Impl = fn
	Register(b.def)
}

var methodRegistry = make(map[*Module]map[string]BuiltinDef)

func init() {
	// This will be called after all init() functions that call Register()
	// We'll populate it lazily instead
}

// buildMethodRegistry builds the method lookup table
func buildMethodRegistry() {
	if len(methodRegistry) > 0 {
		return // already built
	}

	for _, def := range registry {
		if def.IsMethod {
			if methodRegistry[def.ReceiverType] == nil {
				methodRegistry[def.ReceiverType] = make(map[string]BuiltinDef)
			}
			methodRegistry[def.ReceiverType][def.Name] = def
		}
	}
}

// LookupMethod finds a method for a given receiver type
func LookupMethod(receiverType *Module, methodName string) (BuiltinDef, bool) {
	buildMethodRegistry()
	methods, ok := methodRegistry[receiverType]
	if !ok {
		return BuiltinDef{}, false
	}
	def, ok := methods[methodName]
	return def, ok
}

// GetMethodKey returns the environment key for a method
func GetMethodKey(receiverType *Module, methodName string) string {
	return fmt.Sprintf("_%s_%s_builtin",
		strings.ToLower(receiverType.Named), methodName)
}

// BuiltinDef defines a builtin function or method
type BuiltinDef struct {
	Name         string
	IsMethod     bool
	ReceiverType *Module // nil for functions
	ParamTypes   []ParamDef
	ReturnType   hm.Type
	Impl         func(ctx context.Context, self Value, args Args) (Value, error)
	Doc          string
}

// ParamDef defines a parameter with optional default value
type ParamDef struct {
	Name         string
	Type         hm.Type
	DefaultValue Value // nil if no default
}

var registry []BuiltinDef

// Register adds a builtin definition to the registry
func Register(def BuiltinDef) {
	registry = append(registry, def)
}

// ForEachFunction iterates over all registered functions
func ForEachFunction(fn func(BuiltinDef)) {
	for _, def := range registry {
		if !def.IsMethod {
			fn(def)
		}
	}
}

// ForEachMethod iterates over methods for a specific receiver type
func ForEachMethod(receiverType *Module, fn func(BuiltinDef)) {
	for _, def := range registry {
		if def.IsMethod && def.ReceiverType == receiverType {
			fn(def)
		}
	}
}

// TypeVar creates a type variable
func TypeVar(r rune) hm.Type {
	return hm.TypeVariable(r)
}

// NonNull wraps a type in NonNullType
func NonNull(t hm.Type) hm.Type {
	return hm.NonNullType{Type: t}
}

// ListOf creates a list type
func ListOf(t hm.Type) hm.Type {
	return ListType{Type: t}
}

// Optional returns a nullable type with a default value
func Optional(t hm.Type, defaultVal Value) (hm.Type, Value) {
	// If t is already non-null, unwrap it to make it nullable
	if nn, ok := t.(hm.NonNullType); ok {
		return nn.Type, defaultVal
	}
	return t, defaultVal
}
