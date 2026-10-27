package dang

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/ioctx"
)

type aliasTestClient struct {
	t       *testing.T
	queries []string
}

func (c *aliasTestClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.queries = append(c.queries, req.Query)
	require.Contains(c.t, req.Query, "renamed")
	require.NotContains(c.t, req.Query, "original")
	return json.Unmarshal([]byte(`{"renamed":{"message":"hello"}}`), resp.Data)
}

func TestImportAliasesKeepWireNames(t *testing.T) {
	schema, err := SchemaFromSDL(`type Query { renamed: Renamed! } type Renamed { message: String! }`, "alias.graphql")
	require.NoError(t, err)
	client := &aliasTestClient{t: t}
	config := ImportConfig{Name: "Dagger", Schema: schema, Client: client, AutoImport: true, Aliases: map[string]string{"original": "renamed", "Original": "Renamed"}}
	types, values := BuildScopesFromImports("test", []ImportConfig{config})
	imported, ok := types.NamedType("Dagger")
	require.True(t, ok)
	original, ok := imported.NamedType("Original")
	require.True(t, ok)
	renamed, ok := imported.NamedType("Renamed")
	require.True(t, ok)
	require.Same(t, renamed, original)
	_, ok = values.LookupLocal("original")
	require.True(t, ok)
	ctx := ContextWithImportConfigs(t.Context(), config)
	env := runDangSnippetContext(t, ctx, `value: Dagger.Original! = original
message: String! = value.message`)
	value, found, err := env.Lookup(ctx, "message")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StringValue{Val: "hello"}, value)
	require.NotEmpty(t, client.queries)
}

func TestImportAliasesPreserveExistingNames(t *testing.T) {
	schema, err := SchemaFromSDL(`type Query { first: String! second: Int! }`, "collision.graphql")
	require.NoError(t, err)
	config := ImportConfig{Name: "Dagger", Schema: schema, Aliases: map[string]string{"first": "second"}}
	types, values := BuildScopesFromImports("test", []ImportConfig{config})
	imported, ok := types.NamedType("Dagger")
	require.True(t, ok)
	query, ok := imported.NamedType("Query")
	require.True(t, ok)
	scheme, ok := query.SchemeOf("first")
	require.True(t, ok)
	require.Same(t, StringType, functionReturnType(t, scheme))
	value, ok := values.LookupLocal("first")
	require.True(t, ok)
	require.Equal(t, "first", value.(GraphQLFunction).Name)
}

func TestImportAliasesPreserveBuiltins(t *testing.T) {
	schema, err := SchemaFromSDL(`type Query { renamed: String! }`, "builtin-alias.graphql")
	require.NoError(t, err)
	config := ImportConfig{
		Name: "Dagger", Schema: schema, AutoImport: true,
		Aliases: map[string]string{"print": "renamed"},
	}
	types, values := BuildScopesFromImports("test", []ImportConfig{config})
	imported, ok := types.NamedType("Dagger")
	require.True(t, ok)
	scheme, ok := imported.SchemeOf("print")
	require.True(t, ok)
	builtin, ok := Prelude.SchemeOf("print")
	require.True(t, ok)
	require.Same(t, builtin, scheme)
	value, ok := values.LookupLocal("print")
	require.True(t, ok)
	require.IsType(t, BuiltinFunction{}, value)

	var stdout bytes.Buffer
	ctx := ioctx.StdoutToContext(ContextWithImportConfigs(t.Context(), config), &stdout)
	runDangSnippetContext(t, ctx, `Dagger.print(value: "hello")`)
	require.Equal(t, "hello\n", stdout.String())
}
