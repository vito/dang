package dang

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/vito/dang/pkg/hm"
)

// GoFFI is a reflection-driven bridge that exposes arbitrary Go types and
// values to Dang code. It derives a Dang *Module from a reflect.Type, wraps a
// Go value as a Dang Value, and invokes Go methods from Dang.
//
// It is a sibling to the GraphQL/Dagger import path: same target (Dang's
// Module / ModuleValue / Value model), different source. A zero GoFFI is
// usable; maps are lazily initialised.
type GoFFI struct {
	cache      map[reflect.Type]*Module
	converters map[reflect.Type]Converter

	// NameMapper maps a Go field/method name to its Dang spelling. When nil,
	// DefaultNameMapper (PascalCase -> camelCase) is used.
	NameMapper func(goName string) string
}

// Converter is a custom value-mapper for a specific reflect.Type. It bridges
// interfaces and host-specific types (e.g. an embedder's Content interface to
// a pre-existing Dang type). When the FFI encounters a field, argument, or
// return value whose type has a Converter registered, it calls the Converter
// instead of doing default reflection.
type Converter struct {
	// DangType returns the Dang type this Go type maps to. Called at
	// registration time; env is the environment the type is being installed
	// into (useful for resolving pre-existing named types).
	DangType func(env Env) (hm.Type, error)
	// ToDang converts a live Go value to a Dang Value.
	ToDang func(v reflect.Value) (Value, error)
	// FromDang converts a Dang Value back to a Go value. Optional; only needed
	// if Go-side methods take this type as an argument.
	FromDang func(v Value) (reflect.Value, error)
}

var errorType = reflect.TypeFor[error]()

func (ffi *GoFFI) ensureInit() {
	if ffi.cache == nil {
		ffi.cache = make(map[reflect.Type]*Module)
	}
	if ffi.converters == nil {
		ffi.converters = make(map[reflect.Type]Converter)
	}
}

// RegisterConverter installs a custom Converter for a specific reflect.Type.
func (ffi *GoFFI) RegisterConverter(t reflect.Type, c Converter) {
	ffi.ensureInit()
	ffi.converters[t] = c
}

// DefaultNameMapper lowercases the leading run of uppercase letters, mapping
// Go's PascalCase to Dang's camelCase (Children -> children, URL -> url,
// HTMLBody -> htmlBody).
func DefaultNameMapper(goName string) string {
	if goName == "" {
		return goName
	}
	runes := []rune(goName)
	// Find the length of the leading uppercase run.
	upper := 0
	for upper < len(runes) && unicode.IsUpper(runes[upper]) {
		upper++
	}
	switch {
	case upper == 0:
		return goName
	case upper == len(runes):
		// All uppercase (e.g. "URL", "ID") -> all lowercase.
		return strings.ToLower(goName)
	case upper == 1:
		// Normal PascalCase (e.g. "Children").
		runes[0] = unicode.ToLower(runes[0])
	default:
		// Acronym followed by a word (e.g. "HTMLBody"): lowercase all but the
		// final uppercase letter, which begins the next word.
		for i := 0; i < upper-1; i++ {
			runes[i] = unicode.ToLower(runes[i])
		}
	}
	return string(runes)
}

func (ffi *GoFFI) dangName(goName string) string {
	if ffi.NameMapper != nil {
		return ffi.NameMapper(goName)
	}
	return DefaultNameMapper(goName)
}

// argName synthesises a Dang argument name for the i-th (0-based) Go method
// parameter. Reflection cannot recover real parameter names, so positional
// names are used: arg1, arg2, ...
func argName(i int) string {
	return fmt.Sprintf("arg%d", i+1)
}

