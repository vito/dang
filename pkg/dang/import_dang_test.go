package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A native module's namespace holds only the module's own public
// declarations, at runtime as in type checking: the builtins and prelude its
// own code sees are not reachable through it, nor are its `let`s.
func TestDangModuleExportsOnlyItsDeclarations(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`
pub v: Int! = 42
pub text: String! = toString(v)
pub p: String! = Path("a/b").string
let hidden: String! = "h"
`), 0o600))
	ctx := context.Background()
	mod, err := loadDangModule(ctx, "Lib", dir)
	require.NoError(t, err)

	// Type checking: only the module's own names.
	for _, name := range []string{"v", "text", "p"} {
		_, found := mod.exportsType.SchemeOf(name)
		require.True(t, found, name)
	}
	for _, name := range []string{"toString", "print", "Path", "Random", "JSON"} {
		_, found := mod.exportsType.SchemeOf(name)
		require.False(t, found, name)
		_, found = mod.exportsType.NamedType(name)
		require.False(t, found, name)
	}

	// Runtime: the same, with the module's own code still using the prelude.
	exports, err := mod.exportsScope(ctx)
	require.NoError(t, err)
	for name, want := range map[string]Value{
		"v":    IntValue{Val: 42},
		"text": StringValue{Val: "42"},
		"p":    StringValue{Val: "a/b"},
	} {
		got, found, err := exports.Lookup(ctx, name)
		require.NoError(t, err)
		require.True(t, found, name)
		require.Equal(t, want, got, name)
	}
	for _, name := range []string{"toString", "print", "Path", "Random", "JSON", "hidden"} {
		_, found, err := exports.Lookup(ctx, name)
		require.NoError(t, err)
		require.False(t, found, name)
	}
}

// Two modules with the same public shape are still different imports: their
// namespaces don't unify like records.
func TestDangModuleNamespacesKeepTheirIdentity(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"lib", "lib2"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, name), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name, "main.dang"), []byte("pub v: Int! = 1\n"), 0o600))
	}
	ctx := ContextWithImportConfigs(context.Background(),
		ImportConfig{Name: "Lib", DangModuleDir: filepath.Join(root, "lib")},
		ImportConfig{Name: "Lib2", DangModuleDir: filepath.Join(root, "lib2")},
	)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte("import Lib\nimport Lib2\nlet same = Lib == Lib2\n"), 0o600))
	_, err := RunDir(ctx, dir, false)
	require.ErrorContains(t, err, "cannot compare Lib! with Lib2!")
}
