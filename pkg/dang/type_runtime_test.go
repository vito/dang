package dang

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTypeRuntimeRecursiveStatics(t *testing.T) {
	require.NoError(t, evalExample(`
check(n: Int!): String! {
  type Local { self { stamp: String! = toString(n) } }
  let before = Local.stamp
  if (n > 0) { check(n - 1) }
  assert { Local.stamp == before }
  Local.stamp
}
assert { check(3) == "3" }
`))
}

func TestImportedScalarRuntimeCallbacks(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"dang.toml":       "[imports.Lib]\npath = \"./lib\"\n",
		"lib/dang.toml":   "[imports.Other]\npath = \"../other\"\n",
		"other/main.dang": `invoke(&f(raw: String!): a): a { f("!") }`,
		"lib/main.dang": `import Other
prefix: String! = "lib"
scalar Tag {
  new(raw: String!) { Other.invoke { ignored => prefix + raw } }
  label: String! { Tag.stamp + toString(self) }
  self { stamp: String! = "static" }
}
let ctor = &Tag
made: Tag! = Other.invoke { raw => ctor(raw) }
`,
		"main.dang": `import Lib
assert { Lib.made.label == "staticlib!" }
let direct = Lib.Tag("direct")
assert { direct.label == "staticlibdirect" }
let decoded: Lib.Tag! = JSON.decode("\"decoded\"")
assert { decoded.label == "staticlibdecoded" }
let literal: Lib.Tag! = "literal"
assert { literal.label == "staticlibliteral" }
assert { Tag.stamp == "static" }
`,
	}
	for name, source := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RunDir(ctx, dir, false)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("scalar callback waited on its declaring module's initialization")
	}
}
