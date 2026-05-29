package dang

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/pkg/hm"
	"github.com/vito/dang/pkg/introspection"
)

func TestFormatPublicTypeShapePreservesModuleFieldOrder(t *testing.T) {
	mod := NewModule("Thing", ObjectKind)
	mod.Add("zeta", hm.NewScheme(nil, hm.NonNullType{Type: StringType}))
	mod.SetVisibility("zeta", PublicVisibility)
	mod.Add("alpha", hm.NewScheme(nil, hm.NonNullType{Type: IntType}))
	mod.SetVisibility("alpha", PublicVisibility)

	require.Equal(t, "type Thing {\n  pub zeta: String!\n  pub alpha: Int!\n}", FormatPublicTypeShape(mod))
}

func TestFormatPublicTypeShapePreservesGraphQLFieldOrder(t *testing.T) {
	schema := &introspection.Schema{
		Types: introspection.Types{
			{
				Kind: introspection.TypeKindScalar,
				Name: "String",
			},
			{
				Kind: introspection.TypeKindObject,
				Name: "Thing",
				Fields: []*introspection.Field{
					{
						Name: "second",
						TypeRef: &introspection.TypeRef{
							Kind: introspection.TypeKindNonNull,
							OfType: &introspection.TypeRef{
								Kind: introspection.TypeKindScalar,
								Name: "String",
							},
						},
					},
					{
						Name: "first",
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

	env := NewEnv("Test", schema)
	thing, found := env.NamedType("Thing")
	require.True(t, found)
	thingMod, ok := thing.(*Module)
	require.True(t, ok)

	require.Equal(t, "type Thing {\n  pub second: String!\n  pub first: String!\n}", FormatPublicTypeShape(thingMod))
}
