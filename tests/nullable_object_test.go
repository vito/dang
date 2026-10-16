package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/ioctx"
	"github.com/vito/dang/v2/tests/gqlserver"
)

// recordingClient records every GraphQL query sent through it.
type recordingClient struct {
	inner graphql.Client

	mu      sync.Mutex
	queries []string
}

func (c *recordingClient) MakeRequest(ctx context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.mu.Lock()
	c.queries = append(c.queries, req.Query)
	c.mu.Unlock()
	return c.inner.MakeRequest(ctx, req, resp)
}

func (c *recordingClient) Queries() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.queries...)
}

// Nullable object fields are probed for null with a `__typename` query when
// called, except where the probe would be redundant (a `{{ }}` projection
// reveals the null itself) or harmful (re-sending a mutation). These tests pin
// down exactly which requests go out. See vito/dang#186.
func (DangSuite) TestNullableObjectFieldQueries(ctx context.Context, t *testctx.T) {
	run := func(t *testctx.T, source string) []string {
		cfg, err := gqlserver.ImportConfig("Test")
		require.NoError(t, err)
		client := &recordingClient{inner: cfg.Client}
		cfg.Client = client

		path := filepath.Join(t.TempDir(), "test.dang")
		require.NoError(t, os.WriteFile(path, []byte("import Test\n\n"+source), 0o600))

		ctx := ioctx.StdoutToContext(ctx, NewTWriter(t))
		ctx = dang.ContextWithImportConfigs(ctx, cfg)
		require.NoError(t, dang.RunFile(ctx, path, false))
		return client.Queries()
	}

	t.Run("probes a nullable object field", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let m = user(id: "1").manager
assert { m == null }
`)
		require.Equal(t, []string{
			`query Query {user(id:"1"){__typename}}`,
			`query Query {user(id:"1"){manager{__typename}}}`,
		}, queries)
	})

	t.Run("skips the probe for a projection", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let r = user(id: "2").manager.{{name}}
assert { r.name == "John Doe" }
let n = user(id: "1").manager.{{name}}
assert { n == null }
`)
		require.Equal(t, []string{
			// The receiver of .manager is still probed...
			`query Query {user(id:"2"){__typename}}`,
			// ...but manager itself goes straight to the projection.
			`query Query {user(id:"2"){manager{name}}}`,
			`query Query {user(id:"1"){__typename}}`,
			`query Query {user(id:"1"){manager{name}}}`,
		}, queries)
	})

	t.Run("skips the probe for a projected root field", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
assert { maybeUser(id: "nope").{{name}} == null }
`)
		require.Equal(t, []string{
			`query Query {maybeUser(id:"nope"){name}}`,
		}, queries)
	})

	t.Run("probes argument expressions of a projection", func(ctx context.Context, t *testctx.T) {
		// Only the call feeding the projection skips its probe; a nullable
		// object field evaluated in its arguments is still resolved first.
		queries := run(t, `
let r = user(id: (user(id: "2").manager ?? primaryUser).id).{{name}}
assert { r.name == "John Doe" }
`)
		require.Contains(t, queries, `query Query {user(id:"2"){manager{__typename}}}`)
		require.Equal(t, `query Query {user(id:"1"){name}}`, queries[len(queries)-1])
	})

	t.Run("does not probe mutations", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let u = Mutation.updateUser(id: "2", input: UpdateUserInput(name: "Jane Smith"))
assert { u.name == "Jane Smith" }
`)
		require.Len(t, queries, 1)
		require.True(t, strings.HasPrefix(queries[0], "mutation "), queries[0])
		require.NotContains(t, queries[0], "__typename")
	})
}
