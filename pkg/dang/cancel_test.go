package dang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// evalWithTimeout runs src under a context that times out after d, failing
// the test if evaluation doesn't stop promptly after that.
func evalWithTimeout(t *testing.T, d time.Duration, src string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	start := time.Now()
	_, err := evalWithContext(t, ctx, src)
	require.Less(t, time.Since(start), d+5*time.Second, "evaluation didn't stop promptly")
	return err
}

func requireCancelled(t *testing.T, err error, cause error) {
	t.Helper()
	require.Error(t, err)
	var cancelled *CancelledError
	require.True(t, errors.As(err, &cancelled), "expected a *CancelledError, got %T: %v", err, err)
	require.ErrorIs(t, err, cause)
	require.ErrorContains(t, err, "evaluation cancelled: "+cause.Error())
}

// Nothing else stops these: each would run forever without a checkpoint.
func TestRunawayEvaluationStopsAtDeadline(t *testing.T) {
	for name, src := range map[string]string{
		"loop": `loop { null }`,
		"recursion": `
let fib(n: Int!): Int! { if (n < 2) { n } else { fib(n - 1) + fib(n - 2) } }
fib(1000)
`,
		"method recursion": `
type Walker {
  walk(n: Int!): Int! { if (n < 2) { n } else { walk(n - 1) + walk(n - 2) } }
}
Walker().walk(1000)
`,
		"block builtin": `
let spin(): Int! { loop { null } }
[1, 2, 3].map { x => spin() }
`,
		"nested loops": `loop { [1, 2, 3].each { x => x } }`,
		"parallel fields": `
let spin(): Int! { loop { null } }
{{ a: spin(), b: spin() }}
`,
	} {
		t.Run(name, func(t *testing.T) {
			err := evalWithTimeout(t, 50*time.Millisecond, src)
			requireCancelled(t, err, context.DeadlineExceeded)
		})
	}
}

