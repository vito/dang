package dang

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vito/dang/v2/pkg/hm"
)

// describeEval evaluates source in an empty module and describes the result
// with its inferred type.
func describeEval(t *testing.T, ctx context.Context, source string, opts DescribeOptions) string {
	t.Helper()
	m, err := LoadDir(ctx, t.TempDir(), LoadOptions{})
	require.NoError(t, err)
	t.Cleanup(m.Close)
	val, typ, err := m.Eval(ctx, "", source)
	require.NoError(t, err)
	return Describe(ctx, val, typ, opts)
}

func TestDescribeValues(t *testing.T) {
	ctx := context.Background()
	for _, example := range []struct {
		name   string
		source string
		opts   DescribeOptions
		want   string
	}{
		{"string", `"hello\nworld"`, DescribeOptions{}, `String! = "hello\nworld"`},
		{"int", `1 + 2`, DescribeOptions{}, `Int! = 3`},
		{"null", `let s: String = null
s`, DescribeOptions{}, `String = null`},
		{"list", `[1, 2, 3]`, DescribeOptions{}, `[Int!]! = [1, 2, 3]`},
		{"list cut", `[1, 2, 3, 4, 5]`, DescribeOptions{MaxItems: 2}, `[Int!]! = [1, 2, … (3 more)]`},
		{"empty list", `let xs: [Int!]! = []
xs`, DescribeOptions{}, `[Int!]! = []`},
		{"map", `["b": 1, "a": 2]`, DescribeOptions{}, `Map[Int!]! = ["b": 1, "a": 2]`},
		{"map cut", `["b": 1, "a": 2, "c": 3]`, DescribeOptions{MaxItems: 1}, `Map[Int!]! = ["b": 1, … (2 more)]`},
		{"record", `{{name: "a", tags: ["x", "y"], inner: {{n: 1}}}}`, DescribeOptions{Width: -1},
			`{inner: {n: Int!}!, name: String!, tags: [String!]!}! = {{name: "a", tags: ["x", "y"], inner: {{n: 1}}}}`},
		{"depth", `[[[[1, 2]]]]`, DescribeOptions{MaxDepth: 2}, `[[[[Int!]!]!]!]! = [[[… 1 item]]]`},
		{"record depth", `{{a: {{b: {{c: 1}}}}}}`, DescribeOptions{MaxDepth: 2}, `{a: {b: {c: Int!}!}!}! = {{a: {{b: {{…}}}}}}`},
		{"string cut", `"abcdefghijklmnopqrstuvwxyz"`, DescribeOptions{MaxString: 8}, `String! = "abcdef"…(18 chars omitted)…"yz"`},
		{"function", `let inc(x: Int!): Int! { x + 1 }
&inc`, DescribeOptions{}, `(x: Int!): Int! = <function: (x: Int!): Int!>`},
	} {
		t.Run(example.name, func(t *testing.T) {
			require.Equal(t, example.want, describeEval(t, ctx, example.source, example.opts))
		})
	}
}

func TestDescribeObjects(t *testing.T) {
	ctx := context.Background()
	source := `type User {
  pub name: String!
  let secret: Int! = 3
  pub tags: [String!]! = []
  pub greet: String! { "hi " + name }
  pub shout(suffix: String!): String! { name + suffix }
}
User(name: "alice", tags: ["admin"])
`
	require.Equal(t, `User! = User {name: "alice", secret: 3, tags: ["admin"]}`, describeEval(t, ctx, source, DescribeOptions{}))
	require.Equal(t, `User! = User {name: "alice", tags: ["admin"]}`, describeEval(t, ctx, source, DescribeOptions{PublicOnly: true}))
	require.Equal(t, `User! = User {name: "alice", … (2 more)}`, describeEval(t, ctx, source, DescribeOptions{MaxItems: 1}))
	listed := strings.Replace(source, `User(name: "alice", tags: ["admin"])`, `[User(name: "alice", tags: ["admin"])]`, 1)
	require.Equal(t, `[User!]! = [User {…}]`, describeEval(t, ctx, listed, DescribeOptions{MaxDepth: 1}))
}

