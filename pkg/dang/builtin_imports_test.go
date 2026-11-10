package dang

import (
	"context"
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
