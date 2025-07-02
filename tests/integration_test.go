package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/vito/dash/introspection"
	"github.com/vito/dash/pkg/dash"
	"go.opentelemetry.io/otel/attribute"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

func TestIntegration(tT *testing.T) {
	// Connect to Dagger for testing
	ctx := context.Background()

	t := testctx.New(tT,
		oteltest.WithTracing[*testing.T](oteltest.TraceConfig[*testing.T]{
			Attributes: []attribute.KeyValue{
				attribute.Bool(telemetry.UIRevealAttr, true),
			},
		}),
		oteltest.WithLogging[*testing.T](),
	)

	dag, err := dagger.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to Dagger: %v", err)
	}
	defer dag.Close()

	// Get schema
	schema, err := introspectSchema(ctx, dag)
	if err != nil {
		t.Fatalf("Failed to introspect schema: %v", err)
	}

	// Find all test_*.dash files or test_* packages
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
				_, err = dash.RunDir(ctx, dag.GraphQLClient(), schema, testFileOrDir, false)
			} else {
				err = dash.RunFile(ctx, dag.GraphQLClient(), schema, testFileOrDir, false)
			}
			if err != nil {
				t.Errorf("Test %s failed: %v", testFileOrDir, err)
			}
		})
	}
}

// introspectSchema is a helper function to get the GraphQL schema
func introspectSchema(ctx context.Context, dag *dagger.Client) (*introspection.Schema, error) {
	var introspectionResp introspection.Response
	err := dag.Do(ctx, &dagger.Request{
		Query:  introspection.Query,
		OpName: "IntrospectionQuery",
	}, &dagger.Response{
		Data: &introspectionResp,
	})
	if err != nil {
		return nil, err
	}
	return introspectionResp.Schema, nil
}
