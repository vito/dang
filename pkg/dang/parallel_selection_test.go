package dang

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// barrier releases all arrivals only once n of them are waiting at the same
// time. Used under synctest to prove work runs concurrently: if it ran
// serially the first arrival would block forever and synctest would report a
// deadlock, failing the test.
type barrier struct {
	n      int
	mu     sync.Mutex
	count  int
	gate   chan struct{}
	peaked int // highest simultaneous arrival count observed
}

func newBarrier(n int) *barrier {
	return &barrier{n: n, gate: make(chan struct{})}
}

func (b *barrier) arrive() {
	b.mu.Lock()
	b.count++
	if b.count > b.peaked {
		b.peaked = b.count
	}
	if b.count == b.n {
		close(b.gate)
	}
	b.mu.Unlock()
	<-b.gate
}

func TestEvalParallelRunsConcurrentlyAndOrders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const n = 8
		bar := newBarrier(n)
		got, err := evalParallel(context.Background(), n, func(ctx context.Context, i int) (int, error) {
			// Every task must be in flight at once for the barrier to release;
			// a serial implementation would deadlock here.
			bar.arrive()
			return i * 10, nil
		})
		require.NoError(t, err)
		require.Equal(t, []int{0, 10, 20, 30, 40, 50, 60, 70}, got)
		require.Equal(t, n, bar.peaked, "all tasks should be running simultaneously")
	})
}

func TestEvalParallelFastPaths(t *testing.T) {
	// n == 0: fn is never called and an empty (non-nil) slice comes back.
	calls := 0
	got, err := evalParallel(context.Background(), 0, func(ctx context.Context, i int) (int, error) {
		calls++
		return 0, nil
	})
	require.NoError(t, err)
	require.Equal(t, []int{}, got)
	require.Equal(t, 0, calls)

	// n == 1: runs inline on the caller's context (no extra cancel wrapper) and
	// returns the single result.
	callerCtx := context.WithValue(context.Background(), forceChainKey{}, "marker")
	var sawCtx context.Context
	got, err = evalParallel(callerCtx, 1, func(ctx context.Context, i int) (int, error) {
		sawCtx = ctx
		return 42, nil
	})
	require.NoError(t, err)
	require.Equal(t, []int{42}, got)
	require.Same(t, callerCtx, sawCtx, "single-item work should run on the caller's context")
}

func TestEvalParallelFailsFastPreferringRealError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sentinel := errors.New("boom")
		const n = 5
		const failAt = 3
		got, err := evalParallel(context.Background(), n, func(ctx context.Context, i int) (int, error) {
			if i == failAt {
				return 0, sentinel
			}
			// Siblings block until the failure cancels them. They return
			// context.Canceled, which must NOT mask the real error even though
			// several of them sit at a lower index than failAt.
			<-ctx.Done()
			return 0, context.Cause(ctx)
		})
		require.Nil(t, got)
		require.ErrorIs(t, err, sentinel)
	})
}

func TestEvalParallelReportsLowestIndexError(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")
	got, err := evalParallel(context.Background(), 4, func(ctx context.Context, i int) (int, error) {
		switch i {
		case 1:
			return 0, first
		case 3:
			return 0, second
		default:
			return i, nil
		}
	})
	require.Nil(t, got)
	require.ErrorIs(t, err, first)
}

// TestMultiFieldSelectionEvaluatesFieldsConcurrently proves the non-GraphQL
// multi-field selection path (`recv.{{ a, b, ... }}`) fans its fields out
// concurrently, end to end through parse → infer → eval. Each selected field
// calls a builtin that blocks on a shared barrier sized to the field count, so
// the selection only completes if every field is evaluated at the same time.
func TestMultiFieldSelectionEvaluatesFieldsConcurrently(t *testing.T) {
	const n = 4
	var active *barrier
	typeScope, valueScope := barrierModuleScopes(t, "ParObj", n, func() { active.arrive() })

	synctest.Test(t, func(t *testing.T) {
		active = newBarrier(n)
		result := runProgram(t, typeScope, valueScope, "ParObj.{{ f0, f1, f2, f3 }}")
		obj, ok := result.(*Object)
		require.True(t, ok, "expected an object, got %T", result)
		for i := range n {
			val, found, err := obj.Lookup(context.Background(), fmt.Sprintf("f%d", i))
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, StringValue{Val: fmt.Sprintf("v%d", i)}, val)
		}
		require.Equal(t, n, active.peaked, "all selected fields should evaluate simultaneously")
	})
}

