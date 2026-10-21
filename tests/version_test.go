package tests

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
)

// TestVersions runs modules written for different Dang versions end to end.
// Every version shares one interpreter, so modules of different versions
// import each other freely; only parsing differs.
func (DangSuite) TestVersions(ctx context.Context, t *testctx.T) {
	writeFiles := func(root string, files map[string]string) {
		for name, contents := range files {
			path := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
		}
	}

	t.Run("Dang 1.0 module", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"dang.toml": "dang = \"1.0\"\n",
			"main.dang": `
type Pair {
  a: Int! = 1
  b: Int! = 2
}

let pair = Pair
let picked = pair.{a, b}
assert { picked.a == 1 }
assert { picked.b == 2 }
`,
		})
		_, err := dang.RunDir(ctx, root, false)
		require.NoError(t, err)
	})

	t.Run("Dang 2.0 syntax is rejected in a Dang 1.0 module", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"dang.toml": "dang = \"1.0\"\n",
			"main.dang": "inc(x: Int!): Int! { x + 1 }\nlet two = 1.{ inc(_) }\n",
		})
		_, err := dang.RunDir(ctx, root, false)
		require.Error(t, err)
	})

	t.Run("modules of different versions import each other", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"dang.toml": `dang = "1.0"

[imports.Old]
path = "./old"

[imports.New]
path = "./new"
`,
			// No dang.toml of its own: governed by the root's, so 1.0.
			"old/old.dang": `
type Point {
  x: Int! = 1
}

let p = Point
selectedX: Int! = p.{x}.x
`,
			// Its own dang.toml declares 2.0.
			"new/dang.toml": "dang = \"2.0\"\n",
			"new/new.dang": `
inc(x: Int!): Int! { x + 1 }

type Point {
  x: Int! = 1
}

let p = Point
piped: Int! = p.{{x}}.x.{ inc(_) }
`,
			"main.dang": `
import Old
import New

assert { Old.selectedX == 1 }
assert { New.piped == 2 }
`,
		})
		_, err := dang.RunDir(ctx, root, false)
		require.NoError(t, err)
	})

	t.Run("version comes from the source's path, not its importer", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		// Mark the dir as a repo root so dang.toml discovery stops here.
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		writeFiles(root, map[string]string{
			"app/dang.toml": `dang = "1.0"

[imports.Lib]
path = "../lib"
`,
			"app/main.dang": `
import Lib

type Pair {
  a: Int! = 1
}

let pair = Pair
assert { pair.{a}.a == 1 }
assert { Lib.piped == 2 }
`,
			// Outside app/ and governed by no dang.toml, so the current
			// version, even though a 1.0 module imports it.
			"lib/lib.dang": `
inc(x: Int!): Int! { x + 1 }

type Point {
  x: Int! = 1
}

let p = Point
piped: Int! = p.{{x}}.x.{ inc(_) }
`,
		})
		_, err := dang.RunDir(ctx, filepath.Join(root, "app"), false)
		require.NoError(t, err)

		// An embedder's (or the REPL's) version doesn't reach imports either.
		_, err = dang.RunDir(dang.ContextWithVersion(ctx, dang.Version1_0), filepath.Join(root, "app"), false)
		require.NoError(t, err)

		version, err := dang.VersionForDir(ctx, filepath.Join(root, "lib"))
		require.NoError(t, err)
		require.Equal(t, dang.CurrentVersion, version)
	})

	t.Run("dang.toml version applies with embedder-provided imports", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		writeFiles(root, map[string]string{
			"app/dang.toml": "dang = \"1.0\"\n",
			"app/main.dang": `
type Pair {
  a: Int! = 1
}

let pair = Pair
assert { pair.{a}.a == 1 }
assert { Lib.one == 1 }
`,
			"lib/lib.dang": "pub one: Int! = 1\n",
		})
		// An embedder (e.g. the Dagger SDK) installing its own imports
		// means dang.toml's imports are never loaded; its version still
		// applies.
		ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
			Name:          "Lib",
			DangModuleDir: filepath.Join(root, "lib"),
			AutoImport:    true,
		})
		_, err := dang.RunDir(ctx, filepath.Join(root, "app"), false)
		require.NoError(t, err)
	})

	t.Run("embedder version applies without a dang.toml", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		// Mark the dir as a repo root so dang.toml discovery stops here.
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		writeFiles(root, map[string]string{
			"main.dang": `
type Pair {
  a: Int! = 1
}

let pair = Pair
assert { pair.{a}.a == 1 }
`,
		})
		_, err := dang.RunDir(dang.ContextWithVersion(ctx, dang.Version1_0), root, false)
		require.NoError(t, err)

		// Without the embedder's version, the current version rejects it.
		_, err = dang.RunDir(ctx, root, false)
		require.Error(t, err)

		// A dang.toml without a `dang` key (e.g. one for imports, from
		// before the key existed) leaves the version to the embedder...
		writeFiles(root, map[string]string{"dang.toml": ""})
		_, err = dang.RunDir(dang.ContextWithVersion(ctx, dang.Version1_0), root, false)
		require.NoError(t, err)

		// ...but a version it declares always wins over the embedder's.
		writeFiles(root, map[string]string{"dang.toml": "dang = \"2.1\"\n"})
		_, err = dang.RunDir(dang.ContextWithVersion(ctx, dang.Version1_0), root, false)
		require.Error(t, err)
	})

	t.Run("embedder version covers path imports under the same dang.toml", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		const v1Source = `
type Pair {
  a: Int! = 1
}

let pair = Pair
pub picked: Int! = pair.{a}.a
`
		writeFiles(root, map[string]string{
			// From before the `dang` key, for its imports only.
			"dang.toml": "[imports.Lib]\npath = \"./lib\"\n",
			"main.dang": "import Lib\n" + v1Source + "assert { Lib.picked == 1 }\n",
			// Governed by the same dang.toml, so pinned the same.
			"lib/lib.dang": v1Source,
		})
		ctx = dang.ContextWithVersion(ctx, dang.Version1_0)
		_, err := dang.RunDir(ctx, root, false)
		require.NoError(t, err)

		// A module with a dang.toml of its own is another project, which
		// the pin doesn't reach.
		writeFiles(root, map[string]string{"lib/dang.toml": ""})
		_, err = dang.RunDir(ctx, root, false)
		require.ErrorContains(t, err, "lib.dang")
	})
}
