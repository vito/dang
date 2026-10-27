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

func TestImportAliasesPreserveBuiltinSchemaScalars(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		typ   *Type
	}{
		{"String", "text", StringType},
		{"Int", "number", IntType},
		{"Float", "ratio", FloatType},
		{"Boolean", "truth", BooleanType},
		{"ID", "identifier", IDType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := SchemaFromSDL(`
type Query {
  text: String! number: Int! ratio: Float! truth: Boolean! identifier: ID!
  renamed: Renamed!
}
type Renamed { message: String! }
`, "scalar-collision.graphql")
			require.NoError(t, err)
			config := ImportConfig{
				Name: "Dagger", Schema: schema,
				Aliases: map[string]string{tc.name: "Renamed", "Original": "Renamed"},
			}
			types, values := BuildScopesFromImports("test", []ImportConfig{config})
			imported, ok := types.NamedType("Dagger")
			require.True(t, ok)
			scalar, ok := imported.NamedType(tc.name)
			require.True(t, ok)
			require.Same(t, tc.typ, scalar)
			scheme, ok := imported.SchemeOf(tc.field)
			require.True(t, ok)
			require.Same(t, tc.typ, functionReturnType(t, scheme))
			value, ok := values.LookupLocal(tc.field)
			require.True(t, ok)
			runtimeType, ok := moduleType(value.(GraphQLFunction).FnType.Ret(false))
			require.True(t, ok)
			require.Same(t, tc.typ, runtimeType)

			original, ok := imported.NamedType("Original")
			require.True(t, ok)
			renamed, ok := imported.NamedType("Renamed")
			require.True(t, ok)
			require.Same(t, renamed, original)
		})
	}
}

func TestImportAliasesResolveBuiltinSchemaScalarTargets(t *testing.T) {
	schema, err := SchemaFromSDL(`
type Query { text: String! number: Int! ratio: Float! truth: Boolean! identifier: ID! }
`, "scalar-target.graphql")
	require.NoError(t, err)
	config := ImportConfig{
		Name: "Dagger", Schema: schema, AutoImport: true,
		Aliases: map[string]string{
			"LocalString": "String", "LocalInt": "Int", "LocalFloat": "Float",
			"LocalBoolean": "Boolean", "LocalID": "ID",
			"Chained": "LocalString", "Absent": "Path",
		},
	}
	types, _ := BuildScopesFromImports("test", []ImportConfig{config})
	imported, ok := types.NamedType("Dagger")
	require.True(t, ok)
	for _, tc := range []struct {
		alias string
		typ   *Type
	}{
		{"LocalString", StringType},
		{"LocalInt", IntType},
		{"LocalFloat", FloatType},
		{"LocalBoolean", BooleanType},
		{"LocalID", IDType},
	} {
		qualified, found := imported.NamedType(tc.alias)
		require.True(t, found, tc.alias)
		require.Same(t, tc.typ, qualified)
		unqualified, found := types.NamedType(tc.alias)
		require.True(t, found, tc.alias)
		require.Same(t, tc.typ, unqualified)
	}
	for _, name := range []string{"Chained", "Absent"} {
		_, found := imported.NamedType(name)
		require.False(t, found, name)
	}

	ctx := ContextWithImportConfigs(t.Context(), config)
	env := runDangSnippetContext(t, ctx, `let text: Dagger.LocalString! = "hello"`)
	value, found, err := env.Lookup(ctx, "text")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StringValue{Val: "hello"}, value)
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

func TestImportAliasesPreserveSourcePrelude(t *testing.T) {
	schema, err := SchemaFromSDL(`type Query { renamed(raw: String!): String! }`, "prelude-alias.graphql")
	require.NoError(t, err)
	client := &aliasTestClient{t: t}
	config := ImportConfig{
		Name: "Dagger", Schema: schema, Client: client, AutoImport: true,
		Aliases: map[string]string{"Path": "renamed"},
	}
	types, values := BuildScopesFromImports("test", []ImportConfig{config})
	imported, ok := types.NamedType("Dagger")
	require.True(t, ok)
	scheme, ok := imported.SchemeOf("Path")
	require.True(t, ok)
	preludeScheme, ok := PreludeModule().SchemeOf("Path")
	require.True(t, ok)
	require.Same(t, preludeScheme, scheme)
	module, ok := values.LookupLocal("Dagger")
	require.True(t, ok)
	constructor, ok := module.(ValueScope).LookupLocal("Path")
	require.True(t, ok)
	require.IsType(t, ScalarConstructor{}, constructor)

	ctx := ContextWithImportConfigs(t.Context(), config)
	env := runDangSnippetContext(t, ctx, `let path: Path! = Dagger.Path(raw: "a//b/../c")
let name: String! = path.name`)
	name, found, err := env.Lookup(ctx, "name")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StringValue{Val: "c"}, name)
	require.Empty(t, client.queries)
}

