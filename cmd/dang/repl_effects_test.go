package main

import (
	"context"
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
