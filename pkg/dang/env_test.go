package dang

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/introspection"
)

func TestNewEnvSchemaTypeShadowsPreludeType(t *testing.T) {
	_, found := ErrorType.LocalSchemeOf("id")
	require.False(t, found)

	env := NewEnv("Dagger", schemaWithErrorObject())

	schemaString, found := env.NamedType("String")
	require.True(t, found)
	require.Same(t, StringType, schemaString)

	schemaError, found := env.NamedType("Error")
	require.True(t, found)
	require.NotSame(t, ErrorType, schemaError)

	schemaErrorMod, ok := schemaError.(*Module)
	require.True(t, ok)
	require.Equal(t, ObjectKind, schemaErrorMod.Kind)

	_, found = schemaError.LocalSchemeOf("id")
	require.True(t, found)
	_, found = schemaError.LocalSchemeOf("message")
	require.True(t, found)

	_, found = ErrorType.LocalSchemeOf("id")
	require.False(t, found)
}

func TestConcurrentNewEnvWithPreludeTypeCollision(t *testing.T) {
	schema := schemaWithErrorObject()

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan bool, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			env := NewEnv("Dagger", schema)
			schemaError, found := env.NamedType("Error")
			errs <- found && schemaError != ErrorType
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for ok := range errs {
		require.True(t, ok)
	}
	_, found := ErrorType.LocalSchemeOf("id")
	require.False(t, found)
}

func TestRunDirDeclarationsShadowPreludeTypes(t *testing.T) {
	env := runDangSnippet(t, `
type Error {
  pub id: String! = "x"
}
assert { Error.id == "x" }
`)
	classVal, found := env.Get("Error")
	require.True(t, found)
	classFn, ok := classVal.(*ConstructorFunction)
	require.True(t, ok)
	require.NotSame(t, ErrorType, classFn.ClassType)
	_, found = ErrorType.LocalSchemeOf("id")
	require.False(t, found)

	env = runDangSnippet(t, `
enum Error { FOO }
assert { Error.FOO == Error.FOO }
`)
	enumVal, found := env.Get("Error")
	require.True(t, found)
	enumMod, ok := enumVal.(*ModuleValue)
	require.True(t, ok)
	require.NotSame(t, ErrorType, enumMod.Mod)
	_, found = ErrorType.LocalSchemeOf("FOO")
	require.False(t, found)

	env = runDangSnippet(t, `
scalar Error
`)
	scalarVal, found := env.Get("Error")
	require.True(t, found)
	scalarMod, ok := scalarVal.(*ModuleValue)
	require.True(t, ok)
	require.NotSame(t, ErrorType, scalarMod.Mod)
	require.Equal(t, ScalarKind, scalarMod.Mod.(*Module).Kind)
}

func TestRunDirImplementingPreludeInterfaceDoesNotMutatePrelude(t *testing.T) {
	before := len(ErrorType.GetImplementers())
	runDangSnippet(t, `
type MyError implements Error {
  pub message: String! = "x"
}
assert { MyError.message == "x" }
`)
	require.Len(t, ErrorType.GetImplementers(), before)
}

func TestRunDirUnionWithPreludeMemberDoesNotMutatePrelude(t *testing.T) {
	before := len(BasicErrorType.GetUnions())
	runDangSnippet(t, `
union MyUnion = BasicError
assert { MyUnion != null }
`)
	require.Len(t, BasicErrorType.GetUnions(), before)
}

func runDangSnippet(t *testing.T, source string) EvalEnv {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(source), 0o600))
	env, err := RunDir(context.Background(), dir, false)
	require.NoError(t, err)
	return env
}

func schemaWithErrorObject() *introspection.Schema {
	schema := &introspection.Schema{
		Types: introspection.Types{
			{
				Kind: introspection.TypeKindScalar,
				Name: "ID",
			},
			{
				Kind: introspection.TypeKindScalar,
				Name: "String",
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Error",
				Fields: []*introspection.Field{
					{
						Name: "id",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "ID",
							},
						},
					},
					{
						Name: "message",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "String",
							},
						},
					},
				},
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Query",
			},
		},
	}
	schema.QueryType.Name = "Query"
	return schema
}
