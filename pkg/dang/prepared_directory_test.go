package dang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
)

func preparedSource(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(source), 0o600))
	return dir
}

func TestPreparedDirectoryFreshEvaluation(t *testing.T) {
	dir := preparedSource(t, `import Session
interface Named { value: String! }
# A documented type must not write to the inferred graph during evaluation.
type Thing implements Named { value: String! }
type Box { value: Int! = 0; add: Box! { self.value = value + 1; self } }
result: String! = Session.value
named: Named! = Thing(value: Session.value)
bumped: Int! = Box().add.value
`)
	schema, err := SchemaFromSDL("type Query { value: String! }", "session.graphqls")
	require.NoError(t, err)
	contextFor := func(client graphql.Client) context.Context {
		return ContextWithImportConfigs(context.Background(), ImportConfig{Name: "Session", Client: client, Schema: schema})
	}
	initial := &blueprintCountingClient{Client: blueprintClient("prepare-only")}
	program, err := PrepareDir(contextFor(initial), dir)
	require.NoError(t, err)
	require.Zero(t, initial.calls.Load(), "preparation executed an initializer")
	for _, block := range program.blocks {
		block.Walk(func(node Node) bool {
			if imp, ok := node.(*ImportDecl); ok {
				require.Nil(t, imp.client, "prepared source retained a client")
			}
			return true
		})
	}

	var wg sync.WaitGroup
	for index := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("current-%d", index)
			client := &blueprintCountingClient{Client: blueprintClient(name)}
			ctx := contextFor(client)
			values, err := program.Evaluate(ctx)
			if !assertPreparedNoError(t, err) {
				return
			}
			result, found, err := values.Lookup(ctx, "result")
			if err != nil || !found || result.(StringValue).Val != name {
				t.Errorf("result came from another invocation: %v, %v", result, err)
			}
			bumped, found, err := values.Lookup(ctx, "bumped")
			if err != nil || !found || bumped.(IntValue).Val != 1 {
				t.Errorf("object mutation leaked: %v, %v", bumped, err)
			}
			if calls := client.calls.Load(); calls != 2 {
				t.Errorf("initializers did not run once for this invocation: %d", calls)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, initial.calls.Load(), "an evaluation used the preparation client")

	changed, err := SchemaFromSDL("type Query { value: Int! }", "session.graphqls")
	require.NoError(t, err)
	client := &blueprintCountingClient{Client: blueprintClient("invalid-schema")}
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{Name: "Session", Client: client, Schema: changed})
	_, err = program.Evaluate(ctx)
	require.ErrorContains(t, err, "different schema")
	require.Zero(t, client.calls.Load(), "changed schema reached evaluation")
}

func TestPreparedDirectoryReloadedSchema(t *testing.T) {
	dir := preparedSource(t, "import Session\nresult: String! = Session.value\n")
	const sdl = `
directive @a on FIELD_DEFINITION
directive @b on FIELD_DEFINITION
type Query { value: String! @a @b one: One! two: Two! }
type One { value: String! }
type Two { value: String! }
enum Three { A B }
`
	// Each evaluation loads the same SDL again, as a dang.toml schema import
	// does, so it is equal to the prepared schema but not the same pointer.
	ctxFor := func(name string) context.Context {
		schema, err := SchemaFromSDL(sdl, "session.graphqls")
		require.NoError(t, err)
		return ContextWithImportConfigs(context.Background(), ImportConfig{Name: "Session", Client: blueprintClient(name), Schema: schema})
	}
	program, err := PrepareDir(ctxFor("prepare"), dir)
	require.NoError(t, err)
	for range 5 {
		ctx := ctxFor("current")
		values, err := program.Evaluate(ctx)
		require.NoError(t, err)
		result, found, err := values.Lookup(ctx, "result")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "current", result.(StringValue).Val)
	}
}

func assertPreparedNoError(t *testing.T, err error) bool {
	t.Helper()
	if err != nil {
		t.Error(err)
		return false
	}
	return true
}

type preparedFailingClient struct{}

func (preparedFailingClient) MakeRequest(context.Context, *graphql.Request, *graphql.Response) error {
	return errors.New("this invocation failed")
}

func TestPreparedDirectoryDoesNotCacheFailures(t *testing.T) {
	dir := preparedSource(t, "import Session\nresult: String! = Session.value\n")
	schema, err := SchemaFromSDL("type Query { value: String! }", "session.graphqls")
	require.NoError(t, err)
	ctxFor := func(client graphql.Client) context.Context {
		return ContextWithImportConfigs(context.Background(), ImportConfig{Name: "Session", Client: client, Schema: schema})
	}
	program, err := PrepareDir(ctxFor(blueprintClient("prepare")), dir)
	require.NoError(t, err)
	_, err = program.Evaluate(ctxFor(preparedFailingClient{}))
	require.ErrorContains(t, err, "this invocation failed")
	ctx := ctxFor(blueprintClient("retry"))
	values, err := program.Evaluate(ctx)
	require.NoError(t, err)
	result, found, err := values.Lookup(ctx, "result")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "retry", result.(StringValue).Val)
}

