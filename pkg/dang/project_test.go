package dang

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/introspection"
)

func TestSchemaFromSDL(t *testing.T) {
	sdl := `
enum Status {
  ACTIVE
  INACTIVE
}

scalar Timestamp

interface Node {
  id: String!
}

type Query {
  hello(name: String!): String!
  users: [User!]!
  status: Status!
}

type User implements Node {
  id: String!
  name: String!
  age: Int
}

input CreateUserInput {
  name: String!
  email: String!
}

directive @custom(message: String) on FIELD_DEFINITION
`

	schema, err := SchemaFromSDL(sdl, "test.graphqls")
	require.NoError(t, err)
	require.NotNil(t, schema)

	// Query type is set
	require.Equal(t, "Query", schema.QueryType.Name)

	// Types are converted
	queryType := schema.Types.Get("Query")
	require.NotNil(t, queryType)
	require.Equal(t, introspection.TypeKindObject, queryType.Kind)
	require.GreaterOrEqual(t, len(queryType.Fields), 3)

	// hello field has args
	var helloField *introspection.Field
	for _, f := range queryType.Fields {
		if f.Name == "hello" {
			helloField = f
			break
		}
	}
	require.NotNil(t, helloField)
	require.Len(t, helloField.Args, 1)
	require.Equal(t, "name", helloField.Args[0].Name)
	// arg type should be String! (NON_NULL -> String)
	require.Equal(t, introspection.TypeKindNonNull, helloField.Args[0].TypeRef.Kind)
	require.Equal(t, "String", helloField.Args[0].TypeRef.OfType.Name)

	// Enum type
	statusType := schema.Types.Get("Status")
	require.NotNil(t, statusType)
	require.Equal(t, introspection.TypeKindEnum, statusType.Kind)
	require.Len(t, statusType.EnumValues, 2)

	// Scalar type
	tsType := schema.Types.Get("Timestamp")
	require.NotNil(t, tsType)
	require.Equal(t, introspection.TypeKindScalar, tsType.Kind)

	// Interface type
	nodeType := schema.Types.Get("Node")
	require.NotNil(t, nodeType)
	require.Equal(t, introspection.TypeKindInterface, nodeType.Kind)

	// Object implementing interface
	userType := schema.Types.Get("User")
	require.NotNil(t, userType)
	require.Equal(t, introspection.TypeKindObject, userType.Kind)
	require.Len(t, userType.Interfaces, 1)
	require.Equal(t, "Node", userType.Interfaces[0].Name)

	// Input type
	inputType := schema.Types.Get("CreateUserInput")
	require.NotNil(t, inputType)
	require.Equal(t, introspection.TypeKindInputObject, inputType.Kind)
	require.Len(t, inputType.InputFields, 2)

	// Custom directive
	require.NotEmpty(t, schema.Directives)
	var customDir *introspection.DirectiveDef
	for _, d := range schema.Directives {
		if d.Name == "custom" {
			customDir = d
			break
		}
	}
	require.NotNil(t, customDir)
	require.Equal(t, []string{"FIELD_DEFINITION"}, customDir.Locations)
}

func TestSchemaFromSDLFile(t *testing.T) {
	// Test loading the actual test server schema
	schema, err := SchemaFromSDLFile("../../tests/gqlserver/schema.graphqls")
	require.NoError(t, err)
	require.NotNil(t, schema)
	require.Equal(t, "Query", schema.QueryType.Name)

	// Verify some known types from the test schema
	require.NotNil(t, schema.Types.Get("User"))
	require.NotNil(t, schema.Types.Get("Post"))
	require.NotNil(t, schema.Types.Get("Status"))
	require.NotNil(t, schema.Types.Get("ServerInfo"))
}

func TestProjectConfigImports(t *testing.T) {
	// Test that dang.json schema-only imports work for type checking.
	// This simulates what the LSP does: type-check using only the SDL schema.
	ctx := context.Background()

	configPath := "../../tests/dang.json"
	config, err := LoadProjectConfig(configPath)
	require.NoError(t, err)

	configDir := filepath.Dir(configPath)
	resolved, err := ResolveImportConfigs(ctx, config, configDir)
	require.NoError(t, err)
	require.Len(t, resolved, 2)

	// Verify we got schemas for both imports
	var testConfig, otherConfig ImportConfig
	for _, c := range resolved {
		switch c.Name {
		case "Test":
			testConfig = c
		case "Other":
			otherConfig = c
		}
	}
	require.NotNil(t, testConfig.Schema, "Test import should have a schema")
	require.NotNil(t, otherConfig.Schema, "Other import should have a schema")

	// Verify the schema has expected types
	require.NotNil(t, testConfig.Schema.Types.Get("User"))
	require.NotNil(t, testConfig.Schema.Types.Get("Query"))

	// No client for schema-only imports (no endpoint)
	require.Nil(t, testConfig.Client, "schema-only import should have no client")

	// Verify type inference works with schema-only imports
	ctx = ContextWithImportConfigs(ctx, resolved...)
	source := []byte(`import Test
let x = hello(name: "world")
`)
	parsed, err := Parse("test", source)
	require.NoError(t, err)

	typeEnv := NewPreludeEnv()
	_, err = Infer(ctx, typeEnv, parsed.(*ModuleBlock), true)
	require.NoError(t, err)
}

func TestLoadProjectConfig(t *testing.T) {
	// Test loading the test dang.json
	config, err := LoadProjectConfig("../../tests/dang.json")
	require.NoError(t, err)
	require.NotNil(t, config)
	require.Len(t, config.Imports, 2)
	require.NotNil(t, config.Imports["Test"])
	require.Equal(t, "./gqlserver/schema.graphqls", config.Imports["Test"].Schema)
	require.NotNil(t, config.Imports["Other"])
}