func TestImportAliasesPreserveSchemaValuesOverSourcePrelude(t *testing.T) {
	schema, err := SchemaFromSDL(`type Query { renamed: String! } input Path { raw: String! }`, "schema-path.graphql")
	require.NoError(t, err)
	config := ImportConfig{
		Name: "Dagger", Schema: schema, AutoImport: true,
		Aliases: map[string]string{"Path": "renamed"},
	}
	_, values := BuildScopesFromImports("test", []ImportConfig{config})
	module, ok := values.LookupLocal("Dagger")
	require.True(t, ok)
	constructor, ok := module.(ValueScope).LookupLocal("Path")
	require.True(t, ok)
	require.IsType(t, InputObjectConstructor{}, constructor)

	ctx := ContextWithImportConfigs(t.Context(), config)
	env := runDangSnippetContext(t, ctx, `let path: Dagger.Path! = Dagger.Path(raw: "schema")
let raw: String! = path.raw`)
	raw, found, err := env.Lookup(ctx, "raw")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, StringValue{Val: "schema"}, raw)
}

func TestImportAliasesOnlyResolveSchemaTargets(t *testing.T) {
	schema, err := SchemaFromSDL(`type Query { renamed: Renamed! } enum Renamed { HELLO }`, "alias-chain.graphql")
	require.NoError(t, err)
	config := ImportConfig{
		Name: "Dagger", Schema: schema,
		Aliases: map[string]string{
			"first": "second", "second": "renamed",
			"First": "Second", "Second": "Renamed",
			"missing": "absent", "builtin": "print",
		},
	}
	types, values := BuildScopesFromImports("test", []ImportConfig{config})
	imported, ok := types.NamedType("Dagger")
	require.True(t, ok)
	for _, name := range []string{"first", "First", "missing", "builtin"} {
		_, found := imported.NamedType(name)
		require.False(t, found, name)
		_, found = imported.SchemeOf(name)
		require.False(t, found, name)
		_, found = values.LookupLocal(name)
		require.False(t, found, name)
	}
	second, ok := imported.NamedType("Second")
	require.True(t, ok)
	renamed, ok := imported.NamedType("Renamed")
	require.True(t, ok)
	require.Same(t, renamed, second)
	value, ok := values.LookupLocal("second")
	require.True(t, ok)
	require.Equal(t, "renamed", value.(GraphQLFunction).Name)
	value, ok = values.LookupLocal("Second")
	require.True(t, ok)
	require.Same(t, renamed, value.(*Object).Mod)
}

const aliasNarrowingSchema = `
type Query { renamed: Result! node: Node! box: Box! }
interface Node { message: String! }
type Renamed implements Node { message: String! hidden: String! }
type Other implements Node { message: String! }
union Result = Renamed | Other
type Box { result: Result! }
`

type aliasNarrowingClient struct {
	t       *testing.T
	queries []string
}

func (c *aliasNarrowingClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.queries = append(c.queries, req.Query)
	require.NotContains(c.t, req.Query, "Original")
	require.NotContains(c.t, req.Query, "original")
	return json.Unmarshal([]byte(`{
		"renamed": {"__typename": "Renamed", "message": "hello"},
		"node": {"__typename": "Renamed", "message": "hello"},
		"box": {"result": {"__typename": "Renamed", "message": "hello"}}
	}`), resp.Data)
}

