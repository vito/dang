package analysis_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/dang/analysis"
)

// testScope analyzes main.dang (importing the test GraphQL schema as Test)
// and returns the scope its top level sees.
func testScope(t *testing.T, source string) dang.TypeScope {
	t.Helper()
	schema, err := dang.SchemaFromSDLFile(filepath.Join("..", "..", "..", "tests", "gqlserver", "schema.graphqls"))
	require.NoError(t, err)
	a, err := analysis.Analyze(context.Background(), mapFS(map[string]string{"main.dang": source}), ".", analysis.Options{
		ImportConfigs: []dang.ImportConfig{{Name: "Test", Schema: schema}},
	})
	require.NoError(t, err)
	require.Empty(t, a.Diagnostics())
	return a.FileScope("main.dang")
}

const membersSource = `import Test

"""
A person.
"""
type Person {
  """
  The person's name.
  """
  name: String!

  let secret: String! = "shh"

  """
  Greets someone.
  """
  greet(greeting: String! = "hi"): String! {
    greeting + ", " + name
  }

  self {
    """
    Nobody in particular.
    """
    nobody: Person! { Person(name: "nobody") }
  }
}

"""
Adds two numbers.
"""
pub add(x: Int!, y: Int!): Int! { x + y }

let helper = 1

pub people = [Person(name: "a")]
`

