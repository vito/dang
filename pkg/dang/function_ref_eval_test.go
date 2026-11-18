package dang

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// `&x` must not flip AutoCall on the shared AST while it evaluates: the same
// node can be evaluated by several `{{ }}` tasks at once, so a temporary write
// races with their reads. The lazy initializer runs mid-evaluation, inside the
// window the old flip-and-restore left the flag cleared.
func TestFunctionRefEvalLeavesASTUntouched(t *testing.T) {
	ctx := context.Background()

	sym := &Symbol{Name: "f", AutoCall: true}
	ref := &FunctionRef{Expr: sym}

	fn := BuiltinFunction{
		Name:   "f",
		FnType: hm.NewFnType(NewRecordType(""), NonNull(IntType)),
		CallFn: func(context.Context, ValueScope, map[string]Value) (Value, error) {
			return IntValue{Val: 1}, nil
		},
	}

	scope := NewObject(NewType("scope", ObjectKind))
	var during bool
	scope.BindLazy("f", func(context.Context) (Value, error) {
		during = sym.AutoCall
		return fn, nil
	}, PrivateVisibility)

	val, err := EvalNode(ctx, scope, ref)
	require.NoError(t, err)
	require.IsType(t, BuiltinFunction{}, val, "&f yields the function, uncalled")
	require.True(t, during, "AutoCall was flipped on the shared node during evaluation")
	require.True(t, sym.AutoCall)
}

