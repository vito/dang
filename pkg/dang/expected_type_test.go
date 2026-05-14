package dang

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

func TestExpectedTypeDirectiveMapsIDArgumentToObjectOrID(t *testing.T) {
	env := NewEnv("Dagger", expectedTypeTestSchema())

	cacheArgType := requireFunctionArgType(t, env, "useCache", "cache")
	cacheArgNonNull, ok := cacheArgType.(hm.NonNullType)
	require.True(t, ok)
	cacheArgUnion, ok := cacheArgNonNull.Type.(*hm.UnionType)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"CacheVolume", "ID"}, unionOptionNames(cacheArgUnion))

	cacheVolumeRet := requireFunctionReturnType(t, env, "cacheVolume")
	_, err := hm.Assignable(cacheVolumeRet, cacheArgType)
	require.NoError(t, err)

	_, err = hm.Assignable(hm.NonNullType{Type: IDType}, cacheArgType)
	require.NoError(t, err)
}

func TestExpectedTypeDirectiveMapsIDReturnToObject(t *testing.T) {
	env := NewEnv("Dagger", expectedTypeTestSchema())

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
	env := NewEnv("Dagger", expectedTypeTestSchema())

	cachesArgType := requireFunctionArgType(t, env, "useCaches", "caches")
	cachesArgNonNull, ok := cachesArgType.(hm.NonNullType)
	require.True(t, ok)

	_, isGraphQLList := cachesArgNonNull.Type.(GraphQLListType)
	require.False(t, isGraphQLList)

	cachesList, ok := cachesArgNonNull.Type.(ListType)
	require.True(t, ok)
	cacheElemNonNull, ok := cachesList.Type.(hm.NonNullType)
	require.True(t, ok)
	cacheElemUnion, ok := cacheElemNonNull.Type.(*hm.UnionType)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"CacheVolume", "ID"}, unionOptionNames(cacheElemUnion))

	cacheVolumeRet := requireFunctionReturnType(t, env, "cacheVolume")
	objectList := hm.NonNullType{Type: ListType{Type: cacheVolumeRet}}
	_, err := hm.Assignable(objectList, cachesArgType)
	require.NoError(t, err)

	idList := hm.NonNullType{Type: ListType{Type: hm.NonNullType{Type: IDType}}}
	_, err = hm.Assignable(idList, cachesArgType)
	require.NoError(t, err)
}

func TestCustomIDScalarInputAcceptsObjectOrScalarID(t *testing.T) {
	env := NewEnv("Dagger", expectedTypeTestSchema())

	idArgType := requireFunctionArgType(t, env, "loadCacheVolumeFromID", "id")
	idArgNonNull, ok := idArgType.(hm.NonNullType)
	require.True(t, ok)
	idArgUnion, ok := idArgNonNull.Type.(*hm.UnionType)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"CacheVolume", "CacheVolumeID"}, unionOptionNames(idArgUnion))

	cacheVolumeRet := requireFunctionReturnType(t, env, "cacheVolume")
	_, err := hm.Assignable(cacheVolumeRet, idArgType)
	require.NoError(t, err)

	cacheVolumeID, found := env.NamedType("CacheVolumeID")
	require.True(t, found)
	_, err = hm.Assignable(hm.NonNullType{Type: cacheVolumeID}, idArgType)
	require.NoError(t, err)

	_, err = hm.Assignable(hm.NonNullType{Type: StringType}, idArgType)
	require.NoError(t, err)
}

func TestCustomIDScalarListInputAcceptsObjectOrScalarIDLists(t *testing.T) {
	env := NewEnv("Dagger", expectedTypeTestSchema())

	idsArgType := requireFunctionArgType(t, env, "loadCacheVolumesFromIDs", "ids")
	cacheVolumeRet := requireFunctionReturnType(t, env, "cacheVolume")
	objectList := hm.NonNullType{Type: ListType{Type: cacheVolumeRet}}
	_, err := hm.Assignable(objectList, idsArgType)
	require.NoError(t, err)

	cacheVolumeID, found := env.NamedType("CacheVolumeID")
	require.True(t, found)
	idList := hm.NonNullType{Type: ListType{Type: hm.NonNullType{Type: cacheVolumeID}}}
	_, err = hm.Assignable(idList, idsArgType)
	require.NoError(t, err)
}

func requireFunctionArgType(t *testing.T, env Env, funcName, argName string) hm.Type {
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

func requireFunctionReturnType(t *testing.T, env Env, funcName string) hm.Type {
	t.Helper()

	scheme, found := env.SchemeOf(funcName)
	require.True(t, found)
	type_, mono := scheme.Type()
	require.True(t, mono)
	fn, ok := type_.(*hm.FunctionType)
	require.True(t, ok)
	return fn.Ret(false)
}

func unionOptionNames(union *hm.UnionType) []string {
	names := make([]string, len(union.Options))
	for i, option := range union.Options {
		names[i] = option.Name()
	}
	return names
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
				Kind: introspection.TypeKindScalar,
				Name: "CacheVolumeID",
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
						Name: "loadCacheVolumeFromID",
						Args: introspection.InputValues{
							{
								Name: "id",
								TypeRef: &introspection.TypeRef{
									Kind: introspection.TypeKindNonNull,
									OfType: &introspection.TypeRef{
										Kind: introspection.TypeKindScalar,
										Name: "CacheVolumeID",
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
						Name: "loadCacheVolumesFromIDs",
						Args: introspection.InputValues{
							{
								Name: "ids",
								TypeRef: &introspection.TypeRef{
									Kind: introspection.TypeKindNonNull,
									OfType: &introspection.TypeRef{
										Kind: introspection.TypeKindList,
										OfType: &introspection.TypeRef{
											Kind: introspection.TypeKindNonNull,
											OfType: &introspection.TypeRef{
												Kind: introspection.TypeKindScalar,
												Name: "CacheVolumeID",
											},
										},
									},
								},
							},
						},
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindList,
								OfType: &introspection.TypeRef{
									Kind: introspection.TypeKindNonNull,
									OfType: &introspection.TypeRef{
										Kind: introspection.TypeKindObject,
										Name: "CacheVolume",
									},
								},
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
