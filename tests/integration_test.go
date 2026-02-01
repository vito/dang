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

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/dang"
	"github.com/vito/dang/pkg/introspection"
	"github.com/vito/dang/pkg/ioctx"
	"github.com/vito/dang/tests/gqlserver"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

type DangSuite struct {
}

func TestDang(tT *testing.T) {
	testctx.New(tT,
		oteltest.WithTracing[*testing.T](),
		oteltest.WithLogging[*testing.T](),
	).RunTests(DangSuite{})
}

func (DangSuite) TestLanguage(ctx context.Context, t *testctx.T) {
	testGraphQLServer, err := gqlserver.StartServer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)

	// Get schema
	schema, err := introspectSchema(t.Context(), client)
	if err != nil {
		t.Fatalf("Failed to introspect schema: %v", err)
	}

	// Find all test_*.dang files or test_* packages
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
				_, err = dang.RunDir(ctx, client, schema, testFileOrDir, false)
			} else {
				err = dang.RunFile(ctx, client, schema, testFileOrDir, false)
			}
			if err != nil {
				t.Error(err)
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
