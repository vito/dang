package dang

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// scalarJSONSchema defines a scalar named like the builtin JSON import, as
// Dagger's schema does.
const scalarJSONSchema = `
type Query {
  echo(value: JSON!): JSON!
}

scalar JSON
`

// entrypointCall has the shape of the `call` function the Dagger Go SDK
// generates for a Dang entrypoint (written for Dang 2.1): its bare `JSON`
// types are Dagger's scalar, and its `JSON.encode` is the builtin namespace.
const entrypointCall = `pub call(receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!, callDigest: String): JSON! {
  let request = JSON.encode({{
    receiverType: receiverType,
    receiverValue: receiverValue,
    fnName: fnName,
    fnArgs: fnArgs,
  }})
  (request :: JSON!)
}
`

// entrypointCallRequest is what entrypointCall returns for receiverValue
// {"a":1} and fnArgs [1], as JSON values (see entrypointArgs).
const entrypointCallRequest = `{"receiverType":"T","receiverValue":{"a":1},"fnName":"f","fnArgs":[1]}`

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

// writeEntrypoint writes an entrypoint module of the given Dang version
// ("" declares none) whose main.dang is source.
func writeEntrypoint(t *testing.T, version, source string) string {
	t.Helper()
	files := map[string]string{"main.dang": source}
	if version != "" {
		files["dang.toml"] = "dang = \"" + version + "\"\n"
	}
	return writeDangProject(t, files)
}

// entrypointArgs are the arguments the Dagger engine calls entrypointCall
// with: JSON values as Dagger's JSON scalar.
func entrypointArgs() map[string]Value {
	return map[string]Value{
		"receiverType":  StringValue{Val: "T"},
		"receiverValue": ScalarValue{Val: `{"a":1}`, Encoded: true},
		"fnName":        StringValue{Val: "f"},
		"fnArgs":        ScalarValue{Val: `[1]`, Encoded: true},
	}
}

// requireEntrypointResult checks call's result as the Dagger engine reads it
// (core/sdk/dang/v2 entrypointJSONResult): a scalar named JSON holding the
// request.
func requireEntrypointResult(t *testing.T, result Value) {
	t.Helper()
	scalar, ok := result.(ScalarValue)
	require.True(t, ok, "call returned %T", result)
	require.NotNil(t, scalar.ScalarType)
	require.Equal(t, "JSON", scalar.ScalarType.Name())
	require.JSONEq(t, entrypointCallRequest, scalar.Val)
}

func callEntrypoint(t *testing.T, ctx context.Context, env ValueScope) Value {
	t.Helper()
	fn, found := requireEvalGet(t, env, "call")
	require.True(t, found)
	callable, ok := fn.(Callable)
	require.True(t, ok, "call is %T", fn)
	result, err := callable.Call(ctx, env, entrypointArgs())
	require.NoError(t, err)
	return result
}

// requireReturnsImportedJSON checks that env's call function returns the
// imported JSON scalar, not the builtin JSON namespace.
func requireReturnsImportedJSON(t *testing.T, env ValueScope) {
	t.Helper()
	fn, found := requireEvalGet(t, env, "call")
	require.True(t, found)
	fnType := fn.Type()
	if nn, ok := fnType.(hm.NonNullType); ok {
		fnType = nn.Type
	}
	ft, ok := fnType.(*hm.FunctionType)
	require.True(t, ok, "call has type %T", fnType)
	ret := ft.ReturnType()
	if nn, ok := ret.(hm.NonNullType); ok {
		ret = nn.Type
	}
	scalar, ok := ret.(*Type)
	require.True(t, ok, "call returns %T", ret)
	require.NotSame(t, JSONModule, scalar)
	require.Equal(t, ScalarKind, scalar.Kind)
	require.Equal(t, "JSON", scalar.Name())
}

