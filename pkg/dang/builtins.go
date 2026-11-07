package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/vito/dang/v2/pkg/hm"
)

// Args provides type-safe access to function arguments
type Args struct {
	Values map[string]Value
	Block  *FunctionValue // Special field for block arguments
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

// GetEnum retrieves an enum argument's string value
func (a Args) GetEnum(name string) string {
	val, ok := a.Values[name]
	if !ok {
		return ""
	}
	if enumVal, ok := val.(EnumValue); ok {
		return enumVal.Val
	}
	return ""
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

	case DataNumber:
		return ToValue(json.Number(val))
	case json.Number:
		// Untyped fallback (no declared type to honor): keep the JSON author's
		// intent — a bare integer literal becomes an Int, anything fractional a
		// Float. Typed GraphQL/codec leaves never reach here; they convert
		// against their declared type.
		if i, err := val.Int64(); err == nil && int64(int(i)) == i {
			return IntValue{Val: int(i)}, nil
		}
		f, err := val.Float64()
		if err != nil {
			return nil, fmt.Errorf("cannot convert number %q to a Dang Value", val.String())
		}
		return FloatValue{Val: f}, nil

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

func parseParamDefs(pairs ...any) []ParamDef {
	var params []ParamDef
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

		// Check if there's a default value. If the next item is not a
		// string, it is the default for this parameter.
		if i+2 < len(pairs) {
			if _, isString := pairs[i+2].(string); !isString {
				val, isValue := pairs[i+2].(Value)
				if !isValue {
					panic(fmt.Sprintf("Params: expected Value at position %d, got %T", i+2, pairs[i+2]))
				}
				param.DefaultValue = val
				i += 3
			} else {
				i += 2
			}
		} else {
			i += 2
		}

		params = append(params, param)
	}
	return params
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

// Example sets a runnable snippet of Dang demonstrating the function.
func (b *BuiltinBuilder) Example(code string) *BuiltinBuilder {
	b.def.Example = code
	return b
}

// Params adds parameters to the function.
//
// Usage: Params("name", type) or
// Params("name", type, defaultValue, "name2", type2, ...)
func (b *BuiltinBuilder) Params(pairs ...any) *BuiltinBuilder {
	b.def.ParamTypes = append(b.def.ParamTypes, parseParamDefs(pairs...)...)
	return b
}

// Block sets the expected block argument type
func (b *BuiltinBuilder) Block(fnType *hm.FunctionType) *BuiltinBuilder {
	b.def.BlockType = fnType
	return b
}

// Returns sets the return type
func (b *BuiltinBuilder) Returns(typ hm.Type) *BuiltinBuilder {
	b.def.ReturnType = typ
	return b
}

// Deprecated marks the function as deprecated. The reason is shown (alongside
// the call site) when the function is called.
func (b *BuiltinBuilder) Deprecated(reason string) *BuiltinBuilder {
	b.def.Deprecated = reason
	return b
}

// Replacement names the callee that supersedes this one (e.g. "JSON.encode"),
// letting tools rewrite a call mechanically. Only meaningful alongside
// Deprecated.
func (b *BuiltinBuilder) Replacement(name string) *BuiltinBuilder {
	b.def.Replacement = name
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
func Method(receiverType *Type, name string) *MethodBuilder {
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

// Example sets a runnable snippet of Dang demonstrating the method.
func (b *MethodBuilder) Example(code string) *MethodBuilder {
	b.def.Example = code
	return b
}

// Params adds parameters to the method.
func (b *MethodBuilder) Params(pairs ...any) *MethodBuilder {
	b.def.ParamTypes = append(b.def.ParamTypes, parseParamDefs(pairs...)...)
	return b
}

// Block sets the expected block argument type
func (b *MethodBuilder) Block(fnType *hm.FunctionType) *MethodBuilder {
	b.def.BlockType = fnType
	return b
}

// Returns sets the return type
func (b *MethodBuilder) Returns(typ hm.Type) *MethodBuilder {
	b.def.ReturnType = typ
	return b
}

// Deprecated marks the method as deprecated. The reason is shown (alongside
// the call site) when the method is called.
func (b *MethodBuilder) Deprecated(reason string) *MethodBuilder {
	b.def.Deprecated = reason
	return b
}

// Replacement names the member that supersedes this method, letting tools
// rewrite a call mechanically. Only meaningful alongside Deprecated.
func (b *MethodBuilder) Replacement(name string) *MethodBuilder {
	b.def.Replacement = name
	return b
}

// Check sets a type-check-time validation of each call's resolved
// signature. See BuiltinDef.Check.
func (b *MethodBuilder) Check(fn func(*hm.FunctionType) error) *MethodBuilder {
	b.def.Check = fn
	return b
}

// Impl sets the implementation and registers the method
func (b *MethodBuilder) Impl(fn func(context.Context, Value, Args) (Value, error)) {
	b.def.Impl = fn
	Register(b.def)
}

// StaticMethodBuilder provides a fluent API for defining static methods on modules
type StaticMethodBuilder struct {
	def BuiltinDef
}

// StaticMethod creates a new static method builder for a module
func StaticMethod(hostModule *Type, name string) *StaticMethodBuilder {
	return &StaticMethodBuilder{
		def: BuiltinDef{
			Name:       name,
			IsStatic:   true,
			HostModule: hostModule,
			ParamTypes: []ParamDef{},
		},
	}
}

// Doc sets the documentation string
func (b *StaticMethodBuilder) Doc(doc string) *StaticMethodBuilder {
	b.def.Doc = doc
	return b
}

// Example sets a runnable snippet of Dang demonstrating the static method.
func (b *StaticMethodBuilder) Example(code string) *StaticMethodBuilder {
	b.def.Example = code
	return b
}

// Params adds parameters to the static method.
func (b *StaticMethodBuilder) Params(pairs ...any) *StaticMethodBuilder {
	b.def.ParamTypes = append(b.def.ParamTypes, parseParamDefs(pairs...)...)
	return b
}

// Returns sets the return type
func (b *StaticMethodBuilder) Returns(typ hm.Type) *StaticMethodBuilder {
	b.def.ReturnType = typ
	return b
}

// Deprecated marks the static method as deprecated. The reason is shown
// (alongside the call site) when the method is called.
func (b *StaticMethodBuilder) Deprecated(reason string) *StaticMethodBuilder {
	b.def.Deprecated = reason
	return b
}

// Replacement names the member that supersedes this static method, letting
// tools rewrite a call mechanically. Only meaningful alongside Deprecated.
func (b *StaticMethodBuilder) Replacement(name string) *StaticMethodBuilder {
	b.def.Replacement = name
	return b
}

// Impl sets the implementation and registers the static method
func (b *StaticMethodBuilder) Impl(fn func(context.Context, Args) (Value, error)) {
	b.def.Impl = func(ctx context.Context, self Value, args Args) (Value, error) {
		return fn(ctx, args)
	}
	Register(b.def)
}

// BuiltinDef defines a builtin function or method.
type BuiltinDef struct {
	Name         string
	IsMethod     bool
	IsStatic     bool  // true for static methods on a module (e.g. Random.int)
	ReceiverType *Type // nil for functions
	HostModule   *Type // the module this static method belongs to
	ParamTypes   []ParamDef
	BlockType    *hm.FunctionType // nil if no block arg expected
	ReturnType   hm.Type
	Impl         func(ctx context.Context, self Value, args Args) (Value, error)
	Doc          string
	// Example is a tiny, self-contained snippet of Dang that exercises the
	// builtin. The docs reference renders it as a pre-seeded, runnable REPL,
	// and a test evaluates every example to keep them honest.
	Example string
	// Deprecated, when non-empty, marks this builtin as the Go-side equivalent
	// of a @deprecated directive. The string is the human-facing reason
	// (typically naming the replacement). Calling a deprecated builtin prints a
	// warning to stderr pointing at the call site; see FunCall.Eval.
	Deprecated string
	// Replacement, when non-empty, names the callee that supersedes this one
	// (e.g. "JSON.encode"). Unlike the prose in Deprecated, it is structured so
	// tools can rewrite a call mechanically — the LSP offers it as a quick fix.
	Replacement string
	// Check, when set, validates a call's signature once its type variables
	// are resolved by the receiver, the arguments and the block, rejecting
	// at type-check time what a signature can't express (sortBy's key must
	// be orderable). Only list methods run it so far: on selection, with
	// what the receiver resolves, and again in a call that names the method
	// directly (`xs.sortBy { ... }`). A method taken as a value and called
	// later (`let f = &xs.sortBy`) is checked on selection only, so what its
	// block resolves is left to the implementation to reject at runtime.
	Check func(*hm.FunctionType) error
}

// ParamDef defines a parameter with optional default value.
type ParamDef struct {
	Name         string
	Type         hm.Type
	DefaultValue Value // nil if no default
}

type builtinRegistry struct {
	functions []BuiltinDef

	methods            map[*Type][]BuiltinDef
	methodByName       map[*Type]map[string]BuiltinDef
	methodReceivers    []*Type
	methodReceiverSeen map[*Type]bool

	staticMethods    map[*Type][]BuiltinDef
	staticModules    []*Type
	staticModuleSeen map[*Type]bool
}

func newBuiltinRegistry() *builtinRegistry {
	return &builtinRegistry{
		methods:            make(map[*Type][]BuiltinDef),
		methodByName:       make(map[*Type]map[string]BuiltinDef),
		methodReceiverSeen: make(map[*Type]bool),
		staticMethods:      make(map[*Type][]BuiltinDef),
		staticModuleSeen:   make(map[*Type]bool),
	}
}

var builtins = newBuiltinRegistry()

// Register adds a builtin definition to the registry.
func Register(def BuiltinDef) {
	builtins.register(def)
}

func (r *builtinRegistry) register(def BuiltinDef) {
	if def.IsMethod && def.IsStatic {
		panic(fmt.Sprintf("builtin %q cannot be both method and static method", def.Name))
	}

	switch {
	case def.IsMethod:
		if def.ReceiverType == nil {
			panic(fmt.Sprintf("method builtin %q is missing a receiver", def.Name))
		}
		r.methods[def.ReceiverType] = append(r.methods[def.ReceiverType], def)
		if r.methodByName[def.ReceiverType] == nil {
			r.methodByName[def.ReceiverType] = make(map[string]BuiltinDef)
		}
		r.methodByName[def.ReceiverType][def.Name] = def
		if !r.methodReceiverSeen[def.ReceiverType] {
			r.methodReceiverSeen[def.ReceiverType] = true
			r.methodReceivers = append(r.methodReceivers, def.ReceiverType)
		}

	case def.IsStatic:
		if def.HostModule == nil {
			panic(fmt.Sprintf("static builtin %q is missing a host module", def.Name))
		}
		r.staticMethods[def.HostModule] = append(r.staticMethods[def.HostModule], def)
		if !r.staticModuleSeen[def.HostModule] {
			r.staticModuleSeen[def.HostModule] = true
			r.staticModules = append(r.staticModules, def.HostModule)
		}

	default:
		r.functions = append(r.functions, def)
	}
}

// LookupMethod finds a method for a given receiver type.
func LookupMethod(receiverType *Type, methodName string) (BuiltinDef, bool) {
	methods, ok := builtins.methodByName[receiverType]
	if !ok {
		return BuiltinDef{}, false
	}
	def, ok := methods[methodName]
	return def, ok
}

// GetMethodKey returns the environment key for a method.
func GetMethodKey(receiverType *Type, methodName string) string {
	return fmt.Sprintf("_%s_%s_builtin",
		strings.ToLower(receiverType.Named), methodName)
}

// ForEachFunction iterates over all registered functions.
func ForEachFunction(fn func(BuiltinDef)) {
	for _, def := range builtins.functions {
		fn(def)
	}
}

// builtinAliases maps internal names to the builtin function each also binds,
// for code the parser synthesizes, which must reach the builtin even where a
// program binds the builtin's name itself. They are not identifiers, so no
// source can refer to them (or list them, e.g. in completions).
var builtinAliases = map[string]string{
	forLoopBuiltin:     "loop",
	forCondLoopBuiltin: "loop",
}

// builtinNames returns the names a builtin function is bound by: its own
// name, then its aliases (see builtinAliases).
func builtinNames(def BuiltinDef) []string {
	names := []string{def.Name}
	for alias, name := range builtinAliases {
		if name == def.Name {
			names = append(names, alias)
		}
	}
	sort.Strings(names[1:])
	return names
}

var (
	builtinValueNamesOnce sync.Once
	builtinValueNames     map[string]bool
)

// isBuiltinValueName reports whether NewValueScope binds name in its
// builtins frame: a builtin function, an alias of one, a static module, or a
// binding of the Dang-source prelude.
func isBuiltinValueName(name string) bool {
	builtinValueNamesOnce.Do(func() {
		frame := NewObject(Prelude)
		addBuiltinFunctions(frame)
		builtinValueNames = map[string]bool{}
		for name := range frame.Values {
			builtinValueNames[name] = true
		}
		loadPrelude()
		for _, kv := range preludeBindings {
			builtinValueNames[kv.Key] = true
		}
	})
	return builtinValueNames[name]
}

// IsInternalName reports whether name is bound only for code the parser
// synthesizes (see builtinAliases): no identifier spells it, so listings of
// a scope's names, such as completions, leave it out.
func IsInternalName(name string) bool {
	return strings.HasPrefix(name, "$")
}

// ForEachMethod iterates over methods for a specific receiver type.
func ForEachMethod(receiverType *Type, fn func(BuiltinDef)) {
	for _, def := range builtins.methods[receiverType] {
		fn(def)
	}
}

// MethodReceivers returns all modules that have builtin methods registered.
func MethodReceivers() []*Type {
	return append([]*Type(nil), builtins.methodReceivers...)
}

// ForEachStaticMethod iterates over static methods for a specific host module.
func ForEachStaticMethod(hostModule *Type, fn func(BuiltinDef)) {
	for _, def := range builtins.staticMethods[hostModule] {
		fn(def)
	}
}

// StaticModules returns all modules that have static methods registered.
func StaticModules() []*Type {
	return append([]*Type(nil), builtins.staticModules...)
}

// shadowedPreludeMemberHint explains a member missing from local, a type
// declared or imported under a name the Prelude also binds, when what the
// Prelude binds there has it: the local declaration shadows the builtin.
// hasType reports whether the Prelude's type of that name has the member,
// and hasValue whether the module a Prelude value of that name is (a
// namespace such as JSON) has it. It returns "" otherwise. Only the error
// path calls it.
func shadowedPreludeMemberHint(local *Type, hasType, hasValue func(*Type) bool) string {
	if local == nil || local.Named == "" {
		return ""
	}
	found := false
	if sub, ok := Prelude.NamedType(local.Named); ok {
		if t, ok := sub.(*Type); ok && t != local && hasType(t) {
			found = true
		}
	}
	if scheme, ok := Prelude.LocalSchemeOf(local.Named); ok && !found {
		if t, mono := scheme.Type(); mono {
			if mod, ok := unwrapNonNull(t).(*Type); ok && mod != local && mod.metaOf != local && hasValue(mod) {
				found = true
			}
		}
	}
	if !found {
		return ""
	}
	if local.Qualifier != "" {
		return fmt.Sprintf("%s is a different type from the builtin %s, which has it", local, local.Named)
	}
	return fmt.Sprintf("the %s declared here shadows the builtin %s, which has it; rename the declaration to use the builtin", local.Named, local.Named)
}

// DefineEnum creates an enum type with the given values and registers it
// as a nested type on a parent module. It handles both type-level and
// eval-level registration so that the enum values are available during
// both type checking and evaluation.
//
// Usage:
//
//	var CharsetEnum = DefineEnum(RandomModule, "Charset",
//		"ALPHANUMERIC", "ALPHA", "NUMERIC", "HEX",
//	)
func DefineEnum(parent *Type, name string, values ...string) *Type {
	enum := NewType(name, EnumKind)

	// Type-level: register each value and a values() accessor
	for _, v := range values {
		enum.Add(v, hm.NewScheme(nil, NonNull(enum)))
		enum.SetVisibility(v, PublicVisibility)
	}
	valuesScheme := hm.NewScheme(nil, NonNull(ListType{NonNull(enum)}))
	enum.Add("values", valuesScheme)
	enum.SetVisibility("values", PublicVisibility)

	// Register on parent as both a object and a value (its namespace)
	parent.AddObject(name, enum)
	parent.Add(name, hm.NewScheme(nil, enumNamespaceType(enum)))
	parent.SetVisibility(name, PublicVisibility)

	return enum
}

// TypeVar creates a type variable
func TypeVar(r rune) hm.Type {
	return hm.TypeVariable(r)
}

// NonNull wraps a type in NonNullType
func NonNull(t hm.Type) hm.Type {
	return hm.NonNullType{Type: t}
}

// Nullable returns the nullable form of a type. A function type's is
// hm.NullableFunctionType, which can't be called until narrowed.
func Nullable(t hm.Type) hm.Type {
	return hm.MakeNullable(t)
}

// ListOf creates a list type
func ListOf(t hm.Type) hm.Type {
	return ListType{Type: t}
}

// MapOf creates a string-keyed map type with the given value type
func MapOf(t hm.Type) hm.Type {
	return MapType{Type: t}
}

// Optional returns a nullable type with a default value
func Optional(t hm.Type, defaultVal Value) (hm.Type, Value) {
	// If t is already non-null, unwrap it to make it nullable
	if nn, ok := t.(hm.NonNullType); ok {
		return nn.Type, defaultVal
	}
	return t, defaultVal
}
