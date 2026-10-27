package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

func nativeCacheTestModule(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	source := `type Thing { message: String! }
pub marker: String! = "` + marker + `"
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(source), 0o600))
	return dir
}

func inferNativeCacheImport(t *testing.T, ctx context.Context, decl *ImportDecl) ValueScope {
	t.Helper()
	env := NewPreludeTypeScope("test")
	_, err := decl.Infer(ctx, env, hm.NewSimpleFresher())
	require.NoError(t, err)
	return NewValueScope(env)
}

func TestNativeImportsReuseCompiledModulesAcrossAnalyses(t *testing.T) {
	dir := nativeCacheTestModule(t, "first")
	config := ImportConfig{Name: "Lib", DangModuleDir: dir}
	first := ContextWithImportConfigs(t.Context(), config)
	cached := &ImportDecl{Name: &Symbol{Name: "Lib"}}
	firstScope := inferNativeCacheImport(t, first, cached)
	module := cached.dangMod
	thing, ok := cached.inferred.NamedType("Thing")
	require.True(t, ok)
	firstValues, err := cached.Eval(first, firstScope)
	require.NoError(t, err)

	// The LSP supplies a fresh native cache while retaining sibling ASTs.
	// Normalization must recognize this spelling as the same directory.
	config.DangModuleDir = dir + string(filepath.Separator) + "."
	second := ContextWithImportConfigs(t.Context(), config)
	_, found := dangModuleCacheFromContext(second).Load(dir)
	require.False(t, found)
	secondScope := inferNativeCacheImport(t, second, cached)
	require.Same(t, module, cached.dangMod)
	secondThing, ok := cached.inferred.NamedType("Thing")
	require.True(t, ok)
	require.Same(t, thing, secondThing)
	secondValues, err := cached.Eval(second, secondScope)
	require.NoError(t, err)
	require.Same(t, firstValues, secondValues, "module initialization remains once-only")

	// A newly parsed sibling must adopt the module seeded by the cached node.
	fresh := &ImportDecl{Name: &Symbol{Name: "Lib"}}
	inferNativeCacheImport(t, second, fresh)
	require.Same(t, module, fresh.dangMod)
	freshThing, ok := fresh.inferred.NamedType("Thing")
	require.True(t, ok)
	require.Same(t, thing, freshThing)
}

func TestNativeCachedImportsHonorAnExistingAnalysisCache(t *testing.T) {
	dir := nativeCacheTestModule(t, "same")
	config := ImportConfig{Name: "Lib", DangModuleDir: dir}
	first := ContextWithImportConfigs(t.Context(), config)
	cached := &ImportDecl{Name: &Symbol{Name: "Lib"}}
	inferNativeCacheImport(t, first, cached)
	previous := cached.dangMod

	// If a freshly parsed file imports first, it establishes this pass's
	// identity. Cached nodes must use that winner rather than their old module.
	second := ContextWithImportConfigs(t.Context(), config)
	fresh := &ImportDecl{Name: &Symbol{Name: "Lib"}}
	inferNativeCacheImport(t, second, fresh)
	require.NotSame(t, previous, fresh.dangMod)
	inferNativeCacheImport(t, second, cached)
	require.Same(t, fresh.dangMod, cached.dangMod)
}

func TestNativeImportDirectoryChangesReloadTheModule(t *testing.T) {
	firstDir := nativeCacheTestModule(t, "first")
	secondDir := nativeCacheTestModule(t, "second")
	config := ImportConfig{Name: "Lib", DangModuleDir: firstDir}
	first := ContextWithImportConfigs(t.Context(), config)
	decl := &ImportDecl{Name: &Symbol{Name: "Lib"}}
	inferNativeCacheImport(t, first, decl)
	previous := decl.dangMod
	config.DangModuleDir = secondDir
	second := ContextWithImportConfigs(first, config)
	scope := inferNativeCacheImport(t, second, decl)
	require.NotSame(t, previous, decl.dangMod)
	require.Equal(t, secondDir, decl.dangMod.dir)
	values, err := decl.Eval(second, scope)
	require.NoError(t, err)
	marker, found, err := values.(ValueScope).Lookup(second, "marker")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StringValue{Val: "second"}, marker)
}

func TestNativeImportReuseStillRejectsCycles(t *testing.T) {
	dir := nativeCacheTestModule(t, "cycle")
	config := ImportConfig{Name: "Lib", DangModuleDir: dir}
	populated := ContextWithImportConfigs(t.Context(), config)
	decl := &ImportDecl{Name: &Symbol{Name: "Lib"}}
	inferNativeCacheImport(t, populated, decl)
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"cache hit", populated},
		{"reuse seed", ContextWithImportConfigs(t.Context(), config)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := pushCompileStack(tc.ctx, dir)
			_, err := decl.Infer(ctx, NewPreludeTypeScope("test"), hm.NewSimpleFresher())
			require.ErrorContains(t, err, "import cycle detected")
		})
	}
}
