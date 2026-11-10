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

	for _, version := range []dang.Version{dang.Version1_0, dang.Version2_0} {
		t.Run("Dang "+version.String()+" for loops call the builtin despite a loop of the module's own", func(ctx context.Context, t *testctx.T) {
			root := t.TempDir()
			writeFiles(root, map[string]string{
				"dang.toml": "dang = \"" + version.String() + "\"\n",
				"loop.dang": "pub loop(n: Int!): Int! { n }\n",
				"main.dang": `
count(): Int! {
  let i = 0
  for (i < 3) {
    i += 1
  }
  i
}

assert { count() == 3 }
assert { loop(2) == 2 }
`,
			})
			_, err := dang.RunDir(ctx, root, false)
			require.NoError(t, err)
		})
	}

	t.Run("an unqualified import does not shadow a builtin", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		writeFiles(root, map[string]string{
			"dang.toml": "[imports.Lib]\npath = \"./lib\"\n",
			"lib/lib.dang": `
pub toString(n: Int!): Int! { 7 }
pub seven: Int! = 7
`,
			// The import's other names are unqualified; the builtin's name
			// is the builtin, in type checking and evaluation alike, and
			// the import's is still reachable qualified.
			"main.dang": `
import Lib
assert { seven == 7 }
assert { toString(1) == "1" }
assert { Lib.toString(1) == 7 }
`,
		})
		_, err := dang.RunDir(ctx, root, false)
		require.NoError(t, err)
		require.NoError(t, dang.RunFile(ctx, filepath.Join(root, "main.dang"), false))

		// Type checking agrees: the unqualified name has the builtin's type.
		writeFiles(root, map[string]string{
			"main.dang": "import Lib\nlet n: Int! = toString(1)\n",
		})
		_, err = dang.RunDir(ctx, root, false)
		require.ErrorContains(t, err, "String!")
		require.ErrorContains(t, dang.RunFile(ctx, filepath.Join(root, "main.dang"), false), "String!")
	})

	t.Run("declarations shadow the builtin imports", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"main.dang": `
type JSON {
  x: Int! = 1
}
let Random = 2
assert { JSON().x == 1 }
assert { Random == 2 }
assert { YAML.encode(1) == "1" }
`,
		})
		_, err := dang.RunDir(ctx, root, false)
		require.NoError(t, err)
		require.NoError(t, dang.RunFile(ctx, filepath.Join(root, "main.dang"), false))
	})

	t.Run("the builtin imports are qualified only", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		writeFiles(root, map[string]string{
			"main.dang": "encode([1])\n",
		})
		_, err := dang.RunDir(ctx, root, false)
		require.ErrorContains(t, err, `"encode" not found`)
	})

	t.Run("an unqualified import does not override a builtin import", func(ctx context.Context, t *testctx.T) {
		root := t.TempDir()
		require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
		writeFiles(root, map[string]string{
			"dang.toml": "[imports.Lib]\npath = \"./lib\"\n\n[imports.Other]\npath = \"./other\"\n",
			"lib/lib.dang": `
JSON: Int! = 7
type YAML {
  x: Int! = 1
}
`,
			"other/other.dang": "Lib: Int! = 3\nJSON: Int! = 8\nenum Format { TOML UUID }\n",
			// The imports' JSON and YAML are reachable qualified only; the
			// unqualified names stay the builtin imports, in type checking
			// and evaluation alike, at the top level and in a block (a
			// record field's included), and two imports both exporting one
			// aren't ambiguous; nor do an import's enum values override
			// them. The same goes for any import's namespace: Other's Lib
			// doesn't override the Lib imported around it.
			"main.dang": `
import Lib
import Other
assert { JSON.encode([1]) == "[1]" }
assert { YAML.encode(1) == "1" }
assert { TOML.encode({{a: 1}}) == "a = 1" }
assert { UUID.v4.length == 36 }
assert { Other.Format.TOML != Other.Format.UUID }
assert { Lib.JSON == 7 }
assert { Other.JSON == 8 }
assert { Lib.YAML().x == 1 }
let inBlock = if (true) {
  import Lib
  JSON.encode([2])
} else {
  ""
}
assert { inBlock == "[2]" }
let inRecord = {{x: if (true) {
  import Lib
  JSON.encode([3])
} else {
  ""
}}}
assert { inRecord.x == "[3]" }
let otherInBlock = if (true) {
  import Other
  Lib.JSON + Other.Lib
} else {
  0
}
assert { otherInBlock == 10 }
`,
		})
		_, err := dang.RunDir(ctx, root, false)
		require.NoError(t, err)
		require.NoError(t, dang.RunFile(ctx, filepath.Join(root, "main.dang"), false))
	})

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
