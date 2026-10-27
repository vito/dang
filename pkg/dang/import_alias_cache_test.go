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
	config.Aliases["selected"], config.Aliases["Named"] = "second", "Other"
	second := ContextWithImportConfigs(base, config)
	config.Aliases = map[string]string{"Named": "Thing", "selected": "first"}
	equivalent := ContextWithImportConfigs(base, config)
	decl := &ImportDecl{Name: &Symbol{Name: "Dagger"}}
	var firstView TypeScope
	var thing TypeScope
	for _, tc := range []struct {
		ctx           context.Context
		target, named string
		ret           *Type
	}{
		{first, "first", "Thing", StringType},
		{second, "second", "Other", IntType},
		{base, "", "", nil},
		{first, "first", "Thing", StringType},
		{equivalent, "first", "Thing", StringType},
	} {
		env := NewPreludeTypeScope("test")
		_, err := decl.Infer(tc.ctx, env, hm.NewSimpleFresher())
		require.NoError(t, err)
		// Mutations after inference must not affect evaluation either.
		config.Aliases["selected"] = "second"
		value, err := decl.Eval(tc.ctx, NewValueScope(env))
		require.NoError(t, err)
		view := decl.inferred
		canonicalThing, ok := view.NamedType("Thing")
		require.True(t, ok)
		if thing == nil {
			thing, firstView = canonicalThing, view
		}
		require.Same(t, thing, canonicalThing)
		scheme, hasScheme := view.LocalSchemeOf("selected")
		function, hasValue := value.(ValueScope).LookupLocal("selected")
		named, hasNamed := view.LocalNamedType("Named")
		if tc.target == "" {
			require.False(t, hasScheme || hasValue || hasNamed)
			continue
		}
		require.True(t, hasScheme && hasValue && hasNamed)
		require.Same(t, tc.ret, functionReturnType(t, scheme))
		require.Equal(t, tc.target, function.(GraphQLFunction).Name)
		targetType, ok := view.NamedType(tc.named)
		require.True(t, ok)
		require.Same(t, targetType, named)
		if tc.target == "first" {
			require.Same(t, firstView, view)
		}
	}
	// Remapping in another context never mutates an already published view.
	scheme, ok := firstView.LocalSchemeOf("selected")
	require.True(t, ok)
	require.Same(t, StringType, functionReturnType(t, scheme))
}
