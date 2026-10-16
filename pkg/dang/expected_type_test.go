package dang

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Khan/genqlient/graphql"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
	"github.com/vito/dang/v2/pkg/introspection"
	"github.com/vito/dang/v2/pkg/querybuilder"
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

// An ID result whose field is annotated with @expectedType infers as the
// expected object type, so at runtime it has to come back as a handle for that
// object -- loaded via node(id:) -- rather than as the raw ID string.
func TestExpectedTypeDirectivePromotesIDResultToObjectValue(t *testing.T) {
	schema := expectedTypeTestSchema()
	schema.Types.Get("Query").Fields = append(schema.Types.Get("Query").Fields, nodeLoaderTestField())
	env := TypeScopeFromSchema("Dagger", schema)
	client := graphql.NewClient("http://dang.test/query", nil)

	val, err := graphQLResultToValue("cv-id", idTypeRef(), "CacheVolume", schema, env, client)
	require.NoError(t, err)

	gqlVal, ok := val.(GraphQLValue)
	require.True(t, ok, "expected a GraphQL handle, got %T", val)
	require.Equal(t, "CacheVolume", gqlVal.TypeName)
	require.Equal(t, "cv-id", gqlVal.KnownID)

	// The handle is chainable: further selections nest inside the fragment.
	query, err := gqlVal.QueryChain.Select("id").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{node(id:"cv-id"){... on CacheVolume{id}}}`, query)

	// And it still marshals back to its ID for ID-typed arguments, without
	// having to query for one.
	id, err := gqlVal.ID(context.Background())
	require.NoError(t, err)
	require.Equal(t, "cv-id", id)
}

// Without a node(id:) field there is nothing to load the ID with, so the raw ID
// string stays as-is -- it still satisfies the ID arguments it flows into.
func TestExpectedTypeDirectiveKeepsIDResultWithoutNodeField(t *testing.T) {
	schema := expectedTypeTestSchema()
	env := TypeScopeFromSchema("Dagger", schema)
	client := graphql.NewClient("http://dang.test/query", nil)

	val, err := graphQLResultToValue("cv-id", idTypeRef(), "CacheVolume", schema, env, client)
	require.NoError(t, err)
	require.Equal(t, StringValue{Val: "cv-id"}, val)
}

// Older Dagger schemas have no node(id:), but a load<T>FromID root field per
// type, taking that type's own ID scalar. Objects load through it, with no
// inline fragment needed, as long as the ID has the type the loader takes.
func TestLoadObjectFromIDUsesPerTypeLoader(t *testing.T) {
	schema := legacyLoaderTestSchema()
	env := TypeScopeFromSchema("Dagger", schema)
	client := graphql.NewClient("http://dang.test/query", nil)

	fields, idType := nullProbeSelection(schema, "CacheVolume")
	require.Equal(t, []string{"id", "__typename"}, fields)
	require.Equal(t, "CacheVolumeID", idType)

	val, err := loadObjectFromTypedID("cv-id", idType, "CacheVolume", schema, env, client)
	require.NoError(t, err)
	gqlVal, ok := val.(GraphQLValue)
	require.True(t, ok, "expected a GraphQL handle, got %T", val)
	require.Equal(t, "CacheVolume", gqlVal.TypeName)
	require.Equal(t, "cv-id", gqlVal.KnownID)

	query, err := gqlVal.QueryChain.Select("id").Build(context.Background())
	require.NoError(t, err)
	require.Equal(t, `{loadCacheVolumeFromID(id:"cv-id"){id}}`, query)

	// An ID of another type can't be fed to the loader.
	val, err = loadObjectFromTypedID("cv-id", "ID", "CacheVolume", schema, env, client)
	require.NoError(t, err)
	require.Nil(t, val)
}

// When a schema has both, node(id:) wins.
func TestIDLoaderPrefersNode(t *testing.T) {
	schema := legacyLoaderTestSchema()
	node := nodeLoaderTestField()
	node.Args[0].TypeRef.OfType.Name = "CacheVolumeID"
	schema.Types.Get("Query").Fields = append(schema.Types.Get("Query").Fields, node)

	loader, fragment := idLoaderFor(schema, "CacheVolume", "CacheVolumeID")
	require.NotNil(t, loader)
	require.Equal(t, "node", loader.Name)
	require.True(t, fragment)

	// ...unless it takes a different kind of ID.
	node.Args[0].TypeRef.OfType.Name = "ID"
	loader, fragment = idLoaderFor(schema, "CacheVolume", "CacheVolumeID")
	require.NotNil(t, loader)
	require.Equal(t, "loadCacheVolumeFromID", loader.Name)
	require.False(t, fragment)
}

// Only an ID that promises to identify an object is loadable: `ID!`, or a
// custom ID scalar like Dagger's legacy `CacheVolumeID!`. Other built-in
// scalars, `String!` included, and nullable IDs are not.
func TestLoadableIDFieldRequiresIDOrCustomScalar(t *testing.T) {
	for _, tc := range []struct {
		scalar   string
		nonNull  bool
		loadable bool
	}{
		{"ID", true, true},
		{"CacheVolumeID", true, true},
		{"ID", false, false},
		{"String", true, false},
		{"Int", true, false},
		{"Float", true, false},
		{"Boolean", true, false},
	} {
		typeRef := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: tc.scalar}
		if tc.nonNull {
			typeRef = &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: typeRef}
		}
		typ := &introspection.Type{
			Kind:   introspection.TypeKindObject,
			Name:   "CacheVolume",
			Fields: []*introspection.Field{{Name: "id", TypeRef: typeRef}},
		}
		if tc.loadable {
			require.NotNil(t, loadableIDField(typ), "%s (non-null: %v)", tc.scalar, tc.nonNull)
		} else {
			require.Nil(t, loadableIDField(typ), "%s (non-null: %v)", tc.scalar, tc.nonNull)
		}
	}
}

// A `String!` id may only be unique within its type, so it isn't trusted to
// load the object, even through a node(id:) that takes a String: the null
// probe checks `__typename` alone and the handle keeps its original chain,
// with no known ID.
func TestNullProbeDoesNotLoadStringID(t *testing.T) {
	schema := expectedTypeTestSchema()
	schema.Types.Get("CacheVolume").Fields[0].TypeRef.OfType.Name = "String"
	node := nodeLoaderTestField()
	node.Args[0].TypeRef.OfType.Name = "String"
	query := schema.Types.Get("Query")
	query.Fields = append(query.Fields, node)
	env := TypeScopeFromSchema("Dagger", schema)

	fields, idType := nullProbeSelection(schema, "CacheVolume")
	require.Equal(t, []string{"__typename"}, fields)
	require.Empty(t, idType)

	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		queries = append(queries, req.Query)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"cacheVolume":{"__typename":"CacheVolume"}}}`)
	}))
	defer srv.Close()
	client := graphql.NewClient(srv.URL, srv.Client())

	cacheVolumeType, found := env.NamedType("CacheVolume")
	require.True(t, found)
	handle := GraphQLValue{
		Name:       "cacheVolume",
		TypeName:   "CacheVolume",
		Field:      query.Fields[0],
		ValType:    cacheVolumeType,
		Client:     client,
		Schema:     schema,
		TypeScope:  env,
		QueryChain: querybuilder.Query().Client(client).Select("cacheVolume").Arg("key", "k"),
		MaybeNull:  true,
	}

	ctx := context.Background()
	val, err := handle.resolveNull(ctx, "cacheVolume")
	require.NoError(t, err)
	require.Equal(t, []string{`query Query {cacheVolume(key:"k"){__typename}}`}, queries)

	gqlVal, ok := val.(GraphQLValue)
	require.True(t, ok, "expected a GraphQL handle, got %T", val)
	require.Equal(t, "CacheVolume", gqlVal.TypeName)
	require.Empty(t, gqlVal.KnownID)
	require.False(t, gqlVal.MaybeNull)

	built, err := gqlVal.QueryChain.Select("id").Build(ctx)
	require.NoError(t, err)
	require.Equal(t, `{cacheVolume(key:"k"){id}}`, built)
}