func TestImportAliasesNarrowWithCanonicalTypes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
	}{
		{"union case", `let message = case (original) {
  value: Original => value.message
  else => null
}`},
		{"interface case with shadowed alias", `type Original { local: String! }
let message = case (node) {
  value: Original => value.message
  else => null
}`},
		{"mixed union interface case with shadowed alias", `type Original { message: String! }
let result = if (true) { node } else { "fallback" }
let message = case (result) {
  value: Original => value.message
  else => null
}`},
		{"union fragment", `let result = original.{{... on Original {{message}}}}
let message = case (result) { value: Original => value.message
  else => null }`},
		{"interface fragment", `let result = node.{{... on Original {{message}}}}
let message = case (result) { value: Original => value.message
  else => null }`},
		{"lazy union fragment", `let message = original.{{... on Original!}}.message`},
		{"qualified lazy interface fragment", `let message = node.{{... on Dagger.Original!}}.message`},
		{"nested fragment", `let result = box.{{result.{{... on Original {{message}}}}}}
let message = case (result.result) { value: Original => value.message
  else => null }`},
		{"native projection", `let result = original.{{... on Original {{message}}}}
let again = result.{{... on Original {{message}}}}
let message = case (again) { value: Original => value.message
  else => null }`},
		{"shadowed alias", `type Original { local: String! }
type Renamed { local: String! }
let result = original.{{... on Original {{message}}}}
let message = case (result) { value: Original => value.message
  else => null }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := SchemaFromSDL(aliasNarrowingSchema, "alias-narrowing.graphql")
			require.NoError(t, err)
			client := &aliasNarrowingClient{t: t}
			config := ImportConfig{
				Name: "Dagger", Schema: schema, Client: client, AutoImport: true,
				Aliases: map[string]string{"Original": "Renamed", "original": "renamed"},
			}
			ctx := ContextWithImportConfigs(t.Context(), config)
			env := runDangSnippetContext(t, ctx, tc.source)
			value, found, err := env.Lookup(ctx, "message")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, StringValue{Val: "hello"}, value)
			require.NotEmpty(t, client.queries)
		})
	}
}

func TestInterfacePatternsPreferNativeImplementations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typeName string
		operand  string
	}{
		{"schema name", "Renamed", "value"},
		{"schema name in mixed union", "Renamed", "if (true) { value } else { 42 }"},
		{"alias", "Original", "value"},
		{"alias in mixed union", "Original", "if (true) { value } else { 42 }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := SchemaFromSDL(aliasNarrowingSchema, "native-pattern.graphql")
			require.NoError(t, err)
			config := ImportConfig{Name: "Dagger", Schema: schema, AutoImport: true}
			if tc.typeName == "Original" {
				config.Aliases = map[string]string{"Original": "Renamed"}
			}
			ctx := ContextWithImportConfigs(t.Context(), config)
			source := `type ` + tc.typeName + ` implements Node {
  message: String! { "local" }
}
describe(value: Node!): String! {
  case (` + tc.operand + `) {
    native: ` + tc.typeName + ` => native.message
    else => "wrong type"
  }
}
let message = describe(` + tc.typeName + `)
`
			env := runDangSnippetContext(t, ctx, source)
			value, found, err := env.Lookup(ctx, "message")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, StringValue{Val: "local"}, value)
		})
	}
}

func TestImportAliasesPreserveNarrowingRestrictions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{"unselected field", `import Dagger
let result = original.{{... on Original {{message}}}}
let bad = case (result) { value: Original => value.hidden
  else => null }`, "hidden"},
		{"unselected field in another fragment", `import Dagger
let result = original.{{... on Original {{message}}}}
let bad = result.{{... on Original {{hidden}}}}`, "hidden"},
		{"different import identity", `import Dagger
import Another
let bad = Dagger.original.{{... on Another.Original {{message}}}}`, "not a member of union"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := SchemaFromSDL(aliasNarrowingSchema, "alias-narrowing.graphql")
			require.NoError(t, err)
			ctx := ContextWithImportConfigs(t.Context(),
				ImportConfig{Name: "Dagger", Schema: schema, Aliases: map[string]string{"Original": "Renamed", "original": "renamed"}},
				ImportConfig{Name: "Another", Schema: schema, Aliases: map[string]string{"Original": "Renamed"}},
			)
			parsed, err := ParseWithRecovery("alias-narrowing.dang", []byte(tc.source))
			require.NoError(t, err)
			_, err = Infer(ctx, NewPreludeTypeScope(""), parsed.(*FileBlock), true)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

const nestedQualifiedFragmentTypes = `
type Outer {
  type Inner {
    type Cat { name: String! }
    type Dog { name: String! }
    union Pet = Cat | Dog
    pet: Pet! { Cat(name: "nested") }
  }
  inner: Inner! { Inner }
}
`

func TestNestedQualifiedInlineFragments(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
	}{
		{"lazy", `let name = Outer.inner.pet.{{... on Outer.Inner.Cat!}}.name`},
		{"eager", `let selected = Outer.inner.pet.{{... on Outer.Inner.Cat {{name}}}}
let name = case (selected) {
  cat: Cat => cat.name
  else => null
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := runDangSnippet(t, nestedQualifiedFragmentTypes+tc.source)
			name, found, err := env.Lookup(t.Context(), "name")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, StringValue{Val: "nested"}, name)
		})
	}
}

func TestNestedQualifiedInlineFragmentsCheckMembership(t *testing.T) {
	source := nestedQualifiedFragmentTypes + `
type Another {
  type Inner {
    type Cat { name: String! }
  }
}
let bad = Outer.inner.pet.{{... on Another.Inner.Cat!}}
`
	parsed, err := ParseWithRecovery("nested-fragment.dang", []byte(source))
	require.NoError(t, err)
	_, err = Infer(t.Context(), NewPreludeTypeScope(""), parsed.(*FileBlock), true)
	require.ErrorContains(t, err, "not a member of union")
}