// RegisterType derives a *Module from a Go type and installs it in env as a
// class. It is idempotent: re-registering a type returns the cached Module.
// Recursive types (a Foo with a []*Foo field) are handled by inserting the
// Module in the cache before walking fields. A pointer type and its element
// type register the same Module.
func (ffi *GoFFI) RegisterType(env Env, t reflect.Type) (*Module, error) {
	ffi.ensureInit()

	rt := t
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("goffi: RegisterType expects a struct or pointer-to-struct, got %s", t)
	}

	if mod, ok := ffi.cache[rt]; ok {
		// Ensure the cached module is reachable from env (a type may have been
		// registered against a different env earlier). env is nil when the
		// type is being resolved during value conversion, where it's already
		// cached and no installation is needed.
		if env != nil {
			if _, found := env.LocalNamedType(mod.Named); !found {
				env.AddClass(mod.Named, mod)
			}
		}
		return mod, nil
	}

	name := rt.Name()
	if name == "" {
		return nil, fmt.Errorf("goffi: cannot register anonymous struct type %s", rt)
	}

	mod := NewModule(name, ObjectKind)
	// Insert before walking fields so recursive references resolve to this
	// same Module via the cache.
	ffi.cache[rt] = mod
	if env != nil {
		env.AddClass(name, mod)
	}

	// Fields.
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		ft, err := ffi.dangType(env, f.Type)
		if err != nil {
			return nil, fmt.Errorf("goffi: %s.%s: %w", name, f.Name, err)
		}
		dn := ffi.dangName(f.Name)
		mod.Add(dn, hm.NewScheme(nil, ft))
		mod.SetVisibility(dn, PublicVisibility)
	}

	// Methods. Use the pointer type so both value- and pointer-receiver
	// methods are visible.
	pt := reflect.PointerTo(rt)
	for i := 0; i < pt.NumMethod(); i++ {
		m := pt.Method(i)
		fnType, ok, err := ffi.methodType(env, m)
		if err != nil {
			return nil, fmt.Errorf("goffi: %s.%s: %w", name, m.Name, err)
		}
		if !ok {
			// Unmappable or deliberately-skipped method (void mutator,
			// multi-return, unsupported arg/return type).
			continue
		}
		dn := ffi.dangName(m.Name)
		mod.Add(dn, hm.NewScheme(nil, fnType))
		mod.SetVisibility(dn, PublicVisibility)
	}

	return mod, nil
}

// methodType derives the Dang function type for a Go method. The bool result
// reports whether the method should be exposed; methods that are void mutators,
// multi-return, or use unsupported argument/return types are skipped (ok=false,
// nil error) so that registering an arbitrary Go type stays robust.
func (ffi *GoFFI) methodType(env Env, m reflect.Method) (*hm.FunctionType, bool, error) {
	mt := m.Type // includes the receiver as parameter 0
	if mt.IsVariadic() {
		return nil, false, nil
	}

	args := NewRecordType("")
	for i := 1; i < mt.NumIn(); i++ {
		at, err := ffi.dangType(env, mt.In(i))
		if err != nil {
			// Argument type we can't bridge: skip the whole method rather than
			// failing registration of the type.
			return nil, false, nil
		}
		args.Add(argName(i-1), hm.NewScheme(nil, at))
	}

	outs := mt.NumOut()
	hasErr := outs > 0 && mt.Out(outs-1) == errorType
	if hasErr {
		outs--
	}

	switch outs {
	case 0:
		// No value return (pure side-effect / mutator) — not exposed.
		return nil, false, nil
	case 1:
		ret, err := ffi.dangType(env, mt.Out(0))
		if err != nil {
			return nil, false, nil
		}
		return hm.NewFnType(args, ret), true, nil
	default:
		// Multi-value returns have no natural Dang shape yet.
		return nil, false, nil
	}
}

// dangType maps a reflect.Type to its Dang hm.Type. Struct types are
// recursively registered. Interface types must have a registered Converter.
func (ffi *GoFFI) dangType(env Env, t reflect.Type) (hm.Type, error) {
	if c, ok := ffi.converters[t]; ok {
		if c.DangType == nil {
			return nil, fmt.Errorf("converter for %s has no DangType", t)
		}
		return c.DangType(env)
	}

	switch t.Kind() {
	case reflect.String:
		return NonNull(StringType), nil
	case reflect.Bool:
		return NonNull(BooleanType), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return NonNull(IntType), nil
	case reflect.Float32, reflect.Float64:
		return NonNull(FloatType), nil
	case reflect.Pointer:
		inner, err := ffi.dangType(env, t.Elem())
		if err != nil {
			return nil, err
		}
		// A pointer is nullable: drop the NonNull wrapper.
		if nn, ok := inner.(hm.NonNullType); ok {
			return nn.Type, nil
		}
		return inner, nil
	case reflect.Slice, reflect.Array:
		elem, err := ffi.dangType(env, t.Elem())
		if err != nil {
			return nil, err
		}
		return NonNull(ListType{elem}), nil
	case reflect.Struct:
		mod, err := ffi.RegisterType(env, t)
		if err != nil {
			return nil, err
		}
		return NonNull(mod), nil
	case reflect.Interface:
		return nil, fmt.Errorf("interface type %s requires a registered Converter", t)
	case reflect.Map:
		return nil, fmt.Errorf("map types are not yet supported (%s)", t)
	default:
		return nil, fmt.Errorf("unsupported Go type %s (kind %s)", t, t.Kind())
	}
}

