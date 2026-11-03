package main

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

// testReplSession drives entries through the REPL's inference and evaluation path
// against one persistent pair of scopes, like successive prompts.
type testReplSession struct {
	t          *testing.T
	typeScope  dang.TypeScope
	valueScope dang.ValueScope
}

func newTestReplSession(t *testing.T) *testReplSession {
	typeScope, valueScope := dang.BuildScopesFromImports("", nil)
	return &testReplSession{t: t, typeScope: typeScope, valueScope: valueScope}
}

// enter runs one entry, returning the last value or the type error.
func (s *testReplSession) enter(input string) (dang.Value, error) {
	s.t.Helper()
	parsed, err := dang.ParseWithRecovery("repl", []byte(input))
	require.NoError(s.t, err)
	forms := parsed.(*dang.FileBlock).Forms
	if _, err := inferReplForms(context.Background(), forms, s.typeScope, hm.NewSimpleFresher()); err != nil {
		return nil, err
	}
	var last dang.Value
	dang.HoistLocalTypes(context.Background(), forms, s.valueScope)
	for _, node := range forms {
		last, err = dang.EvalNode(context.Background(), s.valueScope, node)
		require.NoError(s.t, err)
	}
	return last, nil
}

func TestReplReassignsTopLevelBindings(t *testing.T) {
	s := newTestReplSession(t)
	_, err := s.enter("let total = 0")
	require.NoError(t, err)
	_, err = s.enter("total = 5")
	require.NoError(t, err)
	_, err = s.enter("[1, 2].each { x => total += x }")
	require.NoError(t, err)
	v, err := s.enter("total")
	require.NoError(t, err)
	require.Equal(t, "8", dang.Repr(v))
}

func TestReplRejectsFunctionWritesToSessionBindings(t *testing.T) {
	s := newTestReplSession(t)
	_, err := s.enter("let count = 0")
	require.NoError(t, err)
	_, err = s.enter("bump: Int! { count += 1; count }")
	require.ErrorContains(t, err, "cannot assign to `count`: top-level bindings are immutable")
}

// Types declared below the top level capture nothing at the prompt either,
// and decode wherever their values reach, even before their declaration ran.
func TestReplLocalTypesCaptureNothing(t *testing.T) {
	s := newTestReplSession(t)
	_, err := s.enter("type Outer { pub level: Int! = 1; type Inner { doubled: Int! { level * 2 } } }")
	require.ErrorContains(t, err, "Inner cannot use `level`")

	_, err = s.enter(`type Box { type Inner { value: String! }; make: Inner! { Inner(value: "built") } }`)
	require.NoError(t, err)
	_, err = s.enter("type Holder { pub inner = Box.make }")
	require.NoError(t, err)
	v, err := s.enter(`let held: Holder! = JSON.decode("{\"inner\": {\"value\": \"decoded\"}}"); held.inner.value`)
	require.NoError(t, err)
	require.Equal(t, `"decoded"`, dang.Repr(v))
}

func TestReplChecksFieldPurity(t *testing.T) {
	s := newTestReplSession(t)
	_, err := s.enter("both(&block(x: Int!): Int!): Int! { let r = {{a: block(1), b: block(2)}}; r.a + r.b }")
	require.NoError(t, err)

	// The requirement inferred in an earlier entry is checked at a later
	// entry's call site.
	_, err = s.enter("run: Int! { let n = 0; both { x => n += x; n } }")
	require.ErrorContains(t, err, "`both` calls `block` inside {{ }}")

	v, err := s.enter("both { x => x * 10 }")
	require.NoError(t, err)
	require.Equal(t, "30", dang.Repr(v))

	_, err = s.enter("run2: Int! { let n = 0; {{a: if (true) { n += 1; n } else { 0 }}}.a }")
	require.ErrorContains(t, err, "{{ }} fields run concurrently; this assignment writes `n`")
}

// Each entry infers with a new fresher, whose variables must still be none of
// those that earlier entries left behind.
func TestReplGenericCallsAcrossEntries(t *testing.T) {
	s := newTestReplSession(t)
	_, err := s.enter("f(v: b, w: [String!]!): Int! { w.length }")
	require.NoError(t, err)
	_, err = s.enter("let empty = []")
	require.NoError(t, err)
	v, err := s.enter("f(1, w: empty)")
	require.NoError(t, err)
	require.Equal(t, "0", dang.Repr(v))

	_, err = s.enter("let p = null\nlet ys = [null]")
	require.NoError(t, err)
	v, err = s.enter(`ys.reduce("") { acc, x => let n: Int! = x ?? 1; acc + "!" }`)
	require.NoError(t, err)
	require.Equal(t, `"!"`, dang.Repr(v))

	_, err = s.enter("nonez(x: Int! = 1): [z]! { [] }")
	require.NoError(t, err)
	v, err = s.enter("[p, nonez(1)]")
	require.NoError(t, err)
	require.Equal(t, "[null, []]", dang.Repr(v))
}