func TestPreparedDirectoryUnsupportedRuntimeTypeState(t *testing.T) {
	for _, source := range []string{
		`type Thing { self { value: String! = "static" } }`,
		`scalar Name { new(value: String!): String! { value } }`,
	} {
		t.Run(source, func(t *testing.T) {
			_, err := PrepareDir(context.Background(), preparedSource(t, source))
			require.ErrorIs(t, err, ErrUnpreparableDirectory)
		})
	}
}

func TestPreparedDirectorySelectionArguments(t *testing.T) {
	t.Run("import", func(t *testing.T) {
		dir := preparedSource(t, `type Echo { echo(value: String!): String! { value } }
let answer = Echo.{{ echo(value: { import Session; Session.value }) }}
result: String! = answer.echo
`)
		schema, err := SchemaFromSDL("type Query { value: String! }", "session.graphqls")
		require.NoError(t, err)
		ctxFor := func(client graphql.Client) context.Context {
			return ContextWithImportConfigs(context.Background(), ImportConfig{Name: "Session", Client: client, Schema: schema})
		}
		program, err := PrepareDir(ctxFor(blueprintClient("prepare-only")), dir)
		require.NoError(t, err)
		ctx := ctxFor(blueprintClient("current"))
		values, err := program.Evaluate(ctx)
		require.NoError(t, err)
		result, found, err := values.Lookup(ctx, "result")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "current", result.(StringValue).Val)
	})

	t.Run("unsupported declaration", func(t *testing.T) {
		dir := preparedSource(t, `type Echo { echo(value: String!): String! { value } }
let answer = Echo.{{ echo(value: {
  scalar Name { label: String! { "hello" } }
  "ok"
}) }}
`)
		_, err := PrepareDir(context.Background(), dir)
		require.ErrorIs(t, err, ErrUnpreparableDirectory)
	})
}

func TestPreparedDirectoryReprepareChangedSource(t *testing.T) {
	dir := preparedSource(t, "value: Int! = 1\n")
	first, err := PrepareDir(context.Background(), dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte("value: Int! = 2\n"), 0o600))
	second, err := PrepareDir(context.Background(), dir)
	require.NoError(t, err)
	for index, program := range []*PreparedDirectory{first, second} {
		values, err := program.Evaluate(context.Background())
		require.NoError(t, err)
		value, found, err := values.Lookup(context.Background(), "value")
		require.NoError(t, err)
		require.True(t, found)
		require.EqualValues(t, index+1, value.(IntValue).Val)
	}
}

func TestPreparedDirectoryLanguageSemantics(t *testing.T) {
	for _, name := range []string{
		"test_self_method_execution.dang",
		"test_list_reduce_object.dang",
		"test_copy_on_write.dang",
		"test_cow_value_aliasing.dang",
		"test_early_return.dang",
		"test_interface_covariant_return.dang",
		"test_constructors.dang",
	} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "..", "tests", name))
			require.NoError(t, err)
			dir := preparedSource(t, string(source))
			program, err := PrepareDir(context.Background(), dir)
			require.NoError(t, err)
			var group sync.WaitGroup
			for range 4 {
				group.Add(1)
				go func() {
					defer group.Done()
					_, err := program.Evaluate(context.Background())
					if err != nil {
						t.Error(err)
					}
				}()
			}
			group.Wait()
		})
	}
}

func TestPreparedDirectoryCrossFilePrivateMembers(t *testing.T) {
	dir := preparedSource(t, `type Vault { let secret: String! = "private" }
`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "reader.dang"), []byte(`
type Reader { read: String! { Vault.secret } }
result: String! = Reader.read
`), 0o600))
	program, err := PrepareDir(context.Background(), dir)
	require.NoError(t, err)
	for range 2 {
		values, err := program.Evaluate(context.Background())
		require.NoError(t, err)
		result, found, err := values.Lookup(context.Background(), "result")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "private", result.(StringValue).Val)
	}
}

func TestPreparedDirectoryKeepsErrorLocations(t *testing.T) {
	for range 2 {
		dir := preparedSource(t, "assert { false }\n")
		program, err := PrepareDir(context.Background(), dir)
		require.NoError(t, err)
		var previous *AssertionError
		for range 2 {
			_, err := program.Evaluate(context.Background())
			var assertion *AssertionError
			require.ErrorAs(t, err, &assertion)
			require.Equal(t, filepath.Join(dir, "main.dang"), assertion.Location.Filename)
			if previous != nil {
				require.NotSame(t, previous, assertion, "evaluation retained a previous error")
			}
			previous = assertion
		}
	}
}

