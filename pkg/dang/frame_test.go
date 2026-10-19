package dang

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// recursionPrograms are bounded recursions used to check that the cost of a
// call stays constant as recursion deepens. Each program's final form starts
// the recursion; DEPTH is replaced with how deep it goes.
var recursionPrograms = []struct {
	name string
	src  string
}{
	{
		name: "function",
		src: `
let down(n: Int!): Int! {
  print(n)
  if (n == 0) { 0 } else { down(n - 1) }
}
down(DEPTH)
`,
	},
	{
		name: "method",
		src: `
type Counter {
  x: Int! = 0

  down(n: Int!): Int! {
    print(n)
    if (n == 0) { x } else { down(n - 1) }
  }
}
Counter(0).down(DEPTH)
`,
	},
}

// prepareRecursion parses and type-checks src at the given depth, evaluates
// every form but the last, and returns the scope and the final form.
func prepareRecursion(t testing.TB, src string, depth int) (ValueScope, Node) {
	t.Helper()
	src = strings.ReplaceAll(src, "DEPTH", strconv.Itoa(depth))
	parsed, err := ParseWithRecovery("recursion", []byte(src))
	require.NoError(t, err, "parse")
	file, ok := parsed.(*FileBlock)
	require.True(t, ok, "unexpected parse result %T", parsed)

	typeScope, valueScope := BuildScopesFromImports("", nil)
	_, err = InferFormsWithPhases(context.Background(), file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(t, err, "infer")

	forms := file.Forms
	for _, node := range forms[:len(forms)-1] {
		_, err := EvalNode(context.Background(), valueScope, node)
		require.NoError(t, err, "setup")
	}
	return valueScope, forms[len(forms)-1]
}

// countingContext counts the Value lookups that reach it. Installed at the
// root, it measures how many lookups walk the entire context chain.
type countingContext struct {
	context.Context
	lookups atomic.Int64
}

func (c *countingContext) Value(key any) any {
	c.lookups.Add(1)
	return c.Context.Value(key)
}

// rootLookups evaluates the program at the given depth and returns how many
// context lookups reached the root.
func rootLookups(t *testing.T, src string, depth int, withEvalContext bool) int64 {
	t.Helper()
	scope, node := prepareRecursion(t, src, depth)
	root := &countingContext{Context: context.Background()}
	var ctx context.Context = root
	if withEvalContext {
		ctx = WithEvalContext(ctx, NewEvalContext("recursion", src))
	}
	_, err := EvalNode(ctx, scope, node)
	require.NoError(t, err)
	return root.lookups.Load()
}

// Evaluation state lives in a single frame, so lookups for it stop at the
// nearest frame instead of walking a context chain that grows with the
// recursion. A lookup for state that is absent (no EvalContext, no stdout, no
// force chain, ...) used to walk to the root at every node of every level of
// recursion, making deep recursion quadratic.
func TestRecursionDoesNotWalkToContextRoot(t *testing.T) {
	for _, prog := range recursionPrograms {
		for _, withEvalContext := range []bool{false, true} {
			name := prog.name + "/bare"
			if withEvalContext {
				name = prog.name + "/evalctx"
			}
			t.Run(name, func(t *testing.T) {
				shallow := rootLookups(t, prog.src, 10, withEvalContext)
				deep := rootLookups(t, prog.src, 1000, withEvalContext)
				require.Equal(t, shallow, deep,
					"lookups reaching the context root should not grow with recursion depth")
			})
		}
	}
}

func benchmarkRecursion(b *testing.B, src string, depth int, withEvalContext bool) {
	scope, node := prepareRecursion(b, src, depth)
	ctx := ioctx.StdoutToContext(context.Background(), io.Discard)
	if withEvalContext {
		ctx = WithEvalContext(ctx, NewEvalContext("recursion", src))
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := EvalNode(ctx, scope, node); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRecursion evaluates recursions 1000 calls deep, with and without
// an EvalContext installed.
func BenchmarkRecursion(b *testing.B) {
	for _, prog := range recursionPrograms {
		b.Run(prog.name+"/bare", func(b *testing.B) {
			benchmarkRecursion(b, prog.src, 1000, false)
		})
		b.Run(prog.name+"/evalctx", func(b *testing.B) {
			benchmarkRecursion(b, prog.src, 1000, true)
		})
	}
}

// A frame answers ioctx lookups with the writers in effect where it was
// pushed, but writers installed on top of a frame must still take effect,
// including inside calls, which push frames of their own.
func TestWritersInstalledAboveAFrame(t *testing.T) {
	scope, node := prepareRecursion(t, recursionPrograms[0].src, 2)

	var below, above bytes.Buffer
	ctx := ioctx.StdoutToContext(context.Background(), &below)
	ctx = WithEvalContext(ctx, NewEvalContext("recursion", recursionPrograms[0].src))
	ctx = ioctx.StdoutToContext(ctx, &above)

	_, err := EvalNode(ctx, scope, node)
	require.NoError(t, err)
	require.Empty(t, below.String())
	require.Equal(t, "2\n1\n0\n", above.String())
}
