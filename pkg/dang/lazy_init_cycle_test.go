package dang

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// evalFileWithTimeout runs src as a file's top level (declarations installed
// lazily and forced in source order, like a script) and returns its error. A
// deadlock in lazy initialization fails the test after a timeout instead of
// hanging the suite; the stuck goroutines are leaked.
func evalFileWithTimeout(t *testing.T, typeScope TypeScope, valueScope ValueScope, src string) error {
	t.Helper()
	parsed, err := ParseWithRecovery("lazy", []byte(src))
	require.NoError(t, err)
	file, ok := parsed.(*FileBlock)
	require.True(t, ok, "unexpected parse result %T", parsed)

	ctx := withTopLevelFrame(context.Background(), typeScope)
	_, err = InferFormsWithPhases(ctx, file.Forms, typeScope, hm.NewSimpleFresher())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		_, err := EvaluateFormsWithPhases(context.Background(), file.Forms, valueScope)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("evaluation deadlocked")
		return nil
	}
}

// TestLazyInitCycleAcrossFields: an initialization cycle hidden behind
// functions and forced from two `{{ }}` fields at once. The gate holds each
// field inside its own initializer until both are, so field p runs a and
// reads b while field q runs b and reads a: neither chain alone sees the
// cycle, and before the wait-for graph they deadlocked.
func TestLazyInitCycleAcrossFields(t *testing.T) {
	for range 20 {
		bar := newBarrier(2)
		typeScope, valueScope := barrierModuleScopes(t, "Gate", 2, bar.arrive)
		err := evalFileWithTimeout(t, typeScope, valueScope, `
let r = {{ p: a, q: b }}
let a = ga
ga: Int! {
  Gate.f0
  b + 1
}
let b = gb
gb: Int! {
  Gate.f1
  a + 1
}
`)
		require.ErrorContains(t, err, "initialization cycle while evaluating variable")
	}
}

// TestLazyInitCycleThroughNestedFields: the half of the cycle that waits
// runs in a `{{ }}` inside an initializer. Field p runs a, whose child task x
// reads b; field q runs b and reads a. The task x blocks no initializer of its
// own, but its parent's a waits for it, so it counts as part of a's chain.
func TestLazyInitCycleThroughNestedFields(t *testing.T) {
	for range 20 {
		bar := newBarrier(2)
		typeScope, valueScope := barrierModuleScopes(t, "Gate", 2, bar.arrive)
		err := evalFileWithTimeout(t, typeScope, valueScope, `
let r = {{ p: a, q: b }}
let a = {{ x: ga, y: 1 }}.x
ga: Int! {
  Gate.f0
  b + 1
}
let b = gb
gb: Int! {
  Gate.f1
  a + 1
}
`)
		require.ErrorContains(t, err, "initialization cycle while evaluating variable")
	}
}

// TestLazyInitSharedAcrossFields: two fields forcing the same binding is not a
// cycle. The second waits for the first's initializer, which runs once, and
// both see its value. The gate holds the initializer until the other field is
// waiting, so the wait is exercised every time.
func TestLazyInitSharedAcrossFields(t *testing.T) {
	var scope *Object
	var runs atomic.Int32
	typeScope, valueScope := barrierModuleScopes(t, "Gate", 1, func() {
		runs.Add(1)
		waitForWaiter(t, scope, "c")
	})
	var ok bool
	scope, ok = valueScope.(*Object)
	require.True(t, ok, "expected an *Object scope, got %T", valueScope)

	err := evalFileWithTimeout(t, typeScope, valueScope, `
let s = {{ p: c, q: c }}
let c = gc
gc: Int! {
  Gate.f0
  42
}
`)
	require.NoError(t, err)
	require.Equal(t, 42, intField(t, scope, "s", "p"))
	require.Equal(t, 42, intField(t, scope, "s", "q"))
	require.Equal(t, int32(1), runs.Load(), "the shared initializer should run once")
}

// TestLazyInitNestedFieldsInInitializer: `{{ }}` tasks inside initializers
// inherit their parent's chain, wait for each other's bindings, and share
// them, without reporting a false cycle.
func TestLazyInitNestedFieldsInInitializer(t *testing.T) {
	var scope *Object
	var runs atomic.Int32
	typeScope, valueScope := barrierModuleScopes(t, "Gate", 1, func() {
		runs.Add(1)
		waitForWaiter(t, scope, "f")
	})
	var ok bool
	scope, ok = valueScope.(*Object)
	require.True(t, ok, "expected an *Object scope, got %T", valueScope)

	err := evalFileWithTimeout(t, typeScope, valueScope, `
let n = {{ x: d, y: e }}
let d = gd
gd: Int! { e + 1 }
let e = ge
ge: Int! { {{ u: f, v: f + 1 }}.u }
let f = gf
gf: Int! {
  Gate.f0
  10
}
`)
	require.NoError(t, err)
	require.Equal(t, 11, intField(t, scope, "n", "x"))
	require.Equal(t, 10, intField(t, scope, "n", "y"))
	require.Equal(t, int32(1), runs.Load(), "the shared initializer should run once")
}

// intField reads the Int field of the object bound to name in scope.
func intField(t *testing.T, scope *Object, name, field string) int {
	t.Helper()
	val, found, err := scope.Lookup(context.Background(), name)
	require.NoError(t, err)
	require.True(t, found, "%s not bound", name)
	obj, ok := val.(*Object)
	require.True(t, ok, "expected %s to be an object, got %T", name, val)
	val, found, err = obj.Lookup(context.Background(), field)
	require.NoError(t, err)
	require.True(t, found, "%s.%s not bound", name, field)
	i, ok := val.(IntValue)
	require.True(t, ok, "expected %s.%s to be an Int, got %T", name, field, val)
	return i.Val
}

// waitForWaiter blocks until some force is waiting on scope's pending
// initializer for name, which the caller is running.
func waitForWaiter(t *testing.T, scope *Object, name string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		scope.mu.Lock()
		p := scope.Pending[name]
		scope.mu.Unlock()
		if p != nil {
			p.mu.Lock()
			waiting := p.ready != nil
			p.mu.Unlock()
			if waiting {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("no force waited on %q", name)
}