// A rescue must not catch the cancellation: `loop { x rescue null }` would
// otherwise spin forever, its every failure rescued.
func TestCancellationIsNotRescued(t *testing.T) {
	for name, src := range map[string]string{
		"fallback": `
let spin(): Int! { loop { null } }
loop { spin() rescue null }
`,
		"Error clause": `
let spin(): Int! { loop { null } }
loop { spin() rescue { e: Error => 0 } }
`,
		"else clause": `
let spin(): Int! { loop { null } }
loop { spin() rescue { else => 0 } }
`,
		"RuntimeError clause": `
let spin(): Int! { loop { null } }
loop { spin() rescue { e: RuntimeError => 0 } }
`,
		"rescue around the loop": `print(loop { null } rescue 1)`,
	} {
		t.Run(name, func(t *testing.T) {
			err := evalWithTimeout(t, 50*time.Millisecond, src)
			requireCancelled(t, err, context.DeadlineExceeded)
		})
	}

	// Deterministically: the context is already cancelled, so the call fails
	// inside the rescue's operand.
	t.Run("already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		out, err := evalWithContext(t, ctx, `
let f(): Int! { 1 }
print(f() rescue 2)
`)
		requireCancelled(t, err, context.Canceled)
		require.Empty(t, out)
	})

	t.Run("cause", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		cause := errors.New("agent turn ended")
		cancel(cause)
		_, err := evalWithContext(t, ctx, `
let f(): Int! { 1 }
f() rescue 2
`)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorIs(t, err, cause)
		require.ErrorContains(t, err, "evaluation cancelled: agent turn ended")
	})
}

// A lazy binding forced by an evaluation that is cancelled must not keep that
// error as its value: nothing could rescue it, so a host evaluating again in
// the same scope would find the binding broken for good.
func TestCancelledLazyInitIsNotMemoized(t *testing.T) {
	obj := NewObject(NewType("test", ObjectKind))
	var runs atomic.Int32
	obj.BindLazy("x", func(ctx context.Context) (Value, error) {
		runs.Add(1)
		// As if calling a function.
		if _, err := enterCall(ctx); err != nil {
			return nil, err
		}
		return IntValue{Val: 42}, nil
	}, PublicVisibility)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := obj.Lookup(cancelled, "x")
	requireCancelled(t, err, context.Canceled)

	val, found, err := obj.Lookup(context.Background(), "x")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, IntValue{Val: 42}, val)

	_, _, err = obj.Lookup(context.Background(), "x")
	require.NoError(t, err)
	require.Equal(t, int32(2), runs.Load(), "the completed run should be memoized")
}

// Likewise an imported module's top-level code.
func TestCancelledModuleEvaluationIsNotMemoized(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`let id(x: Int!): Int! { x }
assert { id(1) == 1 }
pub v: Int! = 42
`), 0o600))
	mod, err := loadDangModule(context.Background(), dir)
	require.NoError(t, err)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = mod.valueScope(cancelled)
	requireCancelled(t, err, context.Canceled)

	scope, err := mod.valueScope(context.Background())
	require.NoError(t, err)
	v, found, err := scope.Lookup(context.Background(), "v")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, IntValue{Val: 42}, v)
}

// The fail-fast cancellation of `{{ }}` siblings stops in-flight I/O, not Dang
// code: the other fields run to completion, so the error reported is still the
// lowest in source order, whichever field raised first.
func TestSiblingFailureDoesNotCancelDangCode(t *testing.T) {
	for range 20 {
		_, err := evalWithContext(t, context.Background(), `
let spin(n: Int!): Int! {
  let i = 0
  loop { i += 1; if (i >= n) { break i } }
}
{{
  slow: { spin(2000); raise "from slow" },
  fast: raise "from fast"
}}
`)
		require.ErrorContains(t, err, "from slow")
	}

	// They still stop when the evaluation is cancelled.
	err := evalWithTimeout(t, 50*time.Millisecond, `
let spin(): Int! { loop { null } }
{{ a: spin(), b: raise "from b" }}
`)
	require.ErrorContains(t, err, "from b")

	// A field failing after a sibling failed is still memoized: what it
	// raised is its result, not an artifact of the cancellation, so a field
	// reading it doesn't run it again.
	out, err := evalWithContext(t, context.Background(), `
let spin(n: Int!): Int! {
  let i = 0
  loop { i += 1; if (i >= n) { break i } }
}
print({{
  a: raise "from a",
  b: { spin(20000); print("running b"); raise "from b" },
  c: { spin(10); b }
}} rescue null)
`)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(out, "running b"), "b ran more than once:\n%s", out)
}

// Only the cancellation evalParallel uses among `{{ }}` siblings is ignored:
// the evaluation's own, or one layered on inside a task, still stops it.
func TestSiblingCancellationOnlyStopsIO(t *testing.T) {
	outer, cancelOuter := context.WithCancel(context.Background())
	defer cancelOuter()
	task, cancelTask := context.WithCancelCause(outer)
	f := deriveFrame(task)
	f.task = &parallelTask{ctx: task, watch: outer}
	task = withFrame(task, f)

	inner, cancelInner := context.WithCancel(task)
	cancelInner()
	_, err := enterCall(inner)
	requireCancelled(t, err, context.Canceled)

	cancelTask(errSiblingFailed)
	_, err = enterCall(task)
	require.NoError(t, err, "a sibling's failure doesn't stop Dang code")

	cancelOuter()
	_, err = enterCall(task)
	requireCancelled(t, err, context.Canceled)
}

// I/O cut off in a `{{ }}` task because a sibling failed doesn't break a lazy
// binding the task was initializing.
func TestLazyInitInterruptedBySiblingIsNotMemoized(t *testing.T) {
	obj := NewObject(NewType("test", ObjectKind))
	var runs atomic.Int32
	obj.BindLazy("x", func(ctx context.Context) (Value, error) {
		if runs.Add(1) == 1 {
			// A query, cut short.
			<-ctx.Done()
			return nil, fmt.Errorf("query: %w", ctx.Err())
		}
		return IntValue{Val: 42}, nil
	}, PublicVisibility)

	_, err := evalParallel(context.Background(), 2, func(ctx context.Context, i int) (Value, error) {
		if i == 0 {
			return nil, errors.New("boom")
		}
		v, _, err := obj.Lookup(ctx, "x")
		return v, err
	})
	require.ErrorContains(t, err, "boom")

	val, _, err := obj.Lookup(context.Background(), "x")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 42}, val)
}

// A force waiting for another evaluation's initializer stops waiting when its
// own evaluation is cancelled.
func TestLazyInitWaiterStopsWhenCancelled(t *testing.T) {
	obj := NewObject(NewType("test", ObjectKind))
	release := make(chan struct{})
	obj.BindLazy("x", func(ctx context.Context) (Value, error) {
		<-release
		return IntValue{Val: 42}, nil
	}, PublicVisibility)
	obj.mu.Lock()
	p := obj.Pending["x"]
	obj.mu.Unlock()

	first := make(chan error, 1)
	go func() {
		_, _, err := obj.Lookup(context.Background(), "x")
		first <- err
	}()
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.running
	}, 5*time.Second, time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := obj.Lookup(ctx, "x")
	requireCancelled(t, err, context.DeadlineExceeded)

	close(release)
	require.NoError(t, <-first)
	val, _, err := obj.Lookup(context.Background(), "x")
	require.NoError(t, err)
	require.Equal(t, IntValue{Val: 42}, val)
}

// Frames answer Done and Err from the nearest context below them that isn't a
// frame, so a cancellation check costs the same at any recursion depth.
func TestFramesShortCircuitCancellation(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	ctx := context.Context(base)
	for range 1000 {
		ctx = withFrame(ctx, deriveFrame(ctx))
	}
	fc, ok := ctx.(*frameContext)
	require.True(t, ok)
	require.Equal(t, base, fc.src)
	require.NoError(t, ctx.Err())
	cancel()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	<-ctx.Done()
}
