package dang

import (
	"bytes"
	"context"
	"errors"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/ioctx"
)

// evalWithContext runs a snippet through parse → infer → eval with the given
// context, returning the evaluation error and anything printed.
func evalWithContext(t *testing.T, ctx context.Context, src string) (string, error) {
	t.Helper()

	parsed, err := ParseWithRecovery("call_depth_test", []byte(src))
	require.NoError(t, err, "parse")
	file, ok := parsed.(*FileBlock)
	require.True(t, ok, "unexpected parse result %T", parsed)

	typeScope, valueScope := BuildScopesFromImports("", nil)
	fresh := hm.NewSimpleFresher()
	_, err = InferFormsWithPhases(ctx, file.Forms, typeScope, fresh)
	require.NoError(t, err, "infer")

	var out bytes.Buffer
	ctx = WithEvalContext(ctx, NewEvalContext("call_depth_test", src))
	ctx = ioctx.StdoutToContext(ctx, &out)
	ctx = ioctx.StderrToContext(ctx, &out)
	for _, node := range file.Forms {
		if _, err := EvalNode(ctx, valueScope, node); err != nil {
			return out.String(), err
		}
	}
	return out.String(), nil
}

func requireStackOverflow(t *testing.T, err error, maxDepth int) {
	t.Helper()
	require.Error(t, err)
	var overflow *StackOverflowError
	require.True(t, errors.As(err, &overflow), "expected a *StackOverflowError, got %T: %v", err, err)
	require.Equal(t, maxDepth, overflow.MaxDepth)
	require.Contains(t, err.Error(), "stack overflow")
}

// Runaway recursion used to grow the Go stack until the runtime aborted the
// whole process with an unrecoverable "fatal error: stack overflow". These
// run at the default limit under a Go stack cap well below the runtime's
// default 1 GB, so they also prove the limit trips with plenty of headroom.
func TestUnboundedRecursionFailsWithStackOverflow(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(64 << 20))
	t.Run("function", func(t *testing.T) {
		_, err := evalWithContext(t, context.Background(), `
let forever(n: Int!): Int! { forever(n + 1) }
forever(0)
`)
		requireStackOverflow(t, err, DefaultMaxCallDepth)
	})

	// The shape that crashed an engine: a method meant to call a top-level
	// function of the same name resolved to itself instead.
	t.Run("naked self-call in a method", func(t *testing.T) {
		_, err := evalWithContext(t, context.Background(), `
type Checks {
  x: Int! = 0

  explore(n: Int!): Int! { explore(n) }
}
Checks(0).explore(1)
`)
		requireStackOverflow(t, err, DefaultMaxCallDepth)
	})

	t.Run("mutual recursion", func(t *testing.T) {
		_, err := evalWithContext(t, context.Background(), `
let ping(n: Int!): Int! { pong(n) }
let pong(n: Int!): Int! { ping(n) }
ping(0)
`)
		requireStackOverflow(t, err, DefaultMaxCallDepth)
	})

	t.Run("constructor", func(t *testing.T) {
		_, err := evalWithContext(t, context.Background(), `
type Chain {
  n: Int! = 0
  next: Chain! = Chain(n + 1)
}
Chain(0)
`)
		requireStackOverflow(t, err, DefaultMaxCallDepth)
	})
}

func TestMaxCallDepthIsConfigurable(t *testing.T) {
	ctx := ContextWithMaxCallDepth(context.Background(), 50)

	_, err := evalWithContext(t, ctx, `
let down(n: Int!): Int! { if (n == 0) { 0 } else { down(n - 1) } }
down(49)
`)
	require.NoError(t, err, "recursion within the limit succeeds")

	_, err = evalWithContext(t, ctx, `
let down(n: Int!): Int! { if (n == 0) { 0 } else { down(n - 1) } }
down(50)
`)
	requireStackOverflow(t, err, 50)
}

func TestDeepRecursionWithinDefaultLimit(t *testing.T) {
	out, err := evalWithContext(t, context.Background(), `
let sum(n: Int!): Int! { if (n == 0) { 0 } else { n + sum(n - 1) } }
print(sum(5000))
`)
	require.NoError(t, err)
	require.Equal(t, "12502500\n", out)
}

func TestStackOverflowIsRescuable(t *testing.T) {
	out, err := evalWithContext(t, ContextWithMaxCallDepth(context.Background(), 100), `
let forever(n: Int!): Int! { forever(n + 1) }
let result = forever(0) rescue {
  e: RuntimeError => 0 - 1
}
print(result)
`)
	require.NoError(t, err)
	require.Equal(t, "-1\n", out)
}
