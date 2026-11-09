package dang

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/hm"
)

// A record's fields are its data: public, so completion offers them, and
// display shows them, as it hides a type's private (`let`) fields.
func TestRecordFieldsArePublic(t *testing.T) {
	parsed, err := Parse("record_test.dang", []byte(`{{a: 1, b: "x"}}`))
	require.NoError(t, err)
	lit, ok := parsed.(*FileBlock).Forms[0].(*ObjectLiteral)
	require.True(t, ok, "expected *ObjectLiteral, got %T", parsed.(*FileBlock).Forms[0])
	for _, field := range lit.Fields {
		require.Equal(t, PublicVisibility, field.Visibility, "field %s", field.Name.Name)
	}

	recType, err := ObjectTypeNode{Fields: []ObjectTypeField{
		{Key: "x", Type: NonNullTypeNode{&NamedTypeNode{Name: "Int"}}},
	}}.Infer(context.Background(), NewPreludeTypeScope("test"), hm.NewSimpleFresher())
	require.NoError(t, err)
	var labels []string
	for _, c := range MembersOf(hm.NonNullType{Type: recType}, "") {
		labels = append(labels, c.Label)
	}
	require.Equal(t, []string{"x"}, labels)
}
