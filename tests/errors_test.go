package tests

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/tests/gqlserver"
	"gotest.tools/v3/golden"
)

// TestErrorMessages tests that error messages match golden files
func (DangSuite) TestErrorMessages(ctx context.Context, t *testctx.T) {
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
	t.Cleanup(func() { _ = testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)

	for _, dangFile := range dangFiles {
		// Extract test name from filename
		testName := strings.TrimSuffix(filepath.Base(dangFile), ".dang")

		t.Run(testName, func(ctx context.Context, t *testctx.T) {
			output := runDangFile(ctx, t, client, dangFile)

			// Compare with golden file
			golden.Assert(t, output, testName+".golden")
		})
	}
}

// runDangFile runs a Dang file and captures combined stdout/stderr
func (DangSuite) TestRunDirControlFlowSourceErrors(ctx context.Context, t *testctx.T) {
	dir, err := os.MkdirTemp("", "dang-rundir-control-flow-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "main.dang")
	err = os.WriteFile(path, []byte(`let saved(x: Int!): Int! { x }

pub store(&block(x: Int!): Int!): Int! {
  saved = block
  0
}

pub makeReturner: Int! {
  store { x =>
    return x
  }
  0
}

pub result = {
  makeReturner
  saved(1)
}
`), 0644)
	require.NoError(t, err)

	_, err = dang.RunDir(ctx, dir, false)
	require.Error(t, err)

	message := err.Error()
	require.Contains(t, message, "Error:")
	require.Contains(t, message, "return from expired function")
	require.Contains(t, message, "return x")
	require.Contains(t, message, path)
}

func runDangFile(ctx context.Context, t *testctx.T, client graphql.Client, dangFile string) string {
	// Create buffers to capture output
	var stdout, stderr bytes.Buffer

	// Set up context with captured stdout/stderr
	ctx = ioctx.StdoutToContext(ctx, &stdout)
	ctx = ioctx.StderrToContext(ctx, &stderr)

	ctx = dang.ContextWithImportConfigs(ctx,
		dang.ImportConfig{
			Name:   "Test",
			Client: client,
		},
		dang.ImportConfig{
			Name:   "Other",
			Client: client, // Same client/schema, but different import name
		},
	)

	// Run the Dang file
	err := dang.RunFile(ctx, dangFile, false)
	require.Error(t, err, "Test expects an error, but did not error.")

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
