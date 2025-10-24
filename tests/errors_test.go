package tests

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/introspection"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/tests/gqlserver"
	"go.opentelemetry.io/otel/attribute"
	"gotest.tools/v3/golden"
)

// TestErrorMessages tests that error messages match golden files
func TestErrorMessages(tT *testing.T) {
	t := testctx.New(tT,
		oteltest.WithTracing[*testing.T](oteltest.TraceConfig[*testing.T]{
			Attributes: []attribute.KeyValue{
				attribute.Bool(telemetry.UIRevealAttr, true),
			},
		}),
		oteltest.WithLogging[*testing.T](),
	)

	errorsDir := filepath.Join("errors")

	// Find all .dang files in the errors directory
	dangFiles, err := filepath.Glob(filepath.Join(errorsDir, "*.dang"))
	if err != nil {
		t.Fatalf("Failed to find .dang files: %v", err)
	}

	if len(dangFiles) == 0 {
		t.Fatal("No .dang test files found in tests/errors/")
	}

	testGraphQLServer, err := gqlserver.StartServer()
	require.NoError(t, err)
	t.Cleanup(func() { testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)

	// Introspect the GraphQL schema (required for Dang execution)
	schema, err := introspectSchema(t.Context(), client)
	if err != nil {
		t.Fatalf("Failed to introspect schema: %v", err)
	}

	for _, dangFile := range dangFiles {
		// Extract test name from filename
		testName := strings.TrimSuffix(filepath.Base(dangFile), ".dang")

		t.Run(testName, func(ctx context.Context, t *testctx.T) {
			output := runDangFile(t.Context(), client, schema, dangFile)

			// Compare with golden file
			golden.Assert(t, output, testName+".golden")
		})
	}
}

// runDangFile runs a Dang file and captures combined stdout/stderr
func runDangFile(ctx context.Context, client graphql.Client, schema *introspection.Schema, dangFile string) string {
	// Create buffers to capture output
	var stdout, stderr bytes.Buffer

	// Set up context with captured stdout/stderr
	ctx = ioctx.StdoutToContext(ctx, &stdout)
	ctx = ioctx.StderrToContext(ctx, &stderr)

	// Run the Dang file
	err := dang.RunFile(ctx, client, schema, dangFile, false)

	// Combine stdout and stderr output
	var combined bytes.Buffer
	combined.Write(stdout.Bytes())
	if err != nil {
		// Write error to stderr buffer, then add to combined output
		stderr.WriteString(err.Error() + "\n")
	}
	combined.Write(stderr.Bytes())

	return combined.String()
}