// legacyLoaderTestSchema is expectedTypeTestSchema in the style of older Dagger
// engines: CacheVolume is identified by its own CacheVolumeID scalar and loaded
// with loadCacheVolumeFromID.
func legacyLoaderTestSchema() *introspection.Schema {
	schema := expectedTypeTestSchema()
	schema.Types = append(schema.Types, &introspection.Type{
		Kind: introspection.TypeKindScalar,
		Name: "CacheVolumeID",
	})
	schema.Types.Get("CacheVolume").Fields[0].TypeRef.OfType.Name = "CacheVolumeID"
	query := schema.Types.Get("Query")
	query.Fields = append(query.Fields, &introspection.Field{
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
	})
	return schema
}

func idTypeRef() *introspection.TypeRef {
	return &introspection.TypeRef{
		Kind: introspection.TypeKindNonNull,
		OfType: &introspection.TypeRef{
			Kind: introspection.TypeKindScalar,
			Name: "ID",
		},
	}
}

func nodeLoaderTestField() *introspection.Field {
	return &introspection.Field{
		Name: "node",
		Args: introspection.InputValues{
			{Name: "id", TypeRef: idTypeRef()},
		},
		TypeRef: &introspection.TypeRef{
			Kind: introspection.TypeKindObject,
			Name: "CacheVolume",
		},
	}
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
