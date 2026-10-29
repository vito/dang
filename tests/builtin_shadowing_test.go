package tests

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
)

// TestBuiltinShadowing checks that a module's own declarations shadow the
// builtins and the prelude, as any outer binding, in every Dang version and
// in every way a directory of source is evaluated.
func (DangSuite) TestBuiltinShadowing(ctx context.Context, t *testctx.T) {
	writeFiles := func(root string, files map[string]string) {
		for name, contents := range files {
			path := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
		}
	}

	for _, version := range []dang.Version{dang.Version1_0, dang.Version2_0, dang.Version2_1} {
		toml := "dang = \"" + version.String() + "\"\n"

		t.Run("Dang "+version.String()+" let shadows a builtin", func(ctx context.Context, t *testctx.T) {
			root := t.TempDir()
			writeFiles(root, map[string]string{
				"dang.toml": toml,
				"main.dang": `
let loop = 1
let toString = "mine"
assert { loop == 1 }
assert { toString == "mine" }
`,
			})
			_, err := dang.RunDir(ctx, root, false)
			require.NoError(t, err)
		})

		t.Run("Dang "+version.String()+" function shadows a builtin", func(ctx context.Context, t *testctx.T) {
			root := t.TempDir()
			writeFiles(root, map[string]string{
				"dang.toml": toml,
				"main.dang": `
pub loop(n: Int!): Int! { n }
assert { loop(2) == 2 }
`,
			})
			_, err := dang.RunDir(ctx, root, false)
			require.NoError(t, err)
		})

		t.Run("Dang "+version.String()+" a sibling file's declaration shadows a builtin", func(ctx context.Context, t *testctx.T) {
			root := t.TempDir()
			writeFiles(root, map[string]string{
				"dang.toml": toml,
				"a.dang":    "pub loop(n: Int!): Int! { n + 1 }\n",
				"b.dang":    "assert { loop(2) == 3 }\n",
			})
			_, err := dang.RunDir(ctx, root, false)
			require.NoError(t, err)
		})

		t.Run("Dang "+version.String()+" an imported module exports its own builtin names", func(ctx context.Context, t *testctx.T) {
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
			writeFiles(root, map[string]string{
				"dang.toml":    toml + "\n[imports.Lib]\npath = \"./lib\"\n",
				"lib/lib.dang": "pub loop(n: Int!): Int! { n * 2 }\n",
				"main.dang": `
import Lib
assert { Lib.loop(2) == 4 }
`,
			})
			_, err := dang.RunDir(ctx, root, false)
			require.NoError(t, err)
		})
	}

	t.Run("declared functions shadow builtins", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"main.dang": "pub loop(n: Int!): Int! { n }\n",
		})
		scope, err := dang.DeclareDir(ctx, root, false)
		require.NoError(t, err)
		val, found, err := scope.Lookup(ctx, "loop")
		require.NoError(t, err)
		require.True(t, found)
		require.IsType(t, dang.FunctionValue{}, val)
	})
}
