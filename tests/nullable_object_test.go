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

// runRecordingQueries runs source against the test GraphQL server, imported
// as Test, and returns every query it sent, in order.
func runRecordingQueries(ctx context.Context, t *testctx.T, source string) []string {
	cfg, err := gqlserver.ImportConfig("Test")
	require.NoError(t, err)
	client := &recordingClient{inner: cfg.Client}
	cfg.Client = client

	path := filepath.Join(t.TempDir(), "test.dang")
	require.NoError(t, os.WriteFile(path, []byte("import Test\n\n"+source), 0o600))

	ctx = ioctx.StdoutToContext(ctx, NewTWriter(t))
	ctx = dang.ContextWithImportConfigs(ctx, cfg)
	require.NoError(t, dang.RunFile(ctx, path, false))
	return client.Queries()
}

// A handle that may be null — a nullable object field, or anything selected
// off one — is probed for null only when it escapes the chain: where it is
// selected from, the leaf or `{{ }}` projection that runs the chain reveals
// the null itself. A probe fetches `id` and `__typename`, and the handle is
// then loaded by ID. Mutations are never probed, since that would re-send
// them. These tests pin down exactly which requests go out. See
// vito/dang#186.
func (DangSuite) TestNullableObjectFieldQueries(ctx context.Context, t *testctx.T) {
	run := func(t *testctx.T, source string) []string {
		return runRecordingQueries(ctx, t, source)
	}

	t.Run("sends one query for a leaf off a nullable field", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
assert { user(id: "User:1").name == "John Doe" }
`)
		require.Equal(t, []string{
			`query Query {user(id:"User:1"){name}}`,
		}, queries)
	})

	t.Run("resolves a leaf off a null field to null", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
assert { maybeUser(id: "nope").name == null }
assert { maybeUser(id: "nope").emails == null }
`)
		require.Equal(t, []string{
			`query Query {maybeUser(id:"nope"){name}}`,
			`query Query {maybeUser(id:"nope"){emails}}`,
		}, queries)
	})

	t.Run("resolves a chain of nullable fields with one query", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
assert { user(id: "User:2").manager.name == "John Doe" }
assert { user(id: "User:2").manager.manager.name == null }
`)
		require.Equal(t, []string{
			`query Query {user(id:"User:2"){manager{name}}}`,
			`query Query {user(id:"User:2"){manager{manager{name}}}}`,
		}, queries)
	})

	t.Run("probes an escaping handle and loads it by ID", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let u = maybeUser(id: "User:1")
assert { u != null }
assert { u.name == "John Doe" }
`)
		require.Equal(t, []string{
			`query Query {maybeUser(id:"User:1"){id __typename}}`,
			// Later selections start from the loader, not the original chain.
			`query Query {node(id:"User:1"){... on User{name}}}`,
		}, queries)
	})

	t.Run("probes an escaping null handle", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let m = user(id: "User:1").manager
assert { m == null }
`)
		require.Equal(t, []string{
			`query Query {user(id:"User:1"){manager{id __typename}}}`,
		}, queries)
	})

	t.Run("probes an escaping deep chain once", func(ctx context.Context, t *testctx.T) {
		// posts is non-null, but selected off an unprobed nullable field, so
		// it may still be null. It has no id, so the probe is __typename.
		queries := run(t, `
let none = user(id: "User:1").manager.posts
assert { none == null }
let some = user(id: "User:2").manager.posts
assert { some != null }
assert { some.pageInfo.hasNextPage == false }
`)
		require.Equal(t, []string{
			`query Query {user(id:"User:1"){manager{posts{__typename}}}}`,
			`query Query {user(id:"User:2"){manager{posts{__typename}}}}`,
			`query Query {user(id:"User:2"){manager{posts{pageInfo{hasNextPage}}}}}`,
		}, queries)
	})

	t.Run("skips the probe for a projection", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let r = user(id: "User:2").manager.{{name}}
assert { r.name == "John Doe" }
let n = user(id: "User:1").manager.{{name}}
assert { n == null }
`)
		require.Equal(t, []string{
			`query Query {user(id:"User:2"){manager{name}}}`,
			`query Query {user(id:"User:1"){manager{name}}}`,
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

	t.Run("probes argument expressions", func(ctx context.Context, t *testctx.T) {
		// Only the call being selected from skips its probe; a nullable object
		// field evaluated in its arguments is still resolved first.
		queries := run(t, `
let r = user(id: (user(id: "User:2").manager ?? primaryUser).id).{{name}}
assert { r.name == "John Doe" }
`)
		require.Equal(t, []string{
			`query Query {user(id:"User:2"){manager{id __typename}}}`,
			`query Query {node(id:"User:1"){... on User{id}}}`,
			`query Query {user(id:"User:1"){name}}`,
		}, queries)
	})

	t.Run("passes a probed handle as an ID for free", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
assert { userName(user: maybeUser(id: "User:2")!) == "Jane Smith" }
`)
		require.Equal(t, []string{
			`query Query {maybeUser(id:"User:2"){id __typename}}`,
			`query Query {userName(user:"User:2")}`,
		}, queries)
	})

	t.Run("narrows a nullable interface field to its concrete type", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let describe(n: Node): String! {
  case (n) {
    u: User => "user " + u.name
    p: Post => "post " + p.title
    else => "unknown"
  }
}

assert { describe(node(id: "User:1")) == "user John Doe" }
assert { describe(node(id: "Post:3")) == "post Third Post" }
assert { node(id: "nope") == null }
`)
		require.Equal(t, []string{
			`query Query {node(id:"User:1"){id __typename}}`,
			`query Query {node(id:"User:1"){... on User{name}}}`,
			`query Query {node(id:"Post:3"){id __typename}}`,
			`query Query {node(id:"Post:3"){... on Post{title}}}`,
			`query Query {node(id:"nope"){id __typename}}`,
		}, queries)
	})

	t.Run("does not probe mutations", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let u = Mutation.updateUser(id: "User:2", input: UpdateUserInput(name: "Jane Smith"))
assert { u.name == "Jane Smith" }
`)
		require.Len(t, queries, 1)
		require.True(t, strings.HasPrefix(queries[0], "mutation "), queries[0])
		require.NotContains(t, queries[0], "__typename")
	})
}

// `case` matches a GraphQL interface or union handle by its concrete type. A
// handle that only knows its abstract type, such as a non-null interface
// field, which is never probed for null, is resolved with the same `{ id
// __typename }` query as the null probe, and loaded by ID; but only when a
// type pattern needs it.
func (DangSuite) TestCaseOnAbstractHandleQueries(ctx context.Context, t *testctx.T) {
	run := func(t *testctx.T, source string) []string {
		return runRecordingQueries(ctx, t, source)
	}

	t.Run("resolves a non-null interface field once", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let r = case (favoriteNode) {
  u: User => "user " + u.name
  p: Post => "post " + p.title + " by " + p.author.name
}
assert { r == "post Second Post by Jane Smith" }
`)
		require.Equal(t, []string{
			`query Query {favoriteNode{id __typename}}`,
			// The arm selects through the loader, not the original chain.
			`query Query {node(id:"Post:2"){... on Post{title}}}`,
			`query Query {node(id:"Post:2"){... on Post{author{name}}}}`,
		}, queries)
	})

	t.Run("resolves a non-null union field once", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let r = case (featured) {
  u: User => u.name
  p: Post => p.title
}
assert { r == "Jane Smith" }
`)
		require.Equal(t, []string{
			`query Query {featured{__typename ... on User { id } ... on Post { id }}}`,
			`query Query {node(id:"User:2"){... on User{name}}}`,
		}, queries)
	})

	t.Run("does not resolve without type patterns", func(ctx context.Context, t *testctx.T) {
		// Value clauses compare against the handle itself.
		queries := run(t, `
let f = favoriteNode
let r = case (f) {
  f => "same"
  else => "other"
}
assert { r == "same" }
`)
		require.Empty(t, queries)
	})

	t.Run("does not resolve when the first pattern is the abstract type", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let r = case (favoriteNode) {
  n: Node => n.id
}
assert { r == "Post:2" }
`)
		require.Equal(t, []string{
			`query Query {favoriteNode{id}}`,
		}, queries)
	})

	t.Run("does not resolve an already concrete handle", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let describe(n: Node): String! {
  case (n) {
    u: User => "user " + u.name
    p: Post => "post " + p.title
    else => "unknown"
  }
}

assert { describe(favoriteNode.{{... on Post!}}) == "post Second Post" }
assert { describe(node(id: "User:1")) == "user John Doe" }
`)
		require.Equal(t, []string{
			`query Query {favoriteNode{__typename}}`,
			`query Query {favoriteNode{... on Post{title}}}`,
			// The nullable node field was probed for null already.
			`query Query {node(id:"User:1"){id __typename}}`,
			`query Query {node(id:"User:1"){... on User{name}}}`,
		}, queries)
	})

	t.Run("does not resolve a mutation-returned handle", func(ctx context.Context, t *testctx.T) {
		queries := run(t, `
let r = case (Mutation.touchNode(id: "Post:1")) {
  p: Post => "post " + p.title
  n: Node => "node " + toString(n.id)
}
assert { r == "node Post:1" }
`)
		require.Len(t, queries, 1)
		require.True(t, strings.HasPrefix(queries[0], "mutation "), queries[0])
		require.NotContains(t, queries[0], "__typename")
	})
}
