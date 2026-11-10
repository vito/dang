package tests

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Showing a GraphQL object — toString, List.join, interpolation, print —
// renders the query chain the handle holds, so it sends nothing, not even
// for a mutation's result.
func (DangSuite) TestDisplayGraphQLSendsNoQueries(ctx context.Context, t *testctx.T) {
	run := func(t *testctx.T, source string) []string {
		return runRecordingQueries(ctx, t, source)
	}

	t.Run("shows a query chain", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let u = primaryUser
print(u)
print(`+"`shown: ${u}`"+`)
print([u, secondaryUser].join(", "))
assert { toString(u) == "<User: primaryUser>" }
`)
		require.Empty(t, queries)
	})

	t.Run("shows a mutation's result without sending it", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let n = Mutation.touchNode(id: "Post:1")
print(n)
print(`+"`shown: ${n}`"+`)
print([n].join(", "))
assert { toString(n) == "<Node: mutation.touchNode(id: …)>" }
`)
		require.Empty(t, queries)
	})

	t.Run("shows a handle loaded by ID as its loader", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let u = favoriteUserID
print(u)
assert { toString(u) == "<User: node(id: …)>" }
`)
		// Only the field's own query, which fetches the ID to load.
		require.Len(t, queries, 1)
		require.True(t, strings.Contains(queries[0], "favoriteUserID"), queries[0])
	})
}
