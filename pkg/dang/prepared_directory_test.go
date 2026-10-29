package dang

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
