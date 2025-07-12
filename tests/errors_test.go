package tests

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vito/sprout/introspection"
	"github.com/vito/sprout/pkg/ioctx"
	"github.com/vito/sprout/pkg/sprout"
	"github.com/vito/sprout/tests/gqlserver"
	"gotest.tools/v3/golden"
)

// TestErrorMessages tests that error messages match golden files
func TestErrorMessages(t *testing.T) {
	errorsDir := filepath.Join("errors")

	// Find all .sp files in the errors directory
	sproutFiles, err := filepath.Glob(filepath.Join(errorsDir, "*.sp"))
	if err != nil {
		t.Fatalf("Failed to find .sp files: %v", err)
	}

	if len(sproutFiles) == 0 {
		t.Fatal("No .sp test files found in tests/errors/")
	}

	testGraphQLServer, err := gqlserver.StartServer()
	require.NoError(t, err)
	t.Cleanup(func() { testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)

	// Introspect the GraphQL schema (required for Sprout execution)
	schema, err := introspectSchema(t.Context(), client)
	if err != nil {
		t.Fatalf("Failed to introspect schema: %v", err)
	}

	for _, sproutFile := range sproutFiles {
		// Extract test name from filename
		testName := strings.TrimSuffix(filepath.Base(sproutFile), ".sp")

		t.Run(testName, func(t *testing.T) {
			output := runSproutFile(t.Context(), client, schema, sproutFile)

			// Compare with golden file
			golden.Assert(t, output, testName+".golden")
		})
	}
}

// runSproutFile runs a Sprout file and captures combined stdout/stderr
func runSproutFile(ctx context.Context, client graphql.Client, schema *introspection.Schema, sproutFile string) string {
	// Create buffers to capture output
	var stdout, stderr bytes.Buffer

	// Set up context with captured stdout/stderr
	ctx = ioctx.StdoutToContext(ctx, &stdout)
	ctx = ioctx.StderrToContext(ctx, &stderr)

	// Run the Sprout file
	err := sprout.RunFile(ctx, client, schema, sproutFile, false)

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
