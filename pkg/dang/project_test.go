package dang

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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

	configPath := "../../tests/dang.toml"
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

	// Client is a lazy service process (service is configured in dang.toml)
	require.NotNil(t, testConfig.Client, "import with service should have a lazy client")

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
	// Test loading the test dang.toml
	config, err := LoadProjectConfig("../../tests/dang.toml")
	require.NoError(t, err)
	require.NotNil(t, config)
	require.Len(t, config.Imports, 2)
	require.NotNil(t, config.Imports["Test"])
	require.Equal(t, "./gqlserver/schema.graphqls", config.Imports["Test"].Schema)
	require.NotNil(t, config.Imports["Other"])
}

func TestExpandEnvVars(t *testing.T) {
	t.Run("no variables", func(t *testing.T) {
		result, err := expandEnvVars("hello world")
		require.NoError(t, err)
		assert.Equal(t, "hello world", result)
	})

	t.Run("empty string", func(t *testing.T) {
		result, err := expandEnvVars("")
		require.NoError(t, err)
		assert.Equal(t, "", result)
	})

	t.Run("defined variable", func(t *testing.T) {
		t.Setenv("DANG_TEST_VAR", "expanded")
		result, err := expandEnvVars("Bearer ${DANG_TEST_VAR}")
		require.NoError(t, err)
		assert.Equal(t, "Bearer expanded", result)
	})

	t.Run("multiple defined variables", func(t *testing.T) {
		t.Setenv("DANG_TEST_A", "foo")
		t.Setenv("DANG_TEST_B", "bar")
		result, err := expandEnvVars("${DANG_TEST_A}/${DANG_TEST_B}")
		require.NoError(t, err)
		assert.Equal(t, "foo/bar", result)
	})

	t.Run("undefined variable", func(t *testing.T) {
		_ = os.Unsetenv("DANG_TEST_UNDEFINED")
		_, err := expandEnvVars("Bearer ${DANG_TEST_UNDEFINED}")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "${DANG_TEST_UNDEFINED}")
		assert.Contains(t, err.Error(), "not set")

		// Should be an expandError with the pattern for source highlighting
		var expErr *expandError
		require.ErrorAs(t, err, &expErr)
		assert.Equal(t, "${DANG_TEST_UNDEFINED}", expErr.pattern)
	})

	t.Run("command substitution", func(t *testing.T) {
		_, err := expandEnvVars("Bearer $(gh auth token)")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "$() command substitution is not supported")
		assert.Contains(t, err.Error(), "${VAR}")

		var expErr *expandError
		require.ErrorAs(t, err, &expErr)
		assert.Equal(t, "$(gh auth token)", expErr.pattern)
	})
}

func TestTomlSourceError(t *testing.T) {
	// Write a temporary dang.toml
	dir := t.TempDir()
	configPath := filepath.Join(dir, "dang.toml")
	tomlSource := `[imports.GitHub]
endpoint = "https://api.github.com/graphql"
authorization = "Bearer ${GH_TOKEN}"
`
	require.NoError(t, os.WriteFile(configPath, []byte(tomlSource), 0644))

	t.Run("wraps expandError with source location", func(t *testing.T) {
		inner := &expandError{
			msg:     "environment variable ${GH_TOKEN} is not set",
			pattern: "${GH_TOKEN}",
		}
		err := tomlSourceError(configPath, inner)

		var sourceErr *SourceError
		require.ErrorAs(t, err, &sourceErr)
		assert.Equal(t, configPath, sourceErr.Location.Filename)
		assert.Equal(t, 3, sourceErr.Location.Line)
		assert.Equal(t, len("${GH_TOKEN}"), sourceErr.Location.Length)
	})

	t.Run("unwraps through fmt.Errorf wrapping", func(t *testing.T) {
		inner := &expandError{
			msg:     "environment variable ${GH_TOKEN} is not set",
			pattern: "${GH_TOKEN}",
		}
		wrapped := fmt.Errorf("authorization: %w", inner)
		err := tomlSourceError(configPath, wrapped)

		var sourceErr *SourceError
		require.ErrorAs(t, err, &sourceErr)
		assert.Contains(t, sourceErr.Error(), "authorization:")
		assert.Equal(t, 3, sourceErr.Location.Line)
	})

	t.Run("returns original error if not an expandError", func(t *testing.T) {
		plain := fmt.Errorf("something else")
		err := tomlSourceError(configPath, plain)
		assert.Equal(t, plain, err)
	})
}
