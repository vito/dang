package dang

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// scalarJSONSchema defines a scalar named like the builtin JSON, as Dagger's
// schema does.
const scalarJSONSchema = `
type Query {
  echo(value: JSON!): JSON!
}

scalar JSON
`

// entrypointContract is the contract the Dagger engine declares for a Dang
// module entrypoint, written beside it at the entrypoint's Dang version.
const entrypointContract = `interface ModuleEntrypoint {
  call(
    receiverType: String!
    receiverValue: JSON
    fnName: String!
    fnArgs: JSON!
  ): JSON!
}
`

// entrypointCall has the shape of the entrypoint the Dagger Go SDK generates
// (pinned to Dang 2.1): bare JSON types, JSON.encode of the request, and a
// String result cast to JSON.
const entrypointCall = `type Entrypoint implements ModuleEntrypoint {
  pub call(
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
    callDigest: String,
  ): JSON! {
    let request = JSON.encode({{
      receiverType: receiverType,
      receiverValue: receiverValue,
      fnName: fnName,
      fnArgs: fnArgs,
    }})
    (request :: JSON!)
  }

  # The schema's JSON stays reachable qualified, and converts both ways.
  pub toSchema(j: JSON!): Dagger.JSON! { (j :: Dagger.JSON!) }
  pub fromSchema(j: Dagger.JSON!): JSON! { (j :: JSON!) }
}

pub entrypoint: Entrypoint! = Entrypoint()
`

// entrypointCallRequest is what entrypointCall returns for entrypointArgs:
// each JSON argument is JSON text, encoded as a string.
const entrypointCallRequest = `{"receiverType":"T","receiverValue":"{\"a\":1}","fnName":"f","fnArgs":"[1]"}`

// writeDangProject writes files into a new repository and returns its root.
func writeDangProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files[".git/HEAD"] = ""
	for name, contents := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	}
	return root
}

// daggerJSONContext auto-imports scalarJSONSchema as Dagger, as the Dagger
// SDK imports Dagger's schema into an entrypoint.
func daggerJSONContext(t *testing.T) context.Context {
	t.Helper()
	schema, err := SchemaFromSDL(scalarJSONSchema, "schema.graphqls")
	require.NoError(t, err)
	return ContextWithImportConfigs(context.Background(), ImportConfig{
		Name:       "Dagger",
		Schema:     schema,
		Client:     &fakeClient{},
		AutoImport: true,
	})
}

// writeEntrypoint writes an entrypoint module of the given Dang version ("",
// none) holding the contract and the entrypoint, as the engine does.
func writeEntrypoint(t *testing.T, version string) string {
	t.Helper()
	files := map[string]string{
		"contract.dang": entrypointContract,
		"main.dang":     entrypointCall,
	}
	if version != "" {
		files["dang.toml"] = "dang = \"" + version + "\"\n"
	}
	return writeDangProject(t, files)
}

// entrypointArgs are the arguments the Dagger engine calls the entrypoint's
// call with: a JSON argument is a ScalarValue of the parameter's scalar type
// (core/sdk/dang/v2 dangConverter.convertString).
func entrypointArgs(t *testing.T, call Callable) map[string]Value {
	t.Helper()
	args := map[string]Value{
		"receiverType": StringValue{Val: "T"},
		"fnName":       StringValue{Val: "f"},
	}
	params := parameterTypes(call)
	for name, val := range map[string]string{"receiverValue": `{"a":1}`, "fnArgs": `[1]`} {
		scalar, ok := unwrapNonNull(params[name]).(*Type)
		require.True(t, ok, "%s is %s", name, params[name])
		require.Equal(t, ScalarKind, scalar.Kind, "%s is %s", name, scalar)
		args[name] = ScalarValue{Val: val, ScalarType: scalar}
	}
	return args
}

// requireBuiltinJSON checks a value as the Dagger engine reads a call's result
// (core/sdk/dang/v2 entrypointJSONResult): a ScalarValue of the builtin JSON
// scalar, a ScalarKind *Type named JSON, holding want.
func requireBuiltinJSON(t *testing.T, result Value, want string) {
	t.Helper()
	scalar, ok := result.(ScalarValue)
	require.True(t, ok, "got %T", result)
	require.Same(t, JSONModule, scalar.ScalarType)
	mod, ok := scalar.ScalarType.(*Type)
	require.True(t, ok, "ScalarType is %T", scalar.ScalarType)
	require.Equal(t, ScalarKind, mod.Kind)
	require.Equal(t, "JSON", scalar.ScalarType.Name())
	require.JSONEq(t, want, scalar.Val)
}

// entrypointMethod returns the entrypoint's method name, bound to it.
func entrypointMethod(t *testing.T, ctx context.Context, env ValueScope, name string) Callable {
	t.Helper()
	ep, found := requireEvalGet(t, env, "entrypoint")
	require.True(t, found)
	method, found, err := ep.(ValueScope).Lookup(ctx, name)
	require.NoError(t, err)
	require.True(t, found)
	if fn, ok := method.(FunctionValue); ok {
		method = BoundMethod{Method: fn, Receiver: ep.(*Object)}
	}
	callable, ok := method.(Callable)
	require.True(t, ok, "%s is %T", name, method)
	return callable
}

