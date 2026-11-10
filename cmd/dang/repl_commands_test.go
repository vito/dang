package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vito/dang/v2/pkg/dang"
)

func TestTypeCommandShowsBounds(t *testing.T) {
	typeScope, _ := dang.BuildScopesFromImports("", nil)
	r := &replComponent{ctx: context.Background(), typeScope: typeScope}

	typeOf := func(expr string) string {
		e := newReplEntry(":type " + expr)
		r.typeCommand(e, []string{expr})
		return e.logs.String()
	}

	require.Contains(t, typeOf("[].sum"), "Type: [a: Numeric] a")
	require.Contains(t, typeOf("[].sort"), "Type: [a: Ordered] [a!]!")

	// A selection instantiates the method's variables; its signature still
	// shows their bounds.
	sorted := typeOf("[1].sort")
	require.Contains(t, sorted, "Type: [Int!]!")
	require.Contains(t, sorted, "Method: List.sort[a: Ordered](descending: Boolean = false): [a]!")
	require.Contains(t, typeOf("[1.5].sum"), "Method: List.sum[a: Numeric]: a")
	require.NotContains(t, typeOf(`["a"].length`), "Method:")

	require.NotContains(t, typeOf("[]"), "forall")
}
