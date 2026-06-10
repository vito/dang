package dang

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
)

func TestAssignableRejectsImplicitScalarCoercion(t *testing.T) {
	stringType := hm.NonNullType{Type: StringType}
	idType := hm.NonNullType{Type: IDType}

	_, err := hm.Assignable(stringType, idType)
	require.Error(t, err)
	require.False(t, hm.IsSubtypeOf(stringType, idType))

	_, err = hm.AssignableWithCoercion(stringType, idType)
	require.NoError(t, err)
}

func TestExpectedTypeDirectiveMapsIDArgumentToObject(t *testing.T) {
	env := TypeScopeFromSchema("Dagger", expectedTypeTestSchema())

	cacheArgType := requireFunctionArgType(t, env, "useCache", "cache")
	cacheArgNonNull, ok := cacheArgType.(hm.NonNullType)
	require.True(t, ok)
	require.Equal(t, "CacheVolume", cacheArgNonNull.Type.Name())

	cacheVolumeRet := requireFunctionReturnType(t, env, "cacheVolume")
	_, err := hm.Assignable(cacheVolumeRet, cacheArgType)
	require.NoError(t, err)
}

func TestExpectedTypeDirectiveMapsIDReturnToObject(t *testing.T) {
	env := TypeScopeFromSchema("Dagger", expectedTypeTestSchema())

	syncCacheScheme, found := env.SchemeOf("syncCache")
	require.True(t, found)
	syncCacheType, mono := syncCacheScheme.Type()
	require.True(t, mono)
	syncCacheFn, ok := syncCacheType.(*hm.FunctionType)
	require.True(t, ok)

	syncCacheRet := syncCacheFn.Ret(false)
	syncCacheNonNull, ok := syncCacheRet.(hm.NonNullType)
	require.True(t, ok)
	require.Equal(t, "CacheVolume", syncCacheNonNull.Type.Name())

	useCacheScheme, found := env.SchemeOf("useCache")
	require.True(t, found)
	useCacheType, mono := useCacheScheme.Type()
	require.True(t, mono)
	useCacheFn, ok := useCacheType.(*hm.FunctionType)
	require.True(t, ok)

	cacheArgScheme, found := useCacheFn.Arg().(*RecordType).SchemeOf("cache")
	require.True(t, found)
	cacheArgType, mono := cacheArgScheme.Type()
	require.True(t, mono)

	_, err := hm.Assignable(syncCacheRet, cacheArgType)
	require.NoError(t, err)
}

func TestExpectedTypeDirectiveMapsIDListArgumentToPlainList(t *testing.T) {
	env := TypeScopeFromSchema("Dagger", expectedTypeTestSchema())

	cachesArgType := requireFunctionArgType(t, env, "useCaches", "caches")
	cachesArgNonNull, ok := cachesArgType.(hm.NonNullType)
	require.True(t, ok)

	_, isGraphQLList := cachesArgNonNull.Type.(GraphQLListType)
	require.False(t, isGraphQLList)

	cachesList, ok := cachesArgNonNull.Type.(ListType)
	require.True(t, ok)
	cacheElemNonNull, ok := cachesList.Type.(hm.NonNullType)
	require.True(t, ok)
	require.Equal(t, "CacheVolume", cacheElemNonNull.Type.Name())

	cacheVolumeRet := requireFunctionReturnType(t, env, "cacheVolume")
	objectList := hm.NonNullType{Type: ListType{Type: cacheVolumeRet}}
	_, err := hm.Assignable(objectList, cachesArgType)
	require.NoError(t, err)
}

func requireFunctionArgType(t *testing.T, env TypeScope, funcName, argName string) hm.Type {
	t.Helper()

	scheme, found := env.SchemeOf(funcName)
	require.True(t, found)
	type_, mono := scheme.Type()
	require.True(t, mono)
	fn, ok := type_.(*hm.FunctionType)
	require.True(t, ok)

	argScheme, found := fn.Arg().(*RecordType).SchemeOf(argName)
	require.True(t, found)
	argType, mono := argScheme.Type()
	require.True(t, mono)
	return argType
}

func requireFunctionReturnType(t *testing.T, env TypeScope, funcName string) hm.Type {
	t.Helper()

	scheme, found := env.SchemeOf(funcName)
	require.True(t, found)
	type_, mono := scheme.Type()
	require.True(t, mono)
	fn, ok := type_.(*hm.FunctionType)
	require.True(t, ok)
	return fn.Ret(false)
}

func expectedTypeTestSchema() *introspection.Schema {
	expectedTypeValue := `"CacheVolume"`
	return &introspection.Schema{
		QueryType: struct {
			Name string `json:"name,omitempty"`
		}{Name: "Query"},
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
				Name: "CacheVolume",
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
				},
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Query",
				Fields: []*introspection.Field{
					{
						Name: "cacheVolume",
						Args: introspection.InputValues{
							{
								Name: "key",
								TypeRef: &introspection.TypeRef{
									Kind: introspection.TypeKindNonNull,
									OfType: &introspection.TypeRef{
										Kind: introspection.TypeKindScalar,
										Name: "String",
									},
								},
							},
						},
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindObject,
								Name: "CacheVolume",
							},
						},
					},
					{
						Name: "syncCache",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "ID",
							},
						},
						Directives: introspection.Directives{
							{
								Name: "expectedType",
								Args: []*introspection.DirectiveArg{
									{Name: "name", Value: &expectedTypeValue},
								},
							},
						},
					},
					{
						Name: "useCache",
						Args: introspection.InputValues{
							{
								Name: "cache",
								TypeRef: &introspection.TypeRef{
									Kind: introspection.TypeKindNonNull,
									OfType: &introspection.TypeRef{
										Kind: introspection.TypeKindScalar,
										Name: "ID",
									},
								},
								Directives: introspection.Directives{
									{
										Name: "expectedType",
										Args: []*introspection.DirectiveArg{
											{Name: "name", Value: &expectedTypeValue},
										},
									},
								},
							},
						},
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "String",
							},
						},
					},
					{
						Name: "useCaches",
						Args: introspection.InputValues{
							{
								Name: "caches",
								TypeRef: &introspection.TypeRef{
									Kind: introspection.TypeKindNonNull,
									OfType: &introspection.TypeRef{
										Kind: introspection.TypeKindList,
										OfType: &introspection.TypeRef{
											Kind: introspection.TypeKindNonNull,
											OfType: &introspection.TypeRef{
												Kind: introspection.TypeKindScalar,
												Name: "ID",
											},
										},
									},
								},
								Directives: introspection.Directives{
									{
										Name: "expectedType",
										Args: []*introspection.DirectiveArg{
											{Name: "name", Value: &expectedTypeValue},
										},
									},
								},
							},
						},
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
		},
	}
}