// TestListSelectionEvaluatesElementsConcurrently proves selection over a list
// (`list.{{ field }}`) fans out across the list elements concurrently. The list
// holds n module values whose selected field blocks on a barrier sized to the
// element count, so the selection only completes if every element is evaluated
// at the same time.
func TestListSelectionEvaluatesElementsConcurrently(t *testing.T) {
	const n = 4
	var active *barrier
	typeScope, valueScope := barrierModuleScopes(t, "ParList", n, func() { active.arrive() })

	synctest.Test(t, func(t *testing.T) {
		active = newBarrier(n)
		result := runProgram(t, typeScope, valueScope, "[ParList, ParList, ParList, ParList].{{ f0 }}")
		list, ok := result.(ListValue)
		require.True(t, ok, "expected a list, got %T", result)
		require.Len(t, list.Elements, n)
		for _, elem := range list.Elements {
			obj, ok := elem.(*Object)
			require.True(t, ok, "expected an object element, got %T", elem)
			val, found, err := obj.Lookup(context.Background(), "f0")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, StringValue{Val: "v0"}, val)
		}
		require.Equal(t, n, active.peaked, "all list elements should evaluate simultaneously")
	})
}

// barrierModuleScopes registers a host module named modName with n zero-arg
// static methods f0..f{n-1}; each runs hit() (to synchronize on a barrier) and
// returns "v{i}". It returns type and value scopes in which the module
// resolves. The builtin registry is saved and restored around the test, and the
// method type schemes are installed by hand because the Prelude — where init()
// would normally register them — is frozen at process start.
func barrierModuleScopes(t *testing.T, modName string, n int, hit func()) (TypeScope, ValueScope) {
	t.Helper()
	// Register into a throwaway registry so these test-only builtins never leak
	// into the shared one (Register mutates the registry in place, so swapping
	// the pointer is the only way to isolate it).
	saved := builtins
	builtins = newBuiltinRegistry()
	t.Cleanup(func() { builtins = saved })

	host := NewType(modName, ObjectKind)
	for i := range n {
		name := fmt.Sprintf("f%d", i)
		val := fmt.Sprintf("v%d", i)
		StaticMethod(host, name).
			Returns(NonNull(StringType)).
			Impl(func(ctx context.Context, _ Args) (Value, error) {
				hit()
				return StringValue{Val: val}, nil
			})
		// Mirror registerBuiltinTypes for this late registration.
		host.Add(name, hm.NewScheme(nil, hm.NewFnType(NewRecordType(""), NonNull(StringType))))
		host.SetVisibility(name, PublicVisibility)
	}

	typeScope := NewPreludeTypeScope("")
	typeScope.primary.AddObject(modName, host)
	typeScope.primary.Add(modName, hm.NewScheme(nil, hm.NonNullType{Type: host}))

	// NewValueScope picks the host module's static methods up from the registry
	// (which, unlike the Prelude, reflects the late registration above).
	return typeScope, NewValueScope(typeScope)
}

// runProgram parses, type-checks, and evaluates src against the given scopes and
// returns the value of the final form.
func runProgram(t *testing.T, typeScope TypeScope, valueScope ValueScope, src string) Value {
	t.Helper()
	parsed, err := ParseWithRecovery("barrier", []byte(src))
	require.NoError(t, err)
	file, ok := parsed.(*FileBlock)
	require.True(t, ok, "unexpected parse result %T", parsed)

	fresh := hm.NewSimpleFresher()
	_, err = InferFormsWithPhases(context.Background(), file.Forms, typeScope, fresh)
	require.NoError(t, err)

	var result Value
	for _, node := range file.Forms {
		result, err = EvalNode(context.Background(), valueScope, node)
		require.NoError(t, err)
	}
	return result
}
