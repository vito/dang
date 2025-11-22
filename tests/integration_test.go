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
	runLanguageTests(ctx, t, false)
}

func (DangSuite) TestFormatLanguage(ctx context.Context, t *testctx.T) {
	runLanguageTests(ctx, t, true)
}

func runLanguageTests(ctx context.Context, t *testctx.T, formatFirst bool) {
	testGraphQLServer, err := gqlserver.StartServer()
	require.NoError(t, err)
	t.Cleanup(func() { _ = testGraphQLServer.Stop() })

	client := graphql.NewClient(testGraphQLServer.QueryURL(), nil)

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

			// t.Parallel()
			fi, err := os.Stat(testFileOrDir)
			if err != nil {
				t.Errorf("Failed to stat test file or directory %s: %v", testFileOrDir, err)
				return
			}

			var runErr error
			if formatFirst {
				// Format the file(s) first, then run the formatted version
				if fi.IsDir() {
					// Format all .dang files in directory to a temp dir
					tempDir, err := os.MkdirTemp("", "dang-fmt-test-*")
					if err != nil {
						t.Errorf("Failed to create temp dir: %v", err)
						return
					}
					t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

					entries, err := os.ReadDir(testFileOrDir)
					if err != nil {
						t.Errorf("Failed to read dir %s: %v", testFileOrDir, err)
						return
					}

					for _, entry := range entries {
						if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dang") {
							continue
						}
						srcPath := filepath.Join(testFileOrDir, entry.Name())
						dstPath := filepath.Join(tempDir, entry.Name())

						if err := formatFileTo(srcPath, dstPath); err != nil {
							t.Errorf("Failed to format %s: %v", srcPath, err)
							return
						}
					}

					_, runErr = dang.RunDir(ctx, tempDir, false)
				} else {
					// Format single file to temp file
					tempFile, err := os.CreateTemp("", "dang-fmt-test-*.dang")
					if err != nil {
						t.Errorf("Failed to create temp file: %v", err)
						return
					}
					tempPath := tempFile.Name()
					_ = tempFile.Close()
					t.Cleanup(func() { _ = os.Remove(tempPath) })

					if err := formatFileTo(testFileOrDir, tempPath); err != nil {
						t.Errorf("Failed to format %s: %v", testFileOrDir, err)
						return
					}

					runErr = dang.RunFile(ctx, tempPath, false)
				}
			} else {
				// Run without formatting
				if fi.IsDir() {
					_, runErr = dang.RunDir(ctx, testFileOrDir, false)
				} else {
					runErr = dang.RunFile(ctx, testFileOrDir, false)
				}
			}

			if runErr != nil {
				t.Error(runErr)
			}
		})
	}
}

// formatFileTo formats a Dang source file and writes the result to dst
func formatFileTo(src, dst string) error {
	source, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	formatted, err := dang.FormatFile(source)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, []byte(formatted), 0644)
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
	if tw.buf.Len() > 0 {
		tw.t.Log(tw.buf.String())
	}
}
