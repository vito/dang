package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(oteltest.Main(m))
}

type DaggerSDKSuite struct{}

func TestDaggerSDK(tT *testing.T) {
	testctx.New(tT,
		oteltest.WithTracing[*testing.T](),
		oteltest.WithLogging[*testing.T](),
	).RunTests(DaggerSDKSuite{})
}

// testModulesDir returns the path to the test modules directory
func testModulesDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	return filepath.Join(filepath.Dir(filename), "..", "testdata")
}

// runDagger runs a dagger command and returns stdout, stderr, and any error
func runDagger(ctx context.Context, module string, args ...string) (string, string, error) {
	modulePath := filepath.Join(testModulesDir(), module)
	cmdArgs := append([]string{"-m", modulePath, "call"}, args...)
	cmd := exec.CommandContext(ctx, "dagger", cmdArgs...)
	cmd.Env = append(os.Environ(), telemetry.PropagationEnv(ctx)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// requireDagger runs a dagger command and fails the test if it errors
func requireDagger(ctx context.Context, t *testctx.T, module string, args ...string) string {
	t.Helper()
	stdout, stderr, err := runDagger(ctx, module, args...)
	require.NoError(t, err, "dagger command failed\nstderr: %s", stderr)
	return stdout
}

func (DaggerSDKSuite) TestDirectives(ctx context.Context, t *testctx.T) {
	t.Run("positional defaultPath", func(ctx context.Context, t *testctx.T) {
		// Should work without --source flag because @defaultPath("/") provides default
		out := requireDagger(ctx, t, "test-directives", "with-positional-default-path")
		require.Contains(t, out, "got source")
	})

	t.Run("named defaultPath", func(ctx context.Context, t *testctx.T) {
		// Should work without --source flag because @defaultPath(path: "/") provides default
		out := requireDagger(ctx, t, "test-directives", "with-named-default-path")
		require.Contains(t, out, "got source")
	})

	t.Run("positional ignorePatterns", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-directives", "with-positional-ignore")
		require.Contains(t, out, "got source")
	})

	t.Run("named ignorePatterns", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-directives", "with-named-ignore")
		require.Contains(t, out, "got source")
	})

	t.Run("mixed syntax", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-directives", "with-mixed-syntax")
		require.Contains(t, out, "got source")
	})
}

func (DaggerSDKSuite) TestEnums(ctx context.Context, t *testctx.T) {
	t.Run("get status", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-enum", "get-status", "--status", "COMPLETED")
		require.Contains(t, out, "COMPLETED")
	})

	t.Run("is completed true", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-enum", "is-completed", "--status", "COMPLETED")
		require.Contains(t, out, "true")
	})

	t.Run("is completed false", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-enum", "is-completed", "--status", "PENDING")
		require.Contains(t, out, "false")
	})

	t.Run("log level priority", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-enum", "get-level-priority", "--level", "ERROR")
		require.Contains(t, out, "3")
	})
}

func (DaggerSDKSuite) TestScalars(ctx context.Context, t *testctx.T) {
	t.Run("timestamp", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-scalar", "get-timestamp", "--ts", "2024-01-01T00:00:00Z")
		require.Contains(t, out, "2024-01-01")
	})

	t.Run("url", func(ctx context.Context, t *testctx.T) {
		out := requireDagger(ctx, t, "test-scalar", "get-url", "--url", "https://example.com")
		require.Contains(t, out, "example.com")
	})
}
