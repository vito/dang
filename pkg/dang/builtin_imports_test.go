package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

func TestBuiltinImportsAreQualifiedImports(t *testing.T) {
	scope := NewPreludeTypeScope("")
	imports, ok := scope.lexical.(*OverlayTypeScope)
	require.True(t, ok)

	for _, mod := range []*Type{JSONModule, YAMLModule, TOMLModule, RandomModule, UUIDModule} {
		// Below the module's own frame, so its declarations shadow them.
		_, local := scope.LocalNamedType(mod.Named)
		require.False(t, local, mod.Named)

		named, found := scope.NamedType(mod.Named)
		require.True(t, found, mod.Named)
		require.Same(t, mod, named)

		origin, found := imports.LocalTypeOrigin(mod.Named)
		require.True(t, found, mod.Named)
		require.Equal(t, ImportedBindingOrigin(mod.Named, true), origin)
		origin, found = imports.LocalValueOrigin(mod.Named)
		require.True(t, found, mod.Named)
		require.Equal(t, ImportedBindingOrigin(mod.Named, true), origin)

		// Not part of the Prelude.
		_, found = Prelude.NamedType(mod.Named)
		require.False(t, found, mod.Named)
	}

	// Qualified only.
	_, found := scope.SchemeOf("encode")
	require.False(t, found)
}

func TestAutoImportReplacesBuiltinImport(t *testing.T) {
	// An auto-imported config named like a builtin import replaces it, in
	// every file, as an explicit import would.
	schema, err := SchemaFromSDL(fakeSchema, "fake.graphqls")
	require.NoError(t, err)
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{
		Name:       "YAML",
		Schema:     schema,
		Client:     &fakeClient{},
		AutoImport: true,
	})
	dir := writeModule(t, map[string]string{
		"main.dang": "assert { YAML.engineVersion == \"v1\" }\n",
	})

	_, err = RunDir(ctx, dir, false)
	require.NoError(t, err)
	require.NoError(t, RunFile(ctx, filepath.Join(dir, "main.dang"), false))

	m, err := LoadDir(ctx, dir, LoadOptions{})
	require.NoError(t, err)
	defer m.Close()
	val, _, err := m.Eval(ctx, "", "YAML.engineVersion")
	require.NoError(t, err)
	require.Equal(t, StringValue{Val: "v1"}, val)
}

func TestDeclareDirImportsAreFileLocal(t *testing.T) {
	// Under DeclareDir, as under RunDir, a file's imports are its own: a
	// function declared in a file that imports YAML (a local module) gets
	// the module, and one in a sibling file gets the builtin YAML.
	root := t.TempDir()
	for name, source := range map[string]string{
		".git/HEAD":      "",
		"dang.toml":      "[imports.YAML]\npath = \"./yaml\"\n",
		"yaml/yaml.dang": "x: Int! = 42\n",
		"a.dang":         "import YAML\n\npub fromModule: Int! { YAML.x }\n",
		"b.dang":         "pub fromBuiltin: String! { YAML.encode([1]) }\n",
	} {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	}
	ctx := context.Background()

	env, err := DeclareDir(ctx, root, false)
	require.NoError(t, err)

	_, found := requireEvalGet(t, env, "YAML")
	require.False(t, found, "neither import is the module's own")

	call := func(name string) Value {
		t.Helper()
		fn, found := requireEvalGet(t, env, name)
		require.True(t, found)
		callable, ok := fn.(Callable)
		require.True(t, ok, "%s is %T", name, fn)
		val, err := callable.Call(ctx, env, map[string]Value{})
		require.NoError(t, err)
		return val
	}
	require.Equal(t, IntValue{Val: 42}, call("fromModule"))
	require.Equal(t, StringValue{Val: "- 1"}, call("fromBuiltin"))
}

func TestUnqualifiedImportKeepsBuiltinImport(t *testing.T) {
	lib := NewType("Lib", ObjectKind)
	libJSON := NewType("JSON", ScalarKind)
	lib.AddObject("JSON", libJSON)
	lib.Add("JSON", hm.NewScheme(nil, NonNull(libJSON)))
	lib.SetVisibility("JSON", PublicVisibility)

	scope := NewPreludeTypeScope("")
	installImportedTypeScope(scope, "Lib", lib, nil)

	named, found := scope.NamedType("JSON")
	require.True(t, found)
	require.Same(t, JSONModule, named)
	scheme, found := scope.SchemeOf("JSON")
	require.True(t, found)
	jsonType, _ := scheme.Type()
	require.Equal(t, NonNull(JSONModule), jsonType)

	libVal := NewObject(lib)
	libVal.Bind("JSON", StringValue{Val: "lib"}, PublicVisibility)
	values := NewValueScope(scope)
	installImportedValueScope(values, "Lib", libVal, nil)

	val, found, err := values.Lookup(context.Background(), "JSON")
	require.NoError(t, err)
	require.True(t, found)
	obj, ok := val.(*Object)
	require.True(t, ok, "JSON is %T", val)
	require.Same(t, JSONModule, obj.Mod)
}
