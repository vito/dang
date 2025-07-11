package tests

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/vito/sprout/introspection"
	"github.com/vito/sprout/pkg/ioctx"
	"github.com/vito/sprout/pkg/sprout"
	"gotest.tools/v3/golden"
)

// TestErrorMessages tests that error messages match golden files
func TestErrorMessages(t *testing.T) {
	errorsDir := filepath.Join("errors")

	// Find all .sp files in the errors directory
	bindFiles, err := filepath.Glob(filepath.Join(errorsDir, "*.sp"))
	if err != nil {
		t.Fatalf("Failed to find .sp files: %v", err)
	}

	if len(bindFiles) == 0 {
		t.Fatal("No .sp test files found in tests/errors/")
	}

	// Connect to Dagger (required for Bind execution)
	ctx := t.Context()
	dag, err := dagger.Connect(ctx)
	if err != nil {
		t.Fatalf("Failed to connect to Dagger: %v", err)
	}
	defer dag.Close()

	// Introspect the GraphQL schema (required for Bind execution)
	schema, err := introspectSchema(ctx, dag)
	if err != nil {
		t.Fatalf("Failed to introspect schema: %v", err)
	}

	for _, bindFile := range bindFiles {
		bindFile := bindFile // capture loop variable

		// Extract test name from filename
		testName := strings.TrimSuffix(filepath.Base(bindFile), ".sp")

		t.Run(testName, func(t *testing.T) {
			output := runBindFile(t.Context(), dag, schema, bindFile)

			// Compare with golden file
			golden.Assert(t, output, testName+".golden")
		})
	}
}

// runBindFile runs a Bind file and captures combined stdout/stderr
func runBindFile(ctx context.Context, dag *dagger.Client, schema *introspection.Schema, bindFile string) string {
	// Create buffers to capture output
	var stdout, stderr bytes.Buffer

	// Set up context with captured stdout/stderr
	ctx = ioctx.StdoutToContext(ctx, &stdout)
	ctx = ioctx.StderrToContext(ctx, &stderr)

	// Run the Bind file
	err := sprout.RunFile(ctx, dag.GraphQLClient(), schema, bindFile, false)

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