func member(t *testing.T, list *analysis.MemberList, name string) analysis.Member {
	t.Helper()
	for _, m := range list.Members {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no member %q in %s", name, names(list.Members))
	return analysis.Member{}
}

func names(members []analysis.Member) []string {
	var ns []string
	for _, m := range members {
		ns = append(ns, m.Name)
	}
	return ns
}

func TestMembersOfAString(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	list, err := analysis.Members(ctx, scope, `"a,b"`)
	require.NoError(t, err)
	require.Equal(t, "String!", list.Type)

	split := member(t, list, "split")
	require.Equal(t, analysis.KindFunction, split.Kind)
	require.Equal(t, "String", split.Receiver)
	require.True(t, split.Builtin)
	require.NotEmpty(t, split.Doc)
	require.Equal(t, `"a,b,c".split(",")`, split.Example)
	require.Equal(t, "String.split", split.Qualified())
}

func TestMembersOfAList(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	list, err := analysis.Members(ctx, scope, `["a"]`)
	require.NoError(t, err)
	require.Equal(t, "[String!]!", list.Type)

	reduce := member(t, list, "reduce")
	require.Equal(t, "List", reduce.Receiver)
	require.Equal(t, "reduce(initial: b) { acc: b, item: a => b }: b", reduce.Signature())
	require.Equal(t, `[1, 2, 3, 4].reduce(0) { acc, x => acc + x }`, reduce.Example)

	dropFirst := member(t, list, "dropFirst")
	require.Equal(t, "dropFirst(count: Int = 1): [a]!", dropFirst.Signature())

	_, err = analysis.Doc(ctx, scope, "List.sort")
	require.ErrorContains(t, err, `no member "sort"`)

	// The same members, by the type's name.
	byName, err := analysis.Members(ctx, scope, "List")
	require.NoError(t, err)
	require.Equal(t, names(list.Members), names(byName.Members))
}

func TestMembersOfAMap(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	list, err := analysis.Members(ctx, scope, `["a": 1]`)
	require.NoError(t, err)
	require.Equal(t, "Map[Int!]!", list.Type)
	keys := member(t, list, "keys")
	require.Equal(t, "Map", keys.Receiver)
	require.True(t, keys.Builtin)
	require.NotEmpty(t, keys.Doc)

	byName, err := analysis.Members(ctx, scope, "Map")
	require.NoError(t, err)
	require.Equal(t, names(list.Members), names(byName.Members))
}

func TestMembersOfAUserType(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	list, err := analysis.Members(ctx, scope, `Person(name: "a")`)
	require.NoError(t, err)
	require.Equal(t, "Person!", list.Type)
	require.Equal(t, []string{"greet", "name"}, names(list.Members), "private members are left out")

	greet := member(t, list, "greet")
	require.Equal(t, analysis.KindFunction, greet.Kind)
	require.Equal(t, "Greets someone.", greet.Doc)
	// An argument with a default is optional, so its type is nullable.
	require.Equal(t, "greet(greeting: String): String!", greet.Signature())

	name := member(t, list, "name")
	require.Equal(t, analysis.KindValue, name.Kind)
	require.Equal(t, "name: String!", name.Signature())
	require.Equal(t, "The person's name.", name.Doc)

	// By the type's name.
	byName, err := analysis.Members(ctx, scope, "Person")
	require.NoError(t, err)
	require.Equal(t, "Person", byName.Type)
	require.Contains(t, names(byName.Members), "greet")

	// A list of them has list methods; its elements, Person's.
	people, err := analysis.Members(ctx, scope, "people")
	require.NoError(t, err)
	require.Contains(t, names(people.Members), "map")
}

func TestMembersOfASchemaType(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	list, err := analysis.Members(ctx, scope, "Test.serverInfo")
	require.NoError(t, err)
	require.Equal(t, "Test.ServerInfo!", list.Type)
	version := member(t, list, "version")
	require.Equal(t, "Test.ServerInfo", version.Receiver)
	require.Equal(t, analysis.KindFunction, version.Kind)

	byName, err := analysis.Members(ctx, scope, "Test.ServerInfo")
	require.NoError(t, err)
	require.Equal(t, names(list.Members), names(byName.Members))

	doc, err := analysis.Doc(ctx, scope, "Test.ServerInfo.version")
	require.NoError(t, err)
	require.Equal(t, "version", doc.Name)

	// The import's namespace lists its fields and types.
	ns, err := analysis.Members(ctx, scope, "Test")
	require.NoError(t, err)
	require.Contains(t, names(ns.Members), "serverInfo")
	require.Contains(t, names(ns.Members), "ServerInfo")
}

func TestMembersOfTheScope(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	list, err := analysis.Members(ctx, scope, "")
	require.NoError(t, err)
	require.Empty(t, list.Type)

	add := member(t, list, "add")
	require.Equal(t, analysis.KindFunction, add.Kind)
	require.Equal(t, "Adds two numbers.", add.Doc)
	require.False(t, add.Builtin)

	helper := member(t, list, "helper")
	require.Equal(t, analysis.KindValue, helper.Kind, "the scope's private declarations are listed")

	// A type's name is bound to its constructor, which leads to the type.
	person := member(t, list, "Person")
	require.Equal(t, analysis.KindFunction, person.Kind)
	require.NotNil(t, person.Scope)
	require.Equal(t, "Person", person.Scope.Name())

	test := member(t, list, "Test")
	require.Equal(t, analysis.KindType, test.Kind)
	require.NotNil(t, test.Scope)

	printFn := member(t, list, "print")
	require.True(t, printFn.Builtin)
	require.Equal(t, `print("hello, world")`, printFn.Example)

	toJSON := member(t, list, "toJSON")
	require.NotEmpty(t, toJSON.Deprecated)
	require.Equal(t, "JSON.encode", toJSON.Replacement)

	for _, m := range list.Members {
		require.False(t, dang.IsInternalName(m.Name), "internal name %s listed", m.Name)
	}
}

func TestDocOfStatics(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	encode, err := analysis.Doc(ctx, scope, "JSON.encode")
	require.NoError(t, err)
	require.True(t, encode.Static)
	require.True(t, encode.Builtin)
	require.NotEmpty(t, encode.Doc, "a static's docs come from its builtin definition")
	require.NotEmpty(t, encode.Example)

	random, err := analysis.Doc(ctx, scope, "Random.int")
	require.NoError(t, err)
	require.NotEmpty(t, random.Doc)

	escape, err := analysis.Doc(ctx, scope, "Regexp.escape")
	require.NoError(t, err)
	require.True(t, escape.Static)
	require.NotEmpty(t, escape.Doc)

	// A self { } block's statics, though the type's name is bound to its
	// constructor.
	nobody, err := analysis.Doc(ctx, scope, "Person.nobody")
	require.NoError(t, err)
	require.True(t, nobody.Static)
	require.Equal(t, "Nobody in particular.", nobody.Doc)
	require.Equal(t, "Person.nobody", analysis.Search(scope, "nobody", 1)[0].Qualified())
}

func TestDocOfDeclarations(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	add, err := analysis.Doc(ctx, scope, "add")
	require.NoError(t, err)
	require.Equal(t, "add(x: Int!, y: Int!): Int!", add.Signature())

	person, err := analysis.Doc(ctx, scope, "Person")
	require.NoError(t, err)
	require.Equal(t, "A person.", person.Doc)

	greet, err := analysis.Doc(ctx, scope, "Person.greet")
	require.NoError(t, err)
	require.Equal(t, "Person", greet.Receiver)

	_, err = analysis.Doc(ctx, scope, "nope")
	require.ErrorContains(t, err, `"nope" not found`)

	_, err = analysis.Doc(ctx, scope, "nope.x")
	require.ErrorContains(t, err, `"nope" not found`)
}

func TestSearch(t *testing.T) {
	scope := testScope(t, membersSource)

	results := analysis.Search(scope, "base64", 5)
	require.NotEmpty(t, results)
	require.Equal(t, "String", results[0].Receiver)
	require.Contains(t, results[0].Name, "Base64")

	results = analysis.Search(scope, "accumulator", 10)
	require.Contains(t, qualified(results), "List.reduce")

	results = analysis.Search(scope, "greets someone", 10)
	require.Equal(t, []string{"Person.greet"}, qualified(results))

	// Imported types are qualified by their shortest name: the file imports
	// Test's names unqualified too.
	results = analysis.Search(scope, "version", 10)
	require.Contains(t, qualified(results), "ServerInfo.version")

	// A member reachable by several names is found once, by the shortest.
	results = analysis.Search(scope, "featured", 10)
	require.Contains(t, qualified(results), "featured")
	require.NotContains(t, qualified(results), "Query.featured")
	require.NotContains(t, qualified(results), "Test.featured")

	require.Len(t, analysis.Search(scope, "a", 3), 3)
}

func qualified(members []analysis.Member) []string {
	var qs []string
	for _, m := range members {
		qs = append(qs, m.Qualified())
	}
	return qs
}

func TestMembersSerialize(t *testing.T) {
	ctx := context.Background()
	scope := testScope(t, membersSource)

	reduce, err := analysis.Doc(ctx, scope, "List.reduce")
	require.NoError(t, err)
	out, err := json.Marshal(reduce)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"name": "reduce",
		"receiver": "List",
		"kind": "function",
		"builtin": true,
		"args": [{"name": "initial", "type": "b"}],
		"block": {"args": [{"name": "acc", "type": "b"}, {"name": "item", "type": "a"}], "type": "b"},
		"type": "b",
		"doc": "reduces the list to a single value using an accumulator function",
		"example": "[1, 2, 3, 4].reduce(0) { acc, x => acc + x }"
	}`, string(out))
}
