package dang

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/hm"
)

// --- Go types under test ---------------------------------------------------

type ffiPerson struct {
	Name      string
	Age       int
	Height    float64
	Cool      bool
	Nicknames []string
	secret    string //nolint:unused // exercises unexported-field skipping
}

func (p ffiPerson) Greeting() string {
	return "Hello, " + p.Name
}

func (p *ffiPerson) Greet(other string) string {
	return p.Name + " greets " + other
}

func (p *ffiPerson) Validate() (string, error) {
	if p.Age < 0 {
		return "", errors.New("negative age")
	}
	return "ok", nil
}

// Mutator with no value return — must not be exposed.
func (p *ffiPerson) Birthday() {
	p.Age++
}

type ffiNode struct {
	Label    string
	Children []*ffiNode
}

// --- helpers ---------------------------------------------------------------

// runFFI parses, type-checks, and evaluates source. setupType installs FFI
// type registrations + bindings into the type environment; setupEval binds the
// matching runtime values.
func runFFI(t *testing.T, source string, setupType func(env Env), setupEval func(env EvalEnv)) (Value, error) {
	t.Helper()
	ctx := context.Background()

	parsed, err := Parse("test", []byte(source))
	require.NoError(t, err)
	block, ok := parsed.(*ModuleBlock)
	require.True(t, ok)

	typeEnv := NewPreludeEnv("test")
	setupType(typeEnv)
	if _, err := Infer(ctx, typeEnv, block, true); err != nil {
		return nil, err
	}

	evalEnv := NewEvalEnv(typeEnv)
	setupEval(evalEnv)
	return EvaluateFormsWithPhases(ctx, block.Forms, evalEnv)
}

func schemeType(t *testing.T, mod *Module, name string) hm.Type {
	t.Helper()
	scheme, found := mod.LocalSchemeOf(name)
	require.Truef(t, found, "field %q not found", name)
	typ, mono := scheme.Type()
	require.True(t, mono)
	return typ
}

// --- tests -----------------------------------------------------------------

func TestGoFFIDefaultNameMapper(t *testing.T) {
	cases := map[string]string{
		"Name":     "name",
		"Children": "children",
		"URL":      "url",
		"ID":       "id",
		"HTMLBody": "htmlBody",
		"A":        "a",
	}
	for in, want := range cases {
		require.Equalf(t, want, DefaultNameMapper(in), "DefaultNameMapper(%q)", in)
	}
}

func TestGoFFIRegisterTypeMapping(t *testing.T) {
	ffi := &GoFFI{}
	typeEnv := NewPreludeEnv("test")

	mod, err := ffi.RegisterType(typeEnv, reflect.TypeFor[*ffiPerson]())
	require.NoError(t, err)

	// Installed as a class.
	installed, found := typeEnv.NamedType("ffiPerson")
	require.True(t, found)
	require.Same(t, mod, installed)

	require.True(t, schemeType(t, mod, "name").Eq(NonNull(StringType)))
	require.True(t, schemeType(t, mod, "age").Eq(NonNull(IntType)))
	require.True(t, schemeType(t, mod, "height").Eq(NonNull(FloatType)))
	require.True(t, schemeType(t, mod, "cool").Eq(NonNull(BooleanType)))
	require.True(t, schemeType(t, mod, "nicknames").Eq(NonNull(ListType{NonNull(StringType)})))

	// Unexported field is skipped.
	_, found = mod.LocalSchemeOf("secret")
	require.False(t, found)

	// Value-receiver no-arg method -> nullary function returning String.
	greeting, ok := schemeType(t, mod, "greeting").(*hm.FunctionType)
	require.True(t, ok)
	require.True(t, greeting.Ret(false).Eq(NonNull(StringType)))

	// Pointer-receiver method with an argument.
	greet, ok := schemeType(t, mod, "greet").(*hm.FunctionType)
	require.True(t, ok)
	greetArgs, ok := greet.Arg().(*RecordType)
	require.True(t, ok)
	require.Len(t, greetArgs.Fields, 1)
	require.Equal(t, "arg1", greetArgs.Fields[0].Key)

	// Method returning (T, error) exposes T.
	validate, ok := schemeType(t, mod, "validate").(*hm.FunctionType)
	require.True(t, ok)
	require.True(t, validate.Ret(false).Eq(NonNull(StringType)))

	// Void mutator is not exposed.
	_, found = mod.LocalSchemeOf("birthday")
	require.False(t, found)
}

func TestGoFFIFieldAccessAndMethodCall(t *testing.T) {
	ffi := &GoFFI{}
	person := &ffiPerson{Name: "Ada", Age: 36, Nicknames: []string{"Countess"}}

	val, err := runFFI(t, `p.name`,
		func(env Env) {
			mod, err := ffi.RegisterType(env, reflect.TypeFor[*ffiPerson]())
			require.NoError(t, err)
			env.Add("p", hm.NewScheme(nil, NonNull(mod)))
			env.SetVisibility("p", PublicVisibility)
		},
		func(env EvalEnv) {
			wrapped, err := ffi.WrapGoValue(person)
			require.NoError(t, err)
			env.Bind("p", wrapped, PublicVisibility)
		},
	)
	require.NoError(t, err)
	require.Equal(t, "Ada", val.String())
}

