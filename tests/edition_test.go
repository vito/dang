package tests

import (
	"context"
	"os"
	"path/filepath"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
)

// TestEditions runs modules written in different editions end to end. Every
// edition shares one interpreter, so modules of different editions import each
// other freely; only parsing differs.
func (DangSuite) TestEditions(ctx context.Context, t *testctx.T) {
	writeFiles := func(root string, files map[string]string) {
		for name, contents := range files {
			path := filepath.Join(root, name)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
		}
	}

	t.Run("edition 1 module", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"dang.toml": "edition = \"1\"\n",
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

	t.Run("edition 2 syntax is rejected in an edition 1 module", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"dang.toml": "edition = \"1\"\n",
			"main.dang": "inc(x: Int!): Int! { x + 1 }\nlet two = 1.{ inc(_) }\n",
		})
		_, err := dang.RunDir(ctx, root, false)
		require.Error(t, err)
	})

	t.Run("modules of different editions import each other", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"dang.toml": `edition = "1"

[imports.Old]
path = "./old"

[imports.New]
path = "./new"
`,
			// No dang.toml of its own: governed by the root's, so edition 1.
			"old/old.dang": `
type Point {
  x: Int! = 1
}

let p = Point
selectedX: Int! = p.{x}.x
`,
			// Its own dang.toml declares edition 2.
			"new/dang.toml": "edition = \"2\"\n",
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

	t.Run("edition comes from the source's path, not its importer", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		// Mark the dir as a repo root so dang.toml discovery stops here.
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		writeFiles(root, map[string]string{
			"app/dang.toml": `edition = "1"

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
			// Outside app/ and governed by no dang.toml, so the latest
			// edition, even though an edition 1 module imports it.
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

		edition, err := dang.EditionForDir(ctx, filepath.Join(root, "lib"))
		require.NoError(t, err)
		require.Equal(t, dang.LatestEdition, edition)
	})

	t.Run("dang.toml edition applies with embedder-provided imports", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		writeFiles(root, map[string]string{
			"app/dang.toml": "edition = \"1\"\n",
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
		// means dang.toml's imports are never loaded; its edition still
		// applies.
		ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
			Name:          "Lib",
			DangModuleDir: filepath.Join(root, "lib"),
			AutoImport:    true,
		})
		_, err := dang.RunDir(ctx, filepath.Join(root, "app"), false)
		require.NoError(t, err)
	})

	t.Run("embedder edition applies without a dang.toml", func(ctx context.Context, t *testctx.T) {
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
		_, err := dang.RunDir(dang.ContextWithEdition(ctx, dang.Edition1), root, false)
		require.NoError(t, err)

		// Without the embedder's edition, the latest edition rejects it.
		_, err = dang.RunDir(ctx, root, false)
		require.Error(t, err)

		// A dang.toml always wins over the embedder's edition, including
		// its implicit default.
		writeFiles(root, map[string]string{"dang.toml": ""})
		_, err = dang.RunDir(dang.ContextWithEdition(ctx, dang.Edition1), root, false)
		require.Error(t, err)
	})
}