// WrapGoValue wraps an arbitrary Go value as a Dang Value. The underlying type
// must have been registered (via RegisterType) or have a Converter.
func (ffi *GoFFI) WrapGoValue(v any) (Value, error) {
	ffi.ensureInit()
	if v == nil {
		return NullValue{}, nil
	}
	return ffi.toDang(reflect.ValueOf(v))
}

// toDang is the recursive Go-value -> Dang-Value conversion.
func (ffi *GoFFI) toDang(rv reflect.Value) (Value, error) {
	if c, ok := ffi.converters[rv.Type()]; ok {
		if c.ToDang == nil {
			return nil, fmt.Errorf("converter for %s has no ToDang", rv.Type())
		}
		return c.ToDang(rv)
	}

	switch rv.Kind() {
	case reflect.String:
		return StringValue{Val: rv.String()}, nil
	case reflect.Bool:
		return BoolValue{Val: rv.Bool()}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return IntValue{Val: int(rv.Int())}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return IntValue{Val: int(rv.Uint())}, nil
	case reflect.Float32, reflect.Float64:
		return FloatValue{Val: rv.Float()}, nil
	case reflect.Pointer:
		if rv.IsNil() {
			return NullValue{}, nil
		}
		return ffi.toDang(rv.Elem())
	case reflect.Interface:
		if rv.IsNil() {
			return NullValue{}, nil
		}
		return ffi.toDang(rv.Elem())
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return NullValue{}, nil
		}
		elemDang, err := ffi.dangType(nil, rv.Type().Elem())
		if err != nil {
			return nil, err
		}
		elems := make([]Value, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			ev, err := ffi.toDang(rv.Index(i))
			if err != nil {
				return nil, err
			}
			elems[i] = ev
		}
		return ListValue{Elements: elems, ElemType: elemDang}, nil
	case reflect.Struct:
		return ffi.wrapStruct(rv)
	default:
		return nil, fmt.Errorf("goffi: cannot convert Go value of type %s to a Dang value", rv.Type())
	}
}

// wrapStruct produces a *ModuleValue backed by the live struct. Field access
// reflects into the struct lazily; methods are bound to a pointer to the
// struct so pointer-receiver methods work and pointer identity flows through.
func (ffi *GoFFI) wrapStruct(rv reflect.Value) (Value, error) {
	rt := rv.Type()
	mod, ok := ffi.cache[rt]
	if !ok {
		return nil, fmt.Errorf("goffi: type %s has not been registered", rt)
	}

	// Ensure the struct is addressable so we can take its pointer for method
	// receivers. A struct reached by value (not through a pointer) is copied
	// into an addressable location.
	if !rv.CanAddr() {
		pv := reflect.New(rt)
		pv.Elem().Set(rv)
		rv = pv.Elem()
	}

	mv := NewModuleValue(mod)

	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		fieldVal := rv.Field(i)
		dn := ffi.dangName(f.Name)
		mv.BindLazy(dn, func(ctx context.Context) (Value, error) {
			return ffi.toDang(fieldVal)
		}, PublicVisibility)
	}

	recv := rv.Addr()
	pt := recv.Type()
	for i := 0; i < pt.NumMethod(); i++ {
		m := pt.Method(i)
		dn := ffi.dangName(m.Name)
		scheme, found := mod.LocalSchemeOf(dn)
		if !found {
			// Method was not exposed during registration.
			continue
		}
		fnType, _ := scheme.Type()
		ft, ok := fnType.(*hm.FunctionType)
		if !ok {
			continue
		}
		mv.Bind(dn, &goFn{
			recv:   recv,
			method: m,
			fnType: ft,
			ffi:    ffi,
		}, PublicVisibility)
	}

	return mv, nil
}