func TestGoFFIAutoCalledMethod(t *testing.T) {
	ffi := &GoFFI{}
	person := &ffiPerson{Name: "Ada"}

	val, err := runFFI(t, `p.greeting`,
		func(env Env) {
			mod, err := ffi.RegisterType(env, reflect.TypeFor[*ffiPerson]())
			require.NoError(t, err)
			env.Add("p", hm.NewScheme(nil, NonNull(mod)))
			env.SetVisibility("p", PublicVisibility)
		},
		func(env EvalEnv) {
			wrapped, err := ffi.WrapGoValue(person)
			require.NoError(t, err)
			env.Bind("p", wrapped, PublicVisibility)
		},
	)
	require.NoError(t, err)
	require.Equal(t, "Hello, Ada", val.String())
}

func TestGoFFIMethodWithArgument(t *testing.T) {
	ffi := &GoFFI{}
	person := &ffiPerson{Name: "Ada"}

	val, err := runFFI(t, `p.greet(arg1: "Babbage")`,
		func(env Env) {
			mod, err := ffi.RegisterType(env, reflect.TypeFor[*ffiPerson]())
			require.NoError(t, err)
			env.Add("p", hm.NewScheme(nil, NonNull(mod)))
			env.SetVisibility("p", PublicVisibility)
		},
		func(env EvalEnv) {
			wrapped, err := ffi.WrapGoValue(person)
			require.NoError(t, err)
			env.Bind("p", wrapped, PublicVisibility)
		},
	)
	require.NoError(t, err)
	require.Equal(t, "Ada greets Babbage", val.String())
}

func TestGoFFIErrorReturnRaises(t *testing.T) {
	ffi := &GoFFI{}
	person := &ffiPerson{Name: "Ada", Age: -1}

	setupType := func(env Env) {
		mod, err := ffi.RegisterType(env, reflect.TypeFor[*ffiPerson]())
		require.NoError(t, err)
		env.Add("p", hm.NewScheme(nil, NonNull(mod)))
		env.SetVisibility("p", PublicVisibility)
	}
	setupEval := func(env EvalEnv) {
		wrapped, err := ffi.WrapGoValue(person)
		require.NoError(t, err)
		env.Bind("p", wrapped, PublicVisibility)
	}

	// Non-nil error propagates as a raised error.
	_, err := runFFI(t, `p.validate`, setupType, setupEval)
	require.Error(t, err)
	require.Contains(t, err.Error(), "negative age")

	// Catchable as a BasicError.
	val, err := runFFI(t, `try { p.validate } catch { err => err.message }`, setupType, setupEval)
	require.NoError(t, err)
	require.Equal(t, "negative age", val.String())
}

func TestGoFFIRecursiveType(t *testing.T) {
	ffi := &GoFFI{}
	root := &ffiNode{
		Label: "root",
		Children: []*ffiNode{
			{Label: "first"},
			{Label: "second", Children: []*ffiNode{{Label: "grandchild"}}},
		},
	}

	val, err := runFFI(t, `root.children[1].children[0].label`,
		func(env Env) {
			mod, err := ffi.RegisterType(env, reflect.TypeFor[*ffiNode]())
			require.NoError(t, err)
			env.Add("root", hm.NewScheme(nil, NonNull(mod)))
			env.SetVisibility("root", PublicVisibility)
		},
		func(env EvalEnv) {
			wrapped, err := ffi.WrapGoValue(root)
			require.NoError(t, err)
			env.Bind("root", wrapped, PublicVisibility)
		},
	)
	require.NoError(t, err)
	require.Equal(t, "grandchild", val.String())
}

// --- interface / converter -------------------------------------------------

type ffiStamp struct{ code string }

func (s ffiStamp) String() string { return "stamp:" + s.code }

type ffiEnvelope struct {
	Mark fmt.Stringer
}

func TestGoFFIUnregisteredInterfaceErrors(t *testing.T) {
	ffi := &GoFFI{}
	typeEnv := NewPreludeEnv("test")
	_, err := ffi.RegisterType(typeEnv, reflect.TypeFor[*ffiEnvelope]())
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a registered Converter")
}

func TestGoFFIInterfaceConverter(t *testing.T) {
	ffi := &GoFFI{}
	ffi.RegisterConverter(reflect.TypeFor[fmt.Stringer](), Converter{
		DangType: func(env Env) (hm.Type, error) {
			return NonNull(StringType), nil
		},
		ToDang: func(v reflect.Value) (Value, error) {
			if v.IsNil() {
				return NullValue{}, nil
			}
			return StringValue{Val: v.Interface().(fmt.Stringer).String()}, nil
		},
	})

	env := &ffiEnvelope{Mark: ffiStamp{code: "A1"}}

	val, err := runFFI(t, `e.mark`,
		func(typeEnv Env) {
			mod, err := ffi.RegisterType(typeEnv, reflect.TypeFor[*ffiEnvelope]())
			require.NoError(t, err)
			typeEnv.Add("e", hm.NewScheme(nil, NonNull(mod)))
			typeEnv.SetVisibility("e", PublicVisibility)
		},
		func(evalEnv EvalEnv) {
			wrapped, err := ffi.WrapGoValue(env)
			require.NoError(t, err)
			evalEnv.Bind("e", wrapped, PublicVisibility)
		},
	)
	require.NoError(t, err)
	require.Equal(t, "stamp:A1", val.String())
}

func TestGoFFIWrapUnregisteredErrors(t *testing.T) {
	ffi := &GoFFI{}
	_, err := ffi.WrapGoValue(&ffiPerson{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "has not been registered")
}