// callEntrypoint calls the entrypoint as the Dagger engine does and checks
// the result.
func callEntrypoint(t *testing.T, ctx context.Context, env ValueScope) {
	t.Helper()
	call := entrypointMethod(t, ctx, env, "call")
	result, err := call.Call(ctx, env, entrypointArgs(t, call))
	require.NoError(t, err)
	requireBuiltinJSON(t, result, entrypointCallRequest)

	// JSON! :: Dagger.JSON! and back keep the text.
	toSchema := entrypointMethod(t, ctx, env, "toSchema")
	schemaJSON, err := toSchema.Call(ctx, env, map[string]Value{"j": result})
	require.NoError(t, err)
	scalar, ok := schemaJSON.(ScalarValue)
	require.True(t, ok, "got %T", schemaJSON)
	require.NotSame(t, JSONModule, scalar.ScalarType)
	require.Equal(t, "JSON", scalar.ScalarType.Name())
	fromSchema := entrypointMethod(t, ctx, env, "fromSchema")
	back, err := fromSchema.Call(ctx, env, map[string]Value{"j": schemaJSON})
	require.NoError(t, err)
	requireBuiltinJSON(t, back, entrypointCallRequest)

	// Dagger.JSON! :: JSON! validates.
	_, err = fromSchema.Call(ctx, env, map[string]Value{"j": ScalarValue{Val: "{", ScalarType: scalar.ScalarType}})
	require.ErrorContains(t, err, "invalid JSON")
}

// requireBuiltinJSONSignature checks the entrypoint's call signature without
// running anything, as the engine reads it: its JSON types are the builtin
// scalar.
func requireBuiltinJSONSignature(t *testing.T, env ValueScope) {
	t.Helper()
	ctor, found := requireEvalGet(t, env, "Entrypoint")
	require.True(t, found)
	constructor, ok := ctor.(*ConstructorFunction)
	require.True(t, ok, "Entrypoint is %T", ctor)
	scheme, found := constructor.ObjectType.SchemeOf("call")
	require.True(t, found)
	callType, _ := scheme.Type()
	ft, ok := callType.(*hm.FunctionType)
	require.True(t, ok, "call has type %T", callType)
	require.Equal(t, NonNull(JSONModule), ft.ReturnType())
	args, ok := ft.Arg().(*RecordType)
	require.True(t, ok)
	for _, name := range []string{"receiverValue", "fnArgs"} {
		argScheme, found := args.SchemeOf(name)
		require.True(t, found, name)
		argType, _ := argScheme.Type()
		require.Same(t, JSONModule, unwrapNonNull(argType), name)
	}
}

func TestBuiltinJSONEntrypoint(t *testing.T) {
	// The Dagger engine's entrypoint contract and the entrypoints its SDKs
	// generate spell the builtin JSON scalar bare, with Dagger's schema
	// (which has a JSON scalar of its own) auto-imported, at whatever Dang
	// version they are pinned to.
	for _, version := range []string{"2.1", "2.4", ""} {
		t.Run("version "+version, func(t *testing.T) {
			ctx := daggerJSONContext(t)
			dir := writeEntrypoint(t, version)

			t.Run("RunDir", func(t *testing.T) {
				env, err := RunDir(ctx, dir, false)
				require.NoError(t, err)
				callEntrypoint(t, ctx, env)
			})

			t.Run("RunFile", func(t *testing.T) {
				// A single file holds the contract and the entrypoint.
				file := filepath.Join(t.TempDir(), "main.dang")
				require.NoError(t, os.WriteFile(file, []byte(entrypointContract+"\n"+entrypointCall), 0o600))
				require.NoError(t, RunFile(ctx, file, false))
			})

			t.Run("DeclareDir", func(t *testing.T) {
				env, err := DeclareDir(ctx, dir, false)
				require.NoError(t, err)
				requireBuiltinJSONSignature(t, env)
			})

			t.Run("LoadDir", func(t *testing.T) {
				m, err := LoadDir(ctx, dir, LoadOptions{})
				require.NoError(t, err)
				defer m.Close()
				result, _, err := m.Eval(ctx, "", `entrypoint.call("T", ("{\"a\":1}" :: JSON!), "f", ("[1]" :: JSON!), null)`)
				require.NoError(t, err)
				requireBuiltinJSON(t, result, entrypointCallRequest)
				// A snippet's own bare JSON is the builtin too.
				result, _, err = m.Eval(ctx, "", `let s: JSON! = ("{}" :: JSON!); entrypoint.fromSchema(entrypoint.toSchema(s))`)
				require.NoError(t, err)
				requireBuiltinJSON(t, result, "{}")
			})
		})
	}

	t.Run("unpinned", func(t *testing.T) {
		// The engine evaluates a copy of an entrypoint that no dang.toml
		// governs, pinning its version with ContextWithVersion.
		for _, v := range []Version{Version2_1, Version2_4} {
			ctx := ContextWithVersion(daggerJSONContext(t), v)
			env, err := RunDir(ctx, writeEntrypoint(t, ""), false)
			require.NoError(t, err)
			callEntrypoint(t, ctx, env)
		}
	})

	t.Run("REPL", func(t *testing.T) {
		schema, err := SchemaFromSDL(scalarJSONSchema, "schema.graphqls")
		require.NoError(t, err)
		typeScope, valueScope := BuildScopesFromImports("", []ImportConfig{{Name: "Dagger", Schema: schema, Client: &fakeClient{}}})
		named, found := typeScope.NamedType("JSON")
		require.True(t, found)
		require.Same(t, JSONModule, named, "bare JSON is the builtin scalar")

		src := entrypointContract + "\n" + entrypointCall
		parsed, err := ParseWithRecovery("repl", []byte(src))
		require.NoError(t, err)
		forms := parsed.(*FileBlock).Forms
		ctx := context.Background()
		_, err = InferTopLevel(ctx, forms, typeScope, hm.NewSimpleFresher())
		require.NoError(t, err)
		ctx = WithEvalContext(ctx, NewEvalContext("repl", src))
		for _, form := range forms {
			_, err := EvalNode(ctx, valueScope, form)
			require.NoError(t, err)
		}
		callEntrypoint(t, ctx, valueScope)
	})
}