func TestImportTypesOverBuiltinImportsBefore2_4(t *testing.T) {
	// Before 2.4, a bare type named like a builtin import is the type an
	// import brings in unqualified, as in Dagger entrypoints, while the value
	// stays the builtin namespace.
	source := entrypointCall + `
pub same(x: JSON!): Dagger.JSON! { x }
pub back(x: Dagger.JSON!): JSON! { x }
pub fromString(s: String!): JSON! { (s :: JSON!) }

assert { JSON.encode([1]) == "[1]" }
assert { JSON.encode(fromString("{}")) == "\"{}\"" }
`
	for _, version := range []string{"2.1", "2.0"} {
		t.Run(version, func(t *testing.T) {
			ctx := daggerJSONContext(t)
			dir := writeEntrypoint(t, version, source)

			env, err := RunDir(ctx, dir, false)
			require.NoError(t, err)
			requireEntrypointResult(t, callEntrypoint(t, ctx, env))

			require.NoError(t, RunFile(ctx, filepath.Join(dir, "main.dang"), false))

			env, err = DeclareDir(ctx, dir, false)
			require.NoError(t, err)
			// DeclareDir checks no bodies, so check the signature.
			requireReturnsImportedJSON(t, env)

			m, err := LoadDir(ctx, dir, LoadOptions{})
			require.NoError(t, err)
			defer m.Close()
			result, _, err := m.Eval(ctx, "", `call("T", ("{\"a\":1}" :: JSON!), "f", ("[1]" :: JSON!), null)`)
			require.NoError(t, err)
			scalar, ok := result.(ScalarValue)
			require.True(t, ok, "call returned %T", result)
			require.Equal(t, "JSON", scalar.ScalarType.Name())
			// A snippet's own bare JSON is the import's type too.
			_, _, err = m.Eval(ctx, "", `let s: JSON! = ("{}" :: JSON!); same(s)`)
			require.NoError(t, err)
		})
	}
}

func TestBuiltinImportDirectivesWhereImportTypesWin(t *testing.T) {
	// A directive's scope names a namespace, as the value of its name does,
	// so @JSON.field is the builtin's though a bare JSON type is Dagger's.
	source := `type User {
  displayName: String! @JSON.field(name: "display_name")
  raw: JSON
}

assert { JSON.encode(User("Ada", null)) == "{\"display_name\":\"Ada\",\"raw\":null}" }
`
	for _, version := range []string{"2.1", "2.4"} {
		t.Run(version, func(t *testing.T) {
			src := source
			if version == "2.4" {
				src = strings.ReplaceAll(src, "raw: JSON", "raw: Dagger.JSON")
			}
			ctx := daggerJSONContext(t)
			dir := writeEntrypoint(t, version, src)
			_, err := RunDir(ctx, dir, false)
			require.NoError(t, err)
			require.NoError(t, RunFile(ctx, filepath.Join(dir, "main.dang"), false))
		})
	}
}

func TestImportTypesOverBuiltinImportsUnpinned(t *testing.T) {
	// The Dagger engine evaluates a copy of an entrypoint that no dang.toml
	// governs, pinning its version with ContextWithVersion.
	ctx := ContextWithVersion(daggerJSONContext(t), Version2_1)
	dir := writeEntrypoint(t, "", entrypointCall)
	env, err := RunDir(ctx, dir, false)
	require.NoError(t, err)
	requireEntrypointResult(t, callEntrypoint(t, ctx, env))
}

func TestBuiltinImportTypesFrom2_4(t *testing.T) {
	// From 2.4, the builtin import's namespace wins in type position too, so
	// the import's type is spelled qualified.
	for _, version := range []string{"2.4", ""} {
		t.Run(version, func(t *testing.T) {
			ctx := daggerJSONContext(t)
			dir := writeEntrypoint(t, version, entrypointCall)
			_, err := RunDir(ctx, dir, false)
			require.ErrorContains(t, err, "type hint mismatch: expression has type String!, but hint expects JSON!")
			err = RunFile(ctx, filepath.Join(dir, "main.dang"), false)
			require.ErrorContains(t, err, "type hint mismatch: expression has type String!, but hint expects JSON!")

			qualified := `pub call(receiverType: String!, receiverValue: Dagger.JSON, fnName: String!, fnArgs: Dagger.JSON!, callDigest: String): Dagger.JSON! {
  let request = JSON.encode({{
    receiverType: receiverType,
    receiverValue: receiverValue,
    fnName: fnName,
    fnArgs: fnArgs,
  }})
  (request :: Dagger.JSON!)
}
`
			dir = writeEntrypoint(t, version, qualified)
			env, err := RunDir(ctx, dir, false)
			require.NoError(t, err)
			requireEntrypointResult(t, callEntrypoint(t, ctx, env))
		})
	}
}

