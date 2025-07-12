package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
	"github.com/vito/sprout/introspection"
	"github.com/vito/sprout/pkg/sprout"
	"github.com/vito/sprout/tests/gqlserver"
	"go.opentelemetry.io/otel/attribute"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func TestIntegration(tT *testing.T) {
	t := testctx.New(tT,
		oteltest.WithTracing[*testing.T](oteltest.TraceConfig[*testing.T]{
			Attributes: []attribute.KeyValue{
				attribute.Bool(telemetry.UIRevealAttr, true),
			},
		}),
		oteltest.WithLogging[*testing.T](),
	)

	testGraphQLServer, err := gqlserver.StartServer()
	require.NoError(t, err)
	t.Cleanup(func() { testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)

	// Get schema
	schema, err := introspectSchema(t.Context(), client)
	if err != nil {
		t.Fatalf("Failed to introspect schema: %v", err)
	}

	// Find all test_*.sp files or test_* packages
	paths, err := filepath.Glob("test_*")
	if err != nil {
		t.Fatalf("Failed to find test files: %v", err)
	}

	if len(paths) == 0 {
		t.Skip("No test_* files or directories found")
	}

	// Run each test file in parallel
	for _, testFileOrDir := range paths {
		t.Run(filepath.Base(testFileOrDir), func(ctx context.Context, t *testctx.T) {
			// t.Parallel()
			fi, err := os.Stat(testFileOrDir)
			if err != nil {
				t.Errorf("Failed to stat test file or directory %s: %v", testFileOrDir, err)
				return
			}
			if fi.IsDir() {
				_, err = sprout.RunDir(ctx, client, schema, testFileOrDir, false)
			} else {
				err = sprout.RunFile(ctx, client, schema, testFileOrDir, false)
			}
			if err != nil {
				t.Errorf("Test %s failed: %v", testFileOrDir, err)
			}
		})
	}
}

// introspectSchema is a helper function to get the GraphQL schema
func introspectSchema(ctx context.Context, client graphql.Client) (*introspection.Schema, error) {
	var introspectionResp introspection.Response
	err := client.MakeRequest(ctx, &graphql.Request{
		Query:  introspection.Query,
		OpName: "IntrospectionQuery",
	}, &graphql.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, err
	}
	return introspectionResp.Schema, nil
}
