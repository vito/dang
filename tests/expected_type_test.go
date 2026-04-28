package tests

import (
	"context"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/tests/gqlserver"
)

func (DangSuite) TestExpectedTypeDirectivesFromIntrospection(ctx context.Context, t *testctx.T) {
	testGraphQLServer, err := gqlserver.StartServer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)
	ctx = ioctx.StdoutToContext(ctx, NewTWriter(t))
	ctx = dang.ContextWithImportConfigs(ctx, dang.ImportConfig{
		Name:   "Test",
		Client: client,
	})

	require.NoError(t, dang.RunFile(ctx, "test_expected_type.dang", false))
}