func TestImportTypesOverBuiltinImportsPerFile(t *testing.T) {
	// The rule follows each file's own version: a Dang module written for
	// 2.1 gets its import's type, though its importer is written for 2.4.
	root := writeDangProject(t, map[string]string{
		"dang.toml":       "dang = \"2.4\"\n\n[imports.Lib]\npath = \"./lib\"\n",
		"schema.graphqls": scalarJSONSchema,
		"lib/dang.toml":   "dang = \"2.1\"\n\n[imports.Schema]\nschema = \"../schema.graphqls\"\n",
		"lib/lib.dang":    "import Schema\n\npub wrap(s: String!): JSON! { (s :: JSON!) }\n",
		"main.dang":       "import Lib\n\npub viaLib: String! = JSON.encode(Lib.wrap(\"x\"))\n\nassert { viaLib == \"\\\"x\\\"\" }\n",
	})
	_, err := RunDir(context.Background(), root, false)
	require.NoError(t, err)
	require.NoError(t, RunFile(context.Background(), filepath.Join(root, "main.dang"), false))
}

func TestExplicitImportReplacesBuiltinImportInEveryVersion(t *testing.T) {
	// An explicit import named like a builtin import replaces it, in type
	// position too, whatever the version and whatever types other imports
	// bring in unqualified, before or after it.
	sdl := scalarJSONSchema + "\nscalar YAML\n"
	body := "\npub n(d: YAML.Doc!): Int! { d.n }\n\nassert { YAML.x == 42 }\nassert { JSON.encode([1]) == \"[1]\" }\n"
	for _, version := range []string{"2.1", "2.4"} {
		for _, imports := range []string{"import Schema\nimport YAML\n", "import YAML\nimport Schema\n"} {
			t.Run(version, func(t *testing.T) {
				root := writeDangProject(t, map[string]string{
					"dang.toml":       "dang = \"" + version + "\"\n\n[imports.YAML]\npath = \"./yaml\"\n\n[imports.Schema]\nschema = \"./schema.graphqls\"\n",
					"schema.graphqls": sdl,
					"yaml/yaml.dang":  "pub x: Int! = 42\n\ntype Doc {\n  pub n: Int! = 1\n}\n",
					"main.dang":       imports + body,
				})
				_, err := RunDir(context.Background(), root, false)
				require.NoError(t, err)
				require.NoError(t, RunFile(context.Background(), filepath.Join(root, "main.dang"), false))
				_, err = DeclareDir(context.Background(), root, false)
				require.NoError(t, err)
			})
		}
	}
}

func TestBuildScopesFromImportsForVersion(t *testing.T) {
	// The REPL's scopes follow its version, as files do.
	schema, err := SchemaFromSDL(scalarJSONSchema, "schema.graphqls")
	require.NoError(t, err)
	configs := []ImportConfig{{Name: "Dagger", Schema: schema}}

	types, _ := BuildScopesFromImportsForVersion("", configs, Version2_1)
	named, found := types.NamedType("JSON")
	require.True(t, found)
	require.NotSame(t, JSONModule, named)
	require.Equal(t, ScalarKind, named.(*Type).Kind)
	scheme, found := types.SchemeOf("JSON")
	require.True(t, found)
	valueType, _ := scheme.Type()
	require.Equal(t, NonNull(JSONModule), valueType, "the value is the namespace")

	types, _ = BuildScopesFromImportsForVersion("", configs, Version2_4)
	named, found = types.NamedType("JSON")
	require.True(t, found)
	require.Same(t, JSONModule, named)
}