// Re-entering a binding redefines it as it was, though the new entry types it
// with variables of its own.
func TestReplRedefinesGenericBindings(t *testing.T) {
	s := newTestReplSession(t)
	for _, entry := range []string{
		"let xs = []", "let xs = []",
		"let p = null", "let p = null",
		"let u = if (true) { [] } else { 1 }", "let u = if (true) { [] } else { 1 }",
	} {
		_, err := s.enter(entry)
		require.NoError(t, err, entry)
	}
}

// A binding's type, pinned by a use in one entry, stays pinned in later ones,
// after the earlier entries' syntax is gone.
func TestReplKeepsPinnedBindingTypes(t *testing.T) {
	s := newTestReplSession(t)
	_, err := s.enter("let xs = []")
	require.NoError(t, err)
	_, err = s.enter("xs += [1]")
	require.NoError(t, err)
	runtime.GC()
	runtime.GC()
	_, err = s.enter("let strs: [String!]! = xs")
	require.ErrorContains(t, err, "cannot use [Int!]! as [String!]!")
	v, err := s.enter("xs")
	require.NoError(t, err)
	require.Equal(t, "[1]", dang.Repr(v))
}

// A binding's variable bound to another entry's (a block call's here) keeps
// that one alive, so it can still be pinned after the entry is gone.
func TestReplKeepsLinkedWeakVariables(t *testing.T) {
	s := newTestReplSession(t)
	_, err := s.enter("let xs = []")
	require.NoError(t, err)
	_, err = s.enter("[[]].each { y => let z = y + xs }")
	require.NoError(t, err)
	runtime.GC()
	runtime.GC()
	runtime.GC()
	_, err = s.enter("xs += [1]")
	require.NoError(t, err)
	_, err = s.enter(`xs += ["s"]`)
	require.ErrorContains(t, err, "cannot use [String!]! as [Int!]!")
}

// Redefining a declaration in a later entry rebinds it: calls run the new
// body, as the checker types them.
func TestReplRedefinitions(t *testing.T) {
	s := newTestReplSession(t)
	mustEnter := func(input string) dang.Value {
		t.Helper()
		v, err := s.enter(input)
		require.NoError(t, err)
		return v
	}

	mustEnter("double(x: Int!): Int! { x * 2 }")
	require.Equal(t, "8", dang.Repr(mustEnter("double(4)")))
	mustEnter("double(count: Int): Int { count }")
	require.Equal(t, "4", dang.Repr(mustEnter("double(4)")))

	mustEnter("let n = 1")
	mustEnter("let n = 2")
	require.Equal(t, "2", dang.Repr(mustEnter("n")))
	mustEnter(`let n = "two"`)
	require.Equal(t, `"two"`, dang.Repr(mustEnter("n")))

	mustEnter("type P { x: Int! = 1; get: Int! { x } }")
	require.Equal(t, "1", dang.Repr(mustEnter("P.get")))
	mustEnter(`type P { y: String! = "y"; get: String! { y + "!" } }`)
	require.Equal(t, `"y!"`, dang.Repr(mustEnter("P.get")))
	require.Equal(t, `"y"`, dang.Repr(mustEnter("P.y")))
	_, err := s.enter("P.x")
	require.ErrorContains(t, err, `field "x" not found`)

	// Earlier functions call the new definition too, so it must fit where
	// the old one did, unless they are redefined along with it.
	mustEnter("half(x: Int!): Int! { x / 2 }")
	mustEnter("quarter(x: Int!): Int! { half(half(x)) }")
	_, err = s.enter("half(x: Int!): Int { null }")
	require.ErrorContains(t, err, "redefining `half` changes its type from (x: Int!): Int! to (x: Int!): Int, but `quarter`, declared earlier, still uses it")
	require.Equal(t, "2", dang.Repr(mustEnter("quarter(8)")))
	mustEnter("half(x: Int!): Int! { x / 2 + 1 }")
	require.Equal(t, "3", dang.Repr(mustEnter("quarter(8)")))
	mustEnter("half(x: Int!): Int { null }\nquarter(x: Int!): Int { x }")
	require.Equal(t, "8", dang.Repr(mustEnter("quarter(8)")))

	mustEnter("type Box { v: Int! = 1 }")
	mustEnter("unbox: Int! { Box.v }")
	_, err = s.enter(`type Box { w: String! = "w" }`)
	require.ErrorContains(t, err, "redefining type `Box` would leave `unbox` using the old one")
	require.Equal(t, "1", dang.Repr(mustEnter("unbox")))
}
