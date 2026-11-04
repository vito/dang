package dang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
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

// Count leaf equality probes rather than time: checking a collection spine
// must not repeatedly ask assignability about each remaining whole subtree.
type scalarCoercionCountedType struct {
	hm.Type
	equalities *int
}

func (t scalarCoercionCountedType) Eq(other hm.Type) bool {
	(*t.equalities)++
	return t.Type.Eq(other)
}

func TestScalarCoercionCollectionWalk(t *testing.T) {
	home := NewType("", ObjectKind)
	tag := NewType("Local", ScalarKind)
	tag.scalarNeedsRuntime = true
	tag.localFrame = home
	for _, existing := range []bool{false, true} {
		for _, depth := range []int{0, 1, 16, 128} {
			t.Run(fmt.Sprintf("existing=%t/depth=%d", existing, depth), func(t *testing.T) {
				var equalities int
				var leaf hm.Type = StringType
				if existing {
					leaf = tag
				}
				var have hm.Type = scalarCoercionCountedType{Type: leaf, equalities: &equalities}
				var want hm.Type = tag
				for level := 0; level < depth; level++ {
					if level%2 == 0 {
						have, want = ListType{have}, ListType{want}
					} else {
						have, want = MapType{have}, MapType{want}
					}
				}
				err := checkScalarCoercion(NewType("", ObjectKind), have, want)
				if existing {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, "cannot materialize scalar Local here")
				}
				require.Equal(t, 1, equalities, "collection nesting must not repeat leaf comparisons")
			})
		}
	}
}

func TestScalarCoercionCollectionSemantics(t *testing.T) {
	home := NewType("", ObjectKind)
	tag := NewType("Local", ScalarKind)
	tag.scalarNeedsRuntime = true
	tag.localFrame = home
	other := NewType("Other", ScalarKind)
	for _, tc := range []struct {
		name         string
		have         hm.Type
		want         hm.Type
		materializes bool
	}{
		{"existing", tag, tag, false},
		{"literal", StringType, tag, true},
		{"existing union member", tag, hm.NewUnionType(tag, other), false},
		{"uncoerced union member", StringType, hm.NewUnionType(StringType, tag), false},
		{"coerced union member", StringType, hm.NewUnionType(tag, other), true},
	} {
		for _, nonNull := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/nonNull=%t", tc.name, nonNull), func(t *testing.T) {
				have, want := tc.have, tc.want
				// Alternate list/map shapes and exercise nullable widening at
				// collection boundaries without changing the scalar conversion.
				for level := 0; level < 8; level++ {
					if nonNull {
						have = hm.NonNullType{Type: have}
						if level%2 == 0 {
							want = hm.NonNullType{Type: want}
						}
					}
					have, want = ListType{MapType{have}}, ListType{MapType{want}}
				}
				// Every case is valid with coercion; this helper must only add
				// the missing-environment error where a new scalar is needed.
				_, err := hm.AssignableWithCoercion(have, want)
				require.NoError(t, err)
				require.NoError(t, checkScalarCoercion(home, have, want))
				err = checkScalarCoercion(NewType("", ObjectKind), have, want)
				if tc.materializes {
					require.ErrorContains(t, err, "cannot materialize scalar Local here")
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

func TestForwardScalarRuntime(t *testing.T) {
	for _, initializer := range []string{`"hello"`, `JSON.decode("\"hello\"")`} {
		t.Run(initializer, func(t *testing.T) {
			dir := preparedSource(t, `type Config { self { value: Label! = `+initializer+` } }
scalar Label { label: String! { toString(self) } }
assert { Config.value.label == "hello" }
`)
			program, err := PrepareDir(context.Background(), dir)
			require.NoError(t, err)
			_, err = program.Evaluate(context.Background())
			require.NoError(t, err)
		})
	}
	for _, test := range []struct{ source, message string }{
		{`type Config { self { value: Label! = "hello"; early: String! = value.label } }
scalar Label { label: String! { toString(self) } }`, "scalar Label is not initialized yet"},
		{`type Config { self { value: Label! = "hello" } }
scalar Label { new(raw: String!) { raw.toUpper } }`, "runtime environment for scalar Label is unavailable"},
	} {
		program, err := PrepareDir(context.Background(), preparedSource(t, test.source))
		require.NoError(t, err)
		_, err = program.Evaluate(context.Background())
		require.ErrorContains(t, err, test.message)
	}
}

func TestImportedScalarRuntimeCallbacks(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"dang.toml":       "[imports.Lib]\npath = \"./lib\"\n",
		"lib/dang.toml":   "[imports.Other]\npath = \"../other\"\n",
		"other/main.dang": `invoke(&f(raw: String!): a): a { f("!") }`,
		"lib/main.dang": `import Other
prefix: String! = "lib"
type Config { self { value: Label! = "forward" } }
scalar Label { label: String! { prefix + toString(self) } }
forward: Label! = Other.invoke { ignored => Config.value }
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
assert { Lib.forward.label == "libforward" }
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
