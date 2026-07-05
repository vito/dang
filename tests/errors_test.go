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
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/ioctx"
	"github.com/vito/dang/v2/tests/gqlserver"
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

store(&block(x: Int!): Int!): Int! {
  saved = block
  0
}

makeReturner: Int! {
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

	// Native Dang modules (not GraphQL schemas), imported from source. Lib backs
	// the cross-module `let` visibility boundary; Util + Calc back the per-module
	// type-distinctness boundary (Calc imports its own copy of Util via its
	// dang.toml, so Calc's Util.Widget doesn't unify with an importer's).
	libDir, err := filepath.Abs("importlib")
	require.NoError(t, err)
	utilDir, err := filepath.Abs("importutil")
	require.NoError(t, err)
	calcDir, err := filepath.Abs("importcalc")
	require.NoError(t, err)

	ctx = dang.ContextWithImportConfigs(ctx,
		dang.ImportConfig{
			Name:   "Test",
			Client: client,
		},
		dang.ImportConfig{
			Name:   "Other",
			Client: client, // Same client/schema, but different import name
		},
		dang.ImportConfig{
			Name:          "Lib",
			DangModuleDir: libDir,
		},
		dang.ImportConfig{
			Name:          "Util",
			DangModuleDir: utilDir,
		},
		dang.ImportConfig{
			Name:          "Calc",
			DangModuleDir: calcDir,
		},
	)

	// Run the Dang file
	err = dang.RunFile(ctx, dangFile, false)
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

// The removed error forms (bare-binding catch-alls and legacy try/catch)
// deliberately still parse so the compiler can reject them with targeted
// diagnostics and `dang fmt` can rewrite them. That means they cannot live
// as fixtures under tests/errors/: the repo-wide `dang fmt` sweep would
// heal them into valid code. They run from a temp dir instead.
func (DangSuite) TestRescueMigrationDiagnostics(ctx context.Context, t *testctx.T) {
	tests := []struct {
		name   string
		source string
		wants  []string
	}{
		{
			name: "bare binding catch-all",
			source: `let x = "value" rescue {
  err => "caught"
}
print(x)
`,
			wants: []string{
				"bare catch-all `err =>` is no longer supported",
				"err: Error =>",
				"else =>",
			},
		},
		{
			name: "legacy try/catch",
			source: `let x = try {
  raise "boom"
} catch {
  err => "caught: " + err.message
}
print(x)
`,
			wants: []string{
				"try/catch was replaced by postfix `rescue`",
				"dang fmt -w",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(ctx context.Context, t *testctx.T) {
			dir, err := os.MkdirTemp("", "dang-rescue-migration")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(dir) })

			path := filepath.Join(dir, "main.dang")
			require.NoError(t, os.WriteFile(path, []byte(tt.source), 0644))

			err = dang.RunFile(ctx, path, false)
			require.Error(t, err)
			for _, want := range tt.wants {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}

// TestDangModuleImportCycle checks that a native Dang import cycle (module A
// imports B imports A) is reported as a clean error rather than looping forever.
// A directory that must ERROR fits neither the golden harness (the cycle message
// embeds absolute module dirs) nor the language harness (an error there is a test
// failure), so — like TestRunDirControlFlowSourceErrors — it runs from a temp
// dir and asserts on the message. Under the per-module identity model each hop
// would instantiate a fresh copy, so cycle detection tracks the active compile
// path (a ctx stack) instead of a shared cache marker.
func (DangSuite) TestDangModuleImportCycle(ctx context.Context, t *testctx.T) {
	root := t.TempDir()

	writeCycleModule := func(name, importsName string) {
		modDir := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(modDir, 0755))
		toml := "[imports." + importsName + "]\npath = \"../" + strings.ToLower(importsName) + "\"\n"
		require.NoError(t, os.WriteFile(filepath.Join(modDir, "dang.toml"), []byte(toml), 0644))
		src := "import " + importsName + "\n\npub " + name + ": Int! = 1\n"
		require.NoError(t, os.WriteFile(filepath.Join(modDir, name+".dang"), []byte(src), 0644))
	}
	writeCycleModule("a", "B")
	writeCycleModule("b", "A")

	_, err := dang.RunDir(ctx, filepath.Join(root, "a"), false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "import cycle detected")
}