func TestDescribeLayout(t *testing.T) {
	ctx := context.Background()
	got := describeEval(t, ctx, `[{{name: "alpha", value: 1}}, {{name: "beta", value: 2}}, {{name: "gamma", value: 3}}]`, DescribeOptions{Width: 40})
	require.Equal(t, `[{name: String!, value: Int!}!]! = [
  {{name: "alpha", value: 1}},
  {{name: "beta", value: 2}},
  {{name: "gamma", value: 3}},
]`, got)

	long := Describe(ctx, StringValue{Val: strings.Repeat("x", 100)}, nil, DescribeOptions{MaxString: -1, MaxBytes: 20})
	require.Equal(t, "String! = \"xxxxxxxxx\n… (output truncated: 92 more bytes)", long)
}

func TestDescribeDeterministic(t *testing.T) {
	ctx := context.Background()
	source := `{{z: ["q": 1, "b": 2, "m": 3], a: [{{y: 1, x: 2}}]}}`
	first := describeEval(t, ctx, source, DescribeOptions{})
	for range 20 {
		require.Equal(t, first, describeEval(t, ctx, source, DescribeOptions{}))
	}
}

func TestDescribeLazyGraphQL(t *testing.T) {
	ctx, client := fakeGraphQLContext(t, true)
	m, err := LoadDir(ctx, t.TempDir(), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()

	val, typ, err := m.Eval(ctx, "", `container.from(address: "alpine").withEnv(value: "b", name: "A")`)
	require.NoError(t, err)
	require.Zero(t, client.count(), "evaluating the chain ran a query")

	lazy := Describe(ctx, val, typ, DescribeOptions{})
	require.Equal(t, `Test.Container! = <lazy: container.from(address: "alpine").withEnv(name: "A", value: "b")>
(not evaluated; select a field such as .sync or .stdout to run it)`, lazy)
	require.Zero(t, client.count(), "describing a lazy value ran a query")
	require.Equal(t, lazy, Describe(ctx, val, typ, DescribeOptions{}))

	// Nested, the handle says what type it is.
	val, typ, err = m.Eval(ctx, "", `{{c: container.from(address: "alpine"), n: 1}}`)
	require.NoError(t, err)
	require.Equal(t, `{c: Test.Container!, n: Int!}! = {{c: <lazy Container: container.from(address: "alpine")>, n: 1}}`,
		Describe(ctx, val, typ, DescribeOptions{Width: -1}))
	require.Zero(t, client.count())

	// Forcing is explicit, and a failing field doesn't hide the others.
	// Neither the plaintext nor the Void field is selected.
	val, typ, err = m.Eval(ctx, "", `container.from(address: "alpine")`)
	require.NoError(t, err)
	require.Equal(t, `Test.Container! = Container {
  stdout: "hi\n",
  exitCode: <error: no command has been set>,
}`,
		Describe(ctx, val, typ, DescribeOptions{Force: true}))
	require.Equal(t, 2, client.count())
}

func TestDescribeNeverForcesMutations(t *testing.T) {
	ctx, client := fakeGraphQLContext(t, true)
	m, err := LoadDir(ctx, t.TempDir(), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()

	val, typ, err := m.Eval(ctx, "", `Mutation.createContainer(name: "a")`)
	require.NoError(t, err)
	require.Equal(t, `Test.Container! = <lazy: mutation createContainer(name: "a")> (not forced: it would send the mutation again)`,
		Describe(ctx, val, typ, DescribeOptions{Force: true}))
	require.Zero(t, client.count())
}

func TestDescribeLazyGraphQLList(t *testing.T) {
	ctx, client := fakeGraphQLContext(t, true)
	m, err := LoadDir(ctx, t.TempDir(), LoadOptions{})
	require.NoError(t, err)
	defer m.Close()

	val, typ, err := m.Eval(ctx, "", `containers`)
	require.NoError(t, err)
	require.Equal(t, `GraphQL[Test.Container!]! = <lazy: containers>
(not evaluated; select fields with .{{…}} to run it)`, Describe(ctx, val, typ, DescribeOptions{}))
	require.Zero(t, client.count())

	require.Equal(t, `GraphQL[Test.Container!]! = [
  Container {stdout: "hi\n", exitCode: 0},
  Container {stdout: "hi\n", exitCode: 0},
]`, Describe(ctx, val, typ, DescribeOptions{Force: true}))
	require.Equal(t, 2, client.count())
}

func TestDescribeWithoutType(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, "null", Describe(ctx, NullValue{}, nil, DescribeOptions{}))
	require.Equal(t, `[String!]! = ["a"]`, Describe(ctx, ListValue{
		Elements: []Value{StringValue{Val: "a"}},
		ElemType: hm.NonNullType{Type: StringType},
	}, nil, DescribeOptions{}))
}
