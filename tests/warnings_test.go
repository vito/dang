package tests

import (
	"bytes"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/ioctx"
	"github.com/vito/dang/v2/tests/gqlserver"
	"gotest.tools/v3/golden"

	"context"
)

// TestWarningMessages runs each tests/warnings/*.dang fixture, expects it to
// SUCCEED, and golden-compares the combined output — stdout followed by
// stderr, where inference warnings land. This is the harness for non-fatal
// diagnostics: the laziness warnings on rescue point at code that type
// checks and runs, so the fixtures cannot live under tests/errors/.
func (DangSuite) TestWarningMessages(ctx context.Context, t *testctx.T) {
	dangFiles, err := filepath.Glob(filepath.Join("warnings", "*.dang"))
	require.NoError(t, err)
	require.NotEmpty(t, dangFiles, "No .dang test files found in tests/warnings/")

	testGraphQLServer, err := gqlserver.StartServer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)

	for _, dangFile := range dangFiles {
		testName := strings.TrimSuffix(filepath.Base(dangFile), ".dang")

		t.Run(testName, func(ctx context.Context, t *testctx.T) {
			var stdout, stderr bytes.Buffer
			ctx = ioctx.StdoutToContext(ctx, &stdout)
			ctx = ioctx.StderrToContext(ctx, &stderr)

			ctx = dang.ContextWithImportConfigs(ctx,
				dang.ImportConfig{
					Name:   "Test",
					Client: client,
				},
			)

			err := dang.RunFile(ctx, dangFile, false)
			require.NoError(t, err, "Warning fixtures must run successfully; got: %v\nstderr:\n%s", err, stderr.String())
			require.NotEmpty(t, stderr.String(), "Warning fixtures must produce at least one warning")

			var combined bytes.Buffer
			combined.Write(stdout.Bytes())
			combined.Write(stderr.Bytes())

			golden.Assert(t, combined.String(), testName+".golden")
		})
	}
}
