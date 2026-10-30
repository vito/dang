package dang

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

type snippetCase struct {
	name string
	src  string
	out  string
}

func runSnippetCases(t *testing.T, cases []snippetCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := evalWithContext(t, context.Background(), tc.src)
			require.NoError(t, err)
			require.Equal(t, tc.out, out)
		})
	}
}

// TestFreshVarsAreNotWrittenOnes pins down calls whose call-site type
// variables are named like the callee's own. Each snippet runs with a new
// fresher, so the first variable minted at the call site (for `null` or `[]`)
// is named `a`, like the generic function's parameter, but must not be it.
func TestFreshVarsAreNotWrittenOnes(t *testing.T) {
	runSnippetCases(t, []snippetCase{
		{
			name: "named null to defaulted param",
			src: `generic(v: a, x: String = "d"): String { x }
print(generic(1, x: null))`,
			out: "d\n",
		},
		{
			name: "positional null to defaulted param",
			src: `generic(v: a, x: String = "d"): String { x }
print(generic(1, null))`,
			out: "d\n",
		},
		{
			name: "null to nullable param without default",
			src: `generic(v: a, x: String): String { x ?? "none" }
print(generic(1, x: null))`,
			out: "none\n",
		},
		{
			name: "two type variables",
			src: `generic(v: a, w: b, x: String = "d"): String { x }
print(generic(1, "s", x: null))`,
			out: "d\n",
		},
		{
			name: "generic method",
			src: `type Box {
  pub pick(v: a, x: String = "d"): String { x }
}
print(Box.pick(1, x: null))`,
			out: "d\n",
		},
		{
			name: "generic function with a block",
			src: `generic(v: a, x: String = "d", &yield(y: a): b): b { yield(v) }
print(generic(1, x: null) { y => y + 1 })`,
			out: "2\n",
		},
		{
			name: "empty list",
			src: `generic(v: a, w: [String!]!): Int! { w.length }
print(generic(1, w: []))`,
			out: "0\n",
		},
		{
			name: "let-bound empty list",
			src: `let empty = []
generic(v: a, w: [String!]!): Int! { w.length }
print(generic(1, w: empty))`,
			out: "0\n",
		},
		{
			// The fresher wrapped around past 'z', so the null after 26
			// `[]`s was the first `[]`'s variable.
			name: "fresher past z",
			src: `let empty = []
let pad = [[], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], [], []]
takes(x: [Int!]!, y: [String]!): Int! { 1 }
print(takes(empty, [null]))`,
			out: "1\n",
		},
	})
}

// TestFreshVarsAcrossFreshers infers each snippet with a new fresher into one
// scope, as REPL entries are, and as an imported module's values meet its
// importer's: variables the later fresher mints must not be the earlier's.
func TestFreshVarsAcrossFreshers(t *testing.T) {
	typeScope, valueScope := BuildScopesFromImports("", nil)
	var out bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &out)
	for _, src := range []string{
		"let empty = []\nf(v: b, w: [String!]!): Int! { w.length }",
		"print(f(1, w: empty))",
		"takes(x: [Int!]!, y: [String!]!): Int! { 1 }",
		"print(takes([], empty))",
	} {
		parsed, err := ParseWithRecovery("generic_call_test", []byte(src))
		require.NoError(t, err)
		forms := parsed.(*FileBlock).Forms
		_, err = InferFormsWithPhases(ctx, forms, typeScope, hm.NewSimpleFresher())
		require.NoError(t, err, src)
		for _, node := range forms {
			_, err := EvalNode(ctx, valueScope, node)
			require.NoError(t, err)
		}
	}
	require.Equal(t, "0\n1\n", out.String())
}

// TestGenericCallsInstantiateCallees pins down calls whose callee's type
// variables meet others written with the same letter: each call must get its
// own copy of them.
func TestGenericCallsInstantiateCallees(t *testing.T) {
	runSnippetCases(t, []snippetCase{
		{
			name: "another function's variable",
			src: `nonez(x: Int! = 1): [z]! { [] }
g(v: z, w: [String!]!): Int! { w.length }
print(g(1, w: nonez(1)))`,
			out: "0\n",
		},
		{
			name: "two calls to one function",
			src: `nonez(x: Int! = 1): [z]! { [] }
takes(x: [Int!]!, y: [String!]!): Int! { 1 }
print(takes(nonez(1), nonez(1)))`,
			out: "1\n",
		},
		{
			name: "two auto-calls to one function",
			src: `none(x: Int! = 1): [a]! { [] }
takes(x: [Int!]!, y: [String!]!): Int! { 1 }
print(takes(none, none))`,
			out: "1\n",
		},
		{
			// reduce's own `b` is not the receiver's element variable `b`.
			name: "list method on a list of a written variable",
			src: `noneb(x: Int! = 1): [b]! { [] }
let ys = [noneb(1)]
print(ys.reduce("") { acc, x => let n: [Int!]! = x; acc + "!" })`,
			out: "!\n",
		},
	})
}
