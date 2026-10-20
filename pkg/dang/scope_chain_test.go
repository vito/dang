package dang

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// prepareScopeChainProgram parses, infers and evaluates every form of src but
// the last, returning what's needed to (re-)evaluate the last one.
func prepareScopeChainProgram(tb testing.TB, src string) (context.Context, ValueScope, Node) {
	tb.Helper()

	parsed, err := ParseWithRecovery("scope_chain_test", []byte(src))
	require.NoError(tb, err, "parse")
	file, ok := parsed.(*FileBlock)
	require.True(tb, ok, "unexpected parse result %T", parsed)

	ctx := context.Background()
	typeScope, valueScope := BuildScopesFromImports("", nil)
	_, err = InferFormsWithPhases(ctx, file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(tb, err, "infer")

	ctx = WithEvalContext(ctx, NewEvalContext("scope_chain_test", src))
	last := len(file.Forms) - 1
	for _, node := range file.Forms[:last] {
		_, err := EvalNode(ctx, valueScope, node)
		require.NoError(tb, err)
	}
	return ctx, valueScope, file.Forms[last]
}

// Self-recursive methods: a naked self-call, an explicit `self.` call, and
// each of those writing a field on every level.
const scopeChainTypes = `
type Rec {
  n: Int! = 0

  naked(k: Int!): Rec! { if (k <= 0) { self } else { naked(k - 1) } }
  explicit(k: Int!): Rec! { if (k <= 0) { self } else { self.explicit(k - 1) } }
}

type Bumper {
  value: Int! = 0

  up(k: Int!): Bumper! { if (k <= 0) { self } else { value += 1; up(k - 1) } }
  setUp(k: Int!): Bumper! {
    if (k <= 0) { self } else { self.value = self.value + 1; self.setUp(k - 1) }
  }
}
`

var scopeChainCases = []struct {
	name string
	call string
}{
	{"naked", "Rec(0).naked(%d)"},
	{"explicit", "Rec(0).explicit(%d)"},
	{"bare-write", "Bumper(0).up(%d)"},
	{"self-write", "Bumper(0).setUp(%d)"},
}

// Each method call runs against a snapshot of its receiver (snapshotReceiver).
// A self-recursive method's receiver is the previous level's snapshot (or a
// copy-on-write generation of it), so if snapshots stacked rather than
// flattened, every level would add a frame and every lookup of a name living on
// the instance (the method itself, fields, builtins) would walk the whole
// recursion: quadratic time and linear memory.
func TestSelfRecursionKeepsReceiverChainBounded(t *testing.T) {
	const depth = 1000
	for _, c := range scopeChainCases {
		t.Run(c.name, func(t *testing.T) {
			ctx, scope, node := prepareScopeChainProgram(t, scopeChainTypes+fmt.Sprintf(c.call, depth))
			val, err := EvalNode(ctx, scope, node)
			require.NoError(t, err)
			obj, ok := val.(*Object)
			require.True(t, ok, "expected an object, got %T", val)
			require.LessOrEqual(t, chainDepth(obj), 3,
				"receiver chain grew with recursion depth")
		})
	}
}

// BenchmarkSelfRecursion times self-recursive methods at two depths, so a
// cost that grows faster than the depth shows up as a ratio well above 4.
func BenchmarkSelfRecursion(b *testing.B) {
	for _, c := range scopeChainCases {
		for _, depth := range []int{1000, 4000} {
			b.Run(fmt.Sprintf("%s/depth=%d", c.name, depth), func(b *testing.B) {
				ctx, scope, node := prepareScopeChainProgram(b, scopeChainTypes+fmt.Sprintf(c.call, depth))
				b.ResetTimer()
				for b.Loop() {
					if _, err := EvalNode(ctx, scope, node); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
