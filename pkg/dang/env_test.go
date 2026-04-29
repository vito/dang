package dang

import (
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
