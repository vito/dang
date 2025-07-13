package tests

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
	"github.com/vito/sprout/introspection"
	"github.com/vito/sprout/pkg/ioctx"
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
			ctx = ioctx.StdoutToContext(ctx, NewTWriter(t))
			ctx = ioctx.StderrToContext(ctx, NewTWriter(t))

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

// tWriter is a writer that writes to testing.T
type tWriter struct {
	t   testing.TB
	buf bytes.Buffer
	mu  sync.Mutex
}

// NewTWriter creates a new TWriter
func NewTWriter(t testing.TB) io.Writer {
	tw := &tWriter{t: t}
	t.Cleanup(tw.flush)
	return tw
}

// Write writes data to the testing.T
func (tw *tWriter) Write(p []byte) (n int, err error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	tw.t.Helper()

	if n, err = tw.buf.Write(p); err != nil {
		return n, err
	}

	for {
		line, err := tw.buf.ReadBytes('\n')
		if err == io.EOF {
			// If we've reached the end of the buffer, write it back, because it doesn't have a newline
			tw.buf.Write(line)
			break
		}
		if err != nil {
			return n, err
		}

		tw.t.Log(strings.TrimSuffix(string(line), "\n"))
	}
	return n, nil
}

func (tw *tWriter) flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.t.Log(tw.buf.String())
}