func TestAutoCallOptionalArguments(t *testing.T) {
	for _, tc := range []struct {
		name, params, fields, expression, expected string
	}{
		{"nullable", "x: Int", "x: Int", "x ?? 9", "9"},
		{"nullable default", "x: Int = 7", "x: Int = 7", "x ?? 9", "7"},
		{"non-null default", "x: Int! = 7", "x: Int! = 7", "x", "7"},
		{"mixed optional", "x: Int, y: Int! = 3", "x: Int\n  y: Int! = 3", "(x ?? 9) + y", "12"},
		{"no arguments", "", "", "9", "9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`let f(%[1]s): Int! { %[3]s }

type P {
  %[2]s
  result: Int! { %[3]s }
}

type Explicit {
  value: Int!
  new(%[1]s) {
    self.value = %[3]s
    self
  }
}

type Methods {
  compute(%[1]s): Int! { %[3]s }
}

let functionAlias = &f
let constructorAlias = &P
let explicitAlias = &Explicit
let selected = {{ make: &P, compute: &f }}
print(f)
print(f())
print(functionAlias)
print(functionAlias())
print(P.result)
print(P().result)
print(constructorAlias.result)
print(constructorAlias().result)
print(Explicit.value)
print(Explicit().value)
print(explicitAlias.value)
print(explicitAlias().value)
print(Methods.compute)
print(Methods.compute())
print(selected.make.result)
print(selected.make().result)
print(selected.compute)
print(selected.compute())
`, tc.params, tc.fields, tc.expression)
			out, err := evalWithContext(t, t.Context(), source)
			require.NoError(t, err)
			var expected string
			for range 18 {
				expected += tc.expected + "\n"
			}
			require.Equal(t, expected, out)
		})
	}
}

func TestAutoCallConstructorFallbacks(t *testing.T) {
	out, err := evalWithContext(t, t.Context(), `type Q {
  n: Int! = 1
}
type P {
  runners: [String!]! = []
  coverage: Q
}
let maybe: P = null
print((maybe ?? P).runners.length)
print((maybe ?? P()).runners.length)
print((if (false) { maybe! } else { P }).runners.length)
print((if (false) { maybe! } else { P() }).runners.length)
`)
	require.NoError(t, err)
	require.Equal(t, "0\n0\n0\n0\n", out)
}

func TestAutoCallRequiredArgumentsAndBlocks(t *testing.T) {
	out, err := evalWithContext(t, t.Context(), `let f(x: Int!): Int! { x }
let withBlock(x: Int, &body(): Int!): Int! { body }
type P { x: Int! }
type Explicit {
  x: Int!
  new(x: Int!) { self.x = x; self }
}
type WithBlock {
  x: Int!
  new(x: Int, &body(): Int!) { self.x = body; self }
}
let functionAlias = &f
let constructorAlias = &P
let explicitAlias = &Explicit
let blockFunctionAlias = &withBlock
let blockConstructorAlias = &WithBlock
print(functionAlias(1))
print(constructorAlias(2).x)
print(explicitAlias(3).x)
print((blockFunctionAlias { 4 }))
print((blockConstructorAlias { 5 }).x)
`)
	require.NoError(t, err)
	require.Equal(t, "1\n2\n3\n4\n5\n", out)

	for _, source := range []string{
		`let f(x: Int!): Int! { x }; f()`,
		`type P { x: Int! }; P()`,
		`type P { new(x: Int!) { self } }; P()`,
		`let f(&body(): Int!): Int! { body }; f()`,
		`let f(&body(): Int!): Int! { body }; f`,
		`type P { new(&body(): Int!) { self } }; P()`,
		`type P { new(&body(): Int!) { self } }; P`,
	} {
		t.Run(source, func(t *testing.T) {
			parsed, err := ParseWithRecovery("autocall", []byte(source))
			require.NoError(t, err)
			types, _ := BuildScopesFromImports("", nil)
			_, err = InferFormsWithPhases(t.Context(), parsed.(*FileBlock).Forms, types, hm.NewSimpleFresher())
			require.Error(t, err, "omitting required arguments must fail")
		})
	}
}

func TestAutoCallNativeOptionalArguments(t *testing.T) {
	args := NewRecordType("", Keyed[*hm.Scheme]{Key: "x", Value: hm.NewScheme(nil, IntType)})
	inputType := NewType("Input", ObjectKind)
	inputFn := InputObjectConstructor{
		TypeName: "Input", TypeScope: inputType,
		FnType: hm.NewFnType(args, NonNull(inputType)),
	}
	def := BuiltinDef{
		ParamTypes: []ParamDef{
			{Name: "x", Type: IntType},
			{Name: "y", Type: NonNull(IntType), DefaultValue: IntValue{Val: 7}},
		},
		ReturnType: NonNull(IntType),
	}
	builtin := BuiltinFunction{
		FnType: createFunctionTypeFromDef(def),
		CallFn: func(_ context.Context, _ ValueScope, args map[string]Value) (Value, error) {
			return applyDefaults(args, def)["y"], nil
		},
	}
	for _, callable := range []Callable{inputFn, builtin} {
		scope := NewObject(NewType("scope", ObjectKind))
		scope.Bind("f", callable, PublicVisibility)
		symbol := &Symbol{Name: "f", AutoCall: true}
		bare, err := EvalNode(t.Context(), scope, symbol)
		require.NoError(t, err)
		explicit, err := EvalNode(t.Context(), scope, &FunCall{Fun: &Symbol{Name: "f"}})
		require.NoError(t, err)
		require.Equal(t, explicit, bare)
		require.False(t, isAutoCallableFn(bare), "the result must no longer be the callable")
		ref, err := EvalNode(t.Context(), scope, &FunctionRef{Expr: symbol})
		require.NoError(t, err)
		require.IsType(t, callable, ref, "&f must still suppress auto-calling")
	}
}

func TestAutoCallCollectionMethodsUseReceiverSignature(t *testing.T) {
	out, err := evalWithContext(t, t.Context(), `let contains = [1, 2, 3].contains
let indexOf = [1, 2, 3].indexOf
assert { contains(2) }
assert { indexOf(3) == 2 }
assert { [1, 2, 3].contains(2) }
assert { [1, 2, 3].indexOf(3) == 2 }

let nullable: [Int]! = [1, null, 3]
let nullableContains = nullable.contains
let nullableIndexOf = nullable.indexOf
assert { nullableContains == nullable.contains() }
assert { nullableIndexOf == nullable.indexOf() }
let explicitContains = &nullable.contains
let explicitIndexOf = &nullable.indexOf
assert { explicitContains(1) }
assert { explicitIndexOf(3) == 2 }

# The annotation, not the narrower literal's runtime element type, is binding.
let widened: [Int]! = [1, 2, 3]
assert { widened.contains == widened.contains() }
assert { widened.indexOf == widened.indexOf() }

# Binding an optional method must not change the shared builtin registration.
let requiredAgain = [4, 5].contains
assert { requiredAgain(5) }
assert { [1, 2].length == [1, 2].length() }

let entries = ["one": 1, "two": 2]
let get = entries.get
let with = entries.with
assert { get("one") == 1 }
assert { with("three", 3).get("three") == 3 }
assert { entries.values == entries.values() }
let nullableEntries: Map[Int]! = ["one": 1, "two": null]
let nullableGet = nullableEntries.get
assert { nullableGet("two") == null }
assert { nullableEntries.values == nullableEntries.values() }

has[a](xs: [a!]!, value: a!): Boolean! {
  let contains = xs.contains
  contains(value)
}
position[a](xs: [a!]!, value: a!): Int {
  let indexOf = xs.indexOf
  indexOf(value)
}
missing[a](entries: Map[a]!, key: String!): Boolean! {
  let get = entries.get
  get(key) == null
}
assert { has([1, 2], 2) }
assert { has(["a", "b"], "b") }
assert { position([1, 2], 2) == 1 }
assert { position(["a", "b"], "b") == 1 }
assert { !missing(entries, "one") }
assert { missing(nullableEntries, "two") }
`)
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestAutoCallEligibilityMatchesEmptyCall(t *testing.T) {
	for _, tc := range []struct {
		name      string
		argType   hm.Type
		defaulted bool
		block     bool
		want      bool
	}{
		{name: "no arguments", want: true},
		{name: "nullable", argType: IntType, want: true},
		{name: "required", argType: NonNull(IntType)},
		{name: "defaulted nullable", argType: IntType, defaulted: true, want: true},
		{name: "specialized default", argType: NonNull(IntType), defaulted: true, want: true},
		{name: "block", block: true},
		{name: "optional with block", argType: IntType, block: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := NewRecordType("")
			if tc.argType != nil {
				args.Add("x", hm.NewScheme(nil, tc.argType))
			}
			if tc.defaulted {
				args.Defaulted = map[string]bool{"x": true}
			}
			ft := hm.NewFnType(args, NonNull(IntType))
			if tc.block {
				ft.SetBlock(hm.NewFnType(NewRecordType(""), NonNull(IntType)))
			}
			for _, callable := range []Callable{
				FunctionValue{FnType: ft},
				&ConstructorFunction{FnType: ft},
				InputObjectConstructor{FnType: ft},
				BuiltinFunction{FnType: ft},
				BoundMethod{Method: FunctionValue{FnType: ft}},
				BoundBuiltinMethod{Method: BuiltinFunction{FnType: ft}},
			} {
				require.Equal(t, tc.want, callable.IsAutoCallable(), "%T", callable)
			}
			_, called, err := autoCallFnType(ft)
			if tc.block {
				require.ErrorContains(t, err, "requires a block argument")
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, called)
				err = (&FunCall{}).validateRequiredArgumentsInInfer(ft)
				if tc.want {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "missing required argument")
				}
			}
		})
	}
}
