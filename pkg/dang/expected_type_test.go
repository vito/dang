package dang

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

func TestExpectedTypeDirectiveMapsIDArgumentToObject(t *testing.T) {
	env := NewEnv("Dagger", expectedTypeTestSchema())

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
	cacheArgNonNull, ok := cacheArgType.(hm.NonNullType)
	require.True(t, ok)
	require.Equal(t, "CacheVolume", cacheArgNonNull.Type.Name())

	cacheVolumeScheme, found := env.SchemeOf("cacheVolume")
	require.True(t, found)
	cacheVolumeType, mono := cacheVolumeScheme.Type()
	require.True(t, mono)
	cacheVolumeFn, ok := cacheVolumeType.(*hm.FunctionType)
	require.True(t, ok)

	_, err := hm.Assignable(cacheVolumeFn.Ret(false), cacheArgType)
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

	useCachesScheme, found := env.SchemeOf("useCaches")
	require.True(t, found)
	useCachesType, mono := useCachesScheme.Type()
	require.True(t, mono)
	useCachesFn, ok := useCachesType.(*hm.FunctionType)
	require.True(t, ok)

	cachesArgScheme, found := useCachesFn.Arg().(*RecordType).SchemeOf("caches")
	require.True(t, found)
	cachesArgType, mono := cachesArgScheme.Type()
	require.True(t, mono)
	cachesArgNonNull, ok := cachesArgType.(hm.NonNullType)
	require.True(t, ok)

	_, isGraphQLList := cachesArgNonNull.Type.(GraphQLListType)
	require.False(t, isGraphQLList)

	cachesList, ok := cachesArgNonNull.Type.(ListType)
	require.True(t, ok)
	cacheElemNonNull, ok := cachesList.Type.(hm.NonNullType)
	require.True(t, ok)
	require.Equal(t, "CacheVolume", cacheElemNonNull.Type.Name())
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
