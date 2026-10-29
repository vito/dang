package lsp

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
)

func TestNameRangeLeavesOutTheEscape(t *testing.T) {
	parsed, err := dang.Parse("test.dang", []byte("let $rescue = 1\nlet y = $rescue\nlet z = y\n"))
	require.NoError(t, err)
	forms := parsed.(*dang.FileBlock).Forms

	decl := forms[0].(*dang.FieldDecl)
	require.Equal(t, Range{
		Start: Position{Line: 0, Character: 5},
		End:   Position{Line: 0, Character: 11},
	}, nameRange(decl.Name.Loc, decl.Name.Name))

	ref := forms[1].(*dang.FieldDecl).Value.(*dang.Symbol)
	require.Equal(t, Range{
		Start: Position{Line: 1, Character: 9},
		End:   Position{Line: 1, Character: 15},
	}, nameRange(ref.Loc, ref.Name))

	plain := forms[2].(*dang.FieldDecl).Value.(*dang.Symbol)
	require.Equal(t, Range{
		Start: Position{Line: 2, Character: 8},
		End:   Position{Line: 2, Character: 9},
	}, nameRange(plain.Loc, plain.Name))
}
