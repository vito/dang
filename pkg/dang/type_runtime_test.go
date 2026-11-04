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

func TestLocalScalarRuntimeOwnership(t *testing.T) {
	require.NoError(t, evalExample(`
check(n: Int!): String! {
  scalar Label {
    new(raw: String!) { toString(n) + raw }
    label: String! { toString(n) + toString(self) }
    self { stamp: Int! = n }
  }
  let first = Label("!")
  if (n > 0) { check(n - 1) }
  assert { Label.stamp == n }
  assert { first.label == toString(n) + toString(n) + "!" }
  Label("!").label
}
assert { check(3) == "33!" }
let labels = ["a", "b"].map { prefix =>
  scalar Label {
    new(raw: String!) { prefix + raw }
    label: String! { prefix + toString(self) }
  }
  let value = Label("!")
  prefix += "next"
  value
}
assert { labels[0]!.label == "anexta!" }
assert { labels[1]!.label == "bnextb!" }
let ctors = ["a", "b"].map { prefix =>
  scalar Label {
    new(raw: String!) { prefix + raw }
    label: String! { prefix + toString(self) }
  }
  &Label
}
let first = ctors[0]!
let second = ctors[1]!
assert { first("!").label == "aa!" }
assert { second("!").label == "bb!" }
# A methods-only value also keeps its own declaring environment.
let plain = ["a", "b"].map { prefix =>
  scalar Label { label: String! { prefix + toString(self) } }
  "!" :: Label!
}
assert { plain[0]!.label == "a!" }
assert { plain[1]!.label == "b!" }
`))
}

func TestLocalScalarMaterializationScope(t *testing.T) {
	const declarations = `type Outer {
  prefix: String!
  scalar Tag {
    new(raw: String!) { prefix + raw }
    label: String! { prefix }
  }
  make: Tag! { Tag("!") }
}
let x = Outer("A").make
`
	for _, statement := range []string{
		`x = "!"`,
		`let y = "!" :: Outer.Tag!`,
		`if (true) { let y: Outer.Tag! = "!" }`,
		`let y: [Outer.Tag!]! = ["!"]`,
		`let y: Outer.Tag! = JSON.decode("\"!\"")`,
		`let y: {{tag: Outer.Tag!}}! = JSON.decode("{\"tag\":\"!\"}")`,
		`print(x == "!")`, `print("!" == x)`,
		`print(x != "!")`, `print("!" != x)`,
		`let matched = case (x) {
  "!" => true
  else => false
}`,
	} {
		t.Run(statement, func(t *testing.T) {
			require.Contains(t, inferWithEffects(t, declarations+statement), "cannot materialize scalar Tag here")
		})
	}
	// Existing values can pass through coercion boundaries; plain labels need
	// no environment at all. Defaults run in the constructor's closure.
	require.NoError(t, evalExample(declarations+`
if (true) {
  let y: Outer.Tag! = x
  assert { y.label == "A" }
  assert { x == y }
}
assert { JSON.encode(x) == "\"A!\"" }
type Plain { scalar Label }
if (true) {
  let plain: Plain.Label! = "ok"
  assert { plain == "ok" }
}
type Holder {
  prefix: String!
  scalar Tag { new(raw: String!) { prefix + raw } }
  type Item { tag: Tag! = "default" }
  make: Item! { Item() }
}
assert { toString(Holder("A").make.tag) == "Adefault" }
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