func TestPreparedDirectoryNativeImportsFallback(t *testing.T) {
	native := preparedSource(t, "type Remote { value: String! = \"native\" }\n")
	dir := preparedSource(t, "import Other\nresult: String! = Other.Remote.value\n")
	ctx := ContextWithImportConfigs(context.Background(), ImportConfig{Name: "Other", DangModuleDir: native})
	_, err := PrepareDir(ctx, dir)
	require.ErrorIs(t, err, ErrUnpreparableDirectory)
	values, err := RunDir(ctx, dir, false)
	require.NoError(t, err)
	result, found, err := values.Lookup(ctx, "result")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "native", result.(StringValue).Val)
}

func TestPreparedDirectoryPureLanguageCorpus(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "tests", "test_*.dang"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	checked := 0
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			source, err := os.ReadFile(file)
			require.NoError(t, err)
			if strings.Contains(string(source), "import ") {
				t.Skip("imported GraphQL/native modules need their integration fixture")
			}
			dir := preparedSource(t, string(source))
			if _, err := RunDir(context.Background(), dir, false); err != nil {
				t.Skip("ordinary evaluation requires the integration fixture")
			}
			program, err := PrepareDir(context.Background(), dir)
			if errors.Is(err, ErrUnpreparableDirectory) {
				t.Skip("runtime type state uses the ordinary evaluation path")
			}
			// A preparation failure is a regression when ordinary evaluation
			// succeeds with the same source and context.
			require.NoError(t, err)
			checked++
			var group sync.WaitGroup
			for range 3 {
				group.Add(1)
				go func() {
					defer group.Done()
					_, err := program.Evaluate(context.Background())
					if err != nil {
						t.Error(err)
					}
				}()
			}
			group.Wait()
		})
	}
	t.Logf("concurrently checked %d pure language programs", checked)
}

func TestPreparedDirectoryStopsServices(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads process state from /proc")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OpName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.OpName == "IntrospectionQuery" {
			fmt.Fprintf(w, `{"data": %s}`, fakeModuleSchemaJSON)
		} else {
			fmt.Fprint(w, `{"data": {"value": "served"}}`)
		}
	}))
	t.Cleanup(srv.Close)

	// The service records its PID, reports the server's endpoint and stays up.
	serviceDir := func(t *testing.T, toml, source string) (string, string) {
		bin := t.TempDir()
		pidFile := filepath.Join(bin, "pid")
		script := fmt.Sprintf("#!/bin/sh\necho $$ > %q\necho '{\"endpoint\": %q}'\nexec cat > /dev/null\n", pidFile, srv.URL)
		require.NoError(t, os.WriteFile(filepath.Join(bin, "service"), []byte(script), 0o755))
		dir := preparedSource(t, source)
		toml = fmt.Sprintf("[imports.Session]\nservice = [%q]\n", filepath.Join(bin, "service")) + toml
		require.NoError(t, os.WriteFile(filepath.Join(dir, "dang.toml"), []byte(toml), 0o600))
		return dir, pidFile
	}
	// A killed child stays a zombie until reaped, and nothing reaps it.
	requireStopped := func(t *testing.T, pidFile string) {
		pid, err := os.ReadFile(pidFile)
		require.NoError(t, err, "the service never started")
		statPath := filepath.Join("/proc", strings.TrimSpace(string(pid)), "stat")
		require.Eventually(t, func() bool {
			stat, err := os.ReadFile(statPath)
			if err != nil {
				return true
			}
			fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
			return fields[0] == "Z"
		}, 5*time.Second, 10*time.Millisecond, "the service is still running")
	}

	t.Run("preparation", func(t *testing.T) {
		// Without a schema, inference starts the service to introspect it.
		dir, pidFile := serviceDir(t, "", "import Session\nvalue: Int! = 1\n")
		_, err := PrepareDir(context.Background(), dir)
		require.NoError(t, err)
		requireStopped(t, pidFile)
	})

	t.Run("evaluation", func(t *testing.T) {
		// With a schema, only evaluation starts the service.
		dir, pidFile := serviceDir(t, "schema = \"session.graphqls\"\n", "import Session\nresult: String! = Session.value\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "session.graphqls"), []byte("type Query { value: String! }\n"), 0o600))
		program, err := PrepareDir(context.Background(), dir)
		require.NoError(t, err)
		_, err = os.Stat(pidFile)
		require.ErrorIs(t, err, os.ErrNotExist, "preparation started the service")
		_, err = program.Evaluate(context.Background())
		require.NoError(t, err)
		requireStopped(t, pidFile)
	})
}