func TestBuiltinJSONScalarValues(t *testing.T) {
	// A JSON! value is a ScalarValue of the builtin JSON scalar, a ScalarKind
	// *Type named JSON, in every version, as the Dagger engine requires.
	for _, version := range []string{"2.1", "2.4"} {
		t.Run(version, func(t *testing.T) {
			dir := writeDangProject(t, map[string]string{
				"dang.toml": "dang = \"" + version + "\"\n",
				"main.dang": "pub j: JSON! = (\"{}\" :: JSON!)\npub y: YAML! = (\"a: 1\" :: YAML!)\npub t: TOML! = (\"a = 1\" :: TOML!)\n",
			})
			ctx := daggerJSONContext(t)
			env, err := RunDir(ctx, dir, false)
			require.NoError(t, err)
			j, found := requireEvalGet(t, env, "j")
			require.True(t, found)
			requireBuiltinJSON(t, j, "{}")
			for name, mod := range map[string]*Type{"y": YAMLModule, "t": TOMLModule} {
				v, found := requireEvalGet(t, env, name)
				require.True(t, found)
				scalar, ok := v.(ScalarValue)
				require.True(t, ok, "%s is %T", name, v)
				require.Same(t, mod, scalar.ScalarType)
			}

			m, err := LoadDir(ctx, dir, LoadOptions{})
			require.NoError(t, err)
			defer m.Close()
			v, typ, err := m.Eval(ctx, "", `("{}" :: JSON!)`)
			require.NoError(t, err)
			require.Equal(t, NonNull(JSONModule), typ)
			requireBuiltinJSON(t, v, "{}")
		})
	}
}

func TestBuiltinCodecScalarsValidate(t *testing.T) {
	for text, cast := range map[string]string{
		`{`:       "JSON",
		`[1] [2]`: "JSON",
		`a: [`:    "YAML",
		`a = `:    "TOML",
		`x`:       "TOML",
	} {
		msg := "invalid " + cast

		// A string converting at runtime raises.
		src := "let s = " + strconv.Quote(text) + "\n(s :: " + cast + "!)"
		_, err := evalWithContext(t, context.Background(), src)
		require.ErrorContains(t, err, msg, src)

		// A literal is checked statically, as an enum literal is.
		src = "(" + strconv.Quote(text) + " :: " + cast + "!)"
		parsed, err := ParseWithRecovery("literal", []byte(src))
		require.NoError(t, err)
		typeScope, _ := BuildScopesFromImports("", nil)
		_, err = InferFormsWithPhases(context.Background(), parsed.(*FileBlock).Forms, typeScope, hm.NewSimpleFresher())
		require.ErrorContains(t, err, msg, src)
	}
}

func TestExplicitImportReplacesBuiltinCodecScalar(t *testing.T) {
	// An explicit import named like a builtin import replaces it, as a type
	// too, whatever types other imports bring in unqualified, before or after
	// it.
	body := "\npub n(d: JSON.Doc!): Int! { d.n }\n\nassert { JSON.x == 42 }\nassert { YAML.encode([1]) == \"- 1\" }\n"
	for _, imports := range []string{"import Schema\nimport JSON\n", "import JSON\nimport Schema\n"} {
		root := writeDangProject(t, map[string]string{
			"dang.toml":       "[imports.JSON]\npath = \"./json\"\n\n[imports.Schema]\nschema = \"./schema.graphqls\"\n",
			"schema.graphqls": scalarJSONSchema,
			"json/json.dang":  "pub x: Int! = 42\n\ntype Doc {\n  pub n: Int! = 1\n}\n",
			"main.dang":       imports + body,
		})
		_, err := RunDir(context.Background(), root, false)
		require.NoError(t, err)
		require.NoError(t, RunFile(context.Background(), filepath.Join(root, "main.dang"), false))
		_, err = DeclareDir(context.Background(), root, false)
		require.NoError(t, err)
	}
}
