package dang

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

const aliasCacheSchema = `
type Query { first: String! second: Int! }
type Thing { message: String! }
type Other { message: String! }
`

type aliasCacheClient struct{}

func (aliasCacheClient) MakeRequest(_ context.Context, _ *graphql.Request, resp *graphql.Response) error {
	return json.Unmarshal([]byte(`{"first":"hello","second":42}`), resp.Data)
}

func TestRunDirRefreshesAliasesOnSharedCache(t *testing.T) {
	schema, err := SchemaFromSDL(aliasCacheSchema, "alias-cache.graphql")
	require.NoError(t, err)
	config := ImportConfig{Name: "Dagger", Schema: schema, Client: aliasCacheClient{}, AutoImport: true}
	base := ContextWithImportConfigs(t.Context(), config)
	dir := t.TempDir()
	run := func(ctx context.Context, source string, want Value) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(source), 0o600))
		env, err := RunDir(ctx, dir, false)
		require.NoError(t, err)
		value, found, err := env.Lookup(ctx, "result")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, want, value)
	}

	// Populate the canonical cache before adding any aliases.
	run(base, `let result: String! = Dagger.first`, StringValue{Val: "hello"})
	config.Aliases = map[string]string{"selected": "first"}
	first := ContextWithImportConfigs(base, config)
	run(first, `let result: String! = Dagger.selected`, StringValue{Val: "hello"})
	config.Aliases = map[string]string{"selected": "second"}
	second := ContextWithImportConfigs(first, config)
	run(second, `let result: Int! = Dagger.selected`, IntValue{Val: 42})

	config.Aliases = nil
	removed := ContextWithImportConfigs(second, config)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(`let result = Dagger.selected`), 0o600))
	_, err = RunDir(removed, dir, false)
	require.ErrorContains(t, err, "selected")
	_, err = RunDir(base, dir, false)
	require.ErrorContains(t, err, "selected")

	// Replacing aliases in a derived context must leave older contexts intact.
	run(first, `let result: String! = Dagger.selected`, StringValue{Val: "hello"})
	run(second, `let result: Int! = Dagger.selected`, IntValue{Val: 42})
}

func TestImportAliasViewsPreserveSnapshotsAndTypeIdentity(t *testing.T) {
	schema, err := SchemaFromSDL(aliasCacheSchema, "alias-view.graphql")
	require.NoError(t, err)
	config := ImportConfig{Name: "Dagger", Schema: schema}
	base := ContextWithImportConfigs(t.Context(), config)
	config.Aliases = map[string]string{"selected": "first", "Named": "Thing"}
	first := ContextWithImportConfigs(base, config)
	decl := &ImportDecl{Name: &Symbol{Name: "Dagger"}}
	infer := func(ctx context.Context) (TypeScope, ValueScope) {
		t.Helper()
		env := NewPreludeTypeScope("test")
		_, err := decl.Infer(ctx, env, hm.NewSimpleFresher())
		require.NoError(t, err)
		value, err := decl.Eval(ctx, NewValueScope(env))
		require.NoError(t, err)
		return decl.inferred, value.(ValueScope)
	}
	firstView, firstValues := infer(first)
	firstScheme, ok := firstView.LocalSchemeOf("selected")
	require.True(t, ok)
	require.Same(t, StringType, functionReturnType(t, firstScheme))
	thing, ok := firstView.NamedType("Thing")
	require.True(t, ok)
	firstNamed, ok := firstView.NamedType("Named")
	require.True(t, ok)
	require.Same(t, thing, firstNamed)

	// Neither a caller's map mutation nor a new context may alter this view.
	config.Aliases["selected"] = "second"
	config.Aliases["Named"] = "Other"
	second := ContextWithImportConfigs(first, config)
	secondView, secondValues := infer(second) // reuse the same ImportDecl node
	require.NotSame(t, firstView, secondView)
	secondScheme, ok := secondView.LocalSchemeOf("selected")
	require.True(t, ok)
	require.Same(t, IntType, functionReturnType(t, secondScheme))
	secondThing, ok := secondView.NamedType("Thing")
	require.True(t, ok)
	require.Same(t, thing, secondThing)
	other, ok := secondView.NamedType("Other")
	require.True(t, ok)
	secondNamed, ok := secondView.NamedType("Named")
	require.True(t, ok)
	require.Same(t, other, secondNamed)
	firstFunction, ok := firstValues.LookupLocal("selected")
	require.True(t, ok)
	require.Equal(t, "first", firstFunction.(GraphQLFunction).Name)
	secondFunction, ok := secondValues.LookupLocal("selected")
	require.True(t, ok)
	require.Equal(t, "second", secondFunction.(GraphQLFunction).Name)

	removedView, removedValues := infer(base)
	_, ok = removedView.LocalNamedType("Named")
	require.False(t, ok)
	_, ok = removedView.LocalSchemeOf("selected")
	require.False(t, ok)
	_, ok = removedValues.LookupLocal("selected")
	require.False(t, ok)
	canonicalThing, ok := removedView.NamedType("Thing")
	require.True(t, ok)
	require.Same(t, thing, canonicalThing)

	reusedView, _ := infer(first)
	require.Same(t, firstView, reusedView)
	// The key depends on the bindings, not map insertion order or identity.
	config.Aliases = map[string]string{"Named": "Thing", "selected": "first"}
	equivalent := ContextWithImportConfigs(base, config)
	equivalentView, _ := infer(equivalent)
	require.Same(t, firstView, equivalentView)
}

func TestImportAliasEvaluationUsesInferredSnapshot(t *testing.T) {
	schema, err := SchemaFromSDL(aliasCacheSchema, "alias-snapshot.graphql")
	require.NoError(t, err)
	config := ImportConfig{Name: "Dagger", Schema: schema, Aliases: map[string]string{"selected": "first"}}
	ctx := ContextWithImportConfigs(t.Context(), config)
	decl := &ImportDecl{Name: &Symbol{Name: "Dagger"}}
	env := NewPreludeTypeScope("test")
	_, err = decl.Infer(ctx, env, hm.NewSimpleFresher())
	require.NoError(t, err)
	config.Aliases["selected"] = "second"
	value, err := decl.Eval(ctx, NewValueScope(env))
	require.NoError(t, err)
	function, ok := value.(ValueScope).LookupLocal("selected")
	require.True(t, ok)
	require.Equal(t, "first", function.(GraphQLFunction).Name)
}