// goFn is a Callable wrapping a bound Go method.
type goFn struct {
	recv   reflect.Value // pointer to the receiver struct
	method reflect.Method
	fnType *hm.FunctionType
	ffi    *GoFFI
}

var _ Callable = (*goFn)(nil)

func (f *goFn) Type() hm.Type { return f.fnType }

func (f *goFn) String() string {
	return fmt.Sprintf("go:%s.%s", f.recv.Type().Elem().Name(), f.method.Name)
}

func (f *goFn) ParameterNames() []string {
	rec, ok := f.fnType.Arg().(*RecordType)
	if !ok {
		return nil
	}
	names := make([]string, len(rec.Fields))
	for i, field := range rec.Fields {
		names[i] = field.Key
	}
	return names
}

func (f *goFn) IsAutoCallable() bool {
	return len(f.ParameterNames()) == 0
}

func (f *goFn) Call(ctx context.Context, env EvalEnv, args map[string]Value) (Value, error) {
	mt := f.method.Type
	in := make([]reflect.Value, mt.NumIn())
	in[0] = f.recv
	for i := 1; i < mt.NumIn(); i++ {
		want := mt.In(i)
		name := argName(i - 1)
		v, ok := args[name]
		if !ok {
			in[i] = reflect.Zero(want)
			continue
		}
		rv, err := f.ffi.fromDang(v, want)
		if err != nil {
			return nil, fmt.Errorf("goffi: %s: argument %q: %w", f.String(), name, err)
		}
		in[i] = rv
	}

	out := f.method.Func.Call(in)

	n := len(out)
	if n > 0 && mt.Out(n-1) == errorType {
		if errv := out[n-1]; !errv.IsNil() {
			err := errv.Interface().(error)
			return nil, &RaisedError{Value: newBasicError(err.Error())}
		}
		n--
	}
	if n == 0 {
		return NullValue{}, nil
	}
	return f.ffi.toDang(out[0])
}

// fromDang converts a Dang Value into a Go reflect.Value assignable to want.
func (ffi *GoFFI) fromDang(v Value, want reflect.Type) (reflect.Value, error) {
	if c, ok := ffi.converters[want]; ok && c.FromDang != nil {
		return c.FromDang(v)
	}

	if _, isNull := v.(NullValue); isNull {
		return reflect.Zero(want), nil
	}

	if want.Kind() == reflect.Pointer {
		inner, err := ffi.fromDang(v, want.Elem())
		if err != nil {
			return reflect.Value{}, err
		}
		p := reflect.New(want.Elem())
		p.Elem().Set(inner)
		return p, nil
	}

	switch want.Kind() {
	case reflect.String:
		sv, ok := v.(StringValue)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected String, got %s", v.Type())
		}
		out := reflect.New(want).Elem()
		out.SetString(sv.Val)
		return out, nil
	case reflect.Bool:
		bv, ok := v.(BoolValue)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected Boolean, got %s", v.Type())
		}
		out := reflect.New(want).Elem()
		out.SetBool(bv.Val)
		return out, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		iv, ok := v.(IntValue)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected Int, got %s", v.Type())
		}
		out := reflect.New(want).Elem()
		out.SetInt(int64(iv.Val))
		return out, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		iv, ok := v.(IntValue)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected Int, got %s", v.Type())
		}
		out := reflect.New(want).Elem()
		out.SetUint(uint64(iv.Val))
		return out, nil
	case reflect.Float32, reflect.Float64:
		switch n := v.(type) {
		case FloatValue:
			out := reflect.New(want).Elem()
			out.SetFloat(n.Val)
			return out, nil
		case IntValue:
			out := reflect.New(want).Elem()
			out.SetFloat(float64(n.Val))
			return out, nil
		default:
			return reflect.Value{}, fmt.Errorf("expected Float, got %s", v.Type())
		}
	case reflect.Slice:
		lv, ok := v.(ListValue)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected List, got %s", v.Type())
		}
		out := reflect.MakeSlice(want, len(lv.Elements), len(lv.Elements))
		for i, el := range lv.Elements {
			ev, err := ffi.fromDang(el, want.Elem())
			if err != nil {
				return reflect.Value{}, fmt.Errorf("element %d: %w", i, err)
			}
			out.Index(i).Set(ev)
		}
		return out, nil
	default:
		return reflect.Value{}, fmt.Errorf("cannot convert Dang value to Go type %s", want)
	}
}
