package lsp

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
)

// A qualified type pattern (`d: Lib.Dog! =>`) names Lib's type, not a local
// one of the same name, so renaming the local type must leave it alone.
func TestRenameSkipsQualifiedTypePatterns(t *testing.T) {
	src := `type Dog {
  name: String!
}

let a = case (pet) {
  d: Lib.Dog! => 1
  o: Dog! => 2
}

let b = risky rescue {
  e: Lib.Dog! => 1
}
`
	parsed, err := dang.Parse("test.dang", []byte(src))
	require.NoError(t, err)

	h := &langHandler{}
	var lines []int
	for _, sym := range h.findSymbolNodes(parsed.(dang.Node), "Dog") {
		lines = append(lines, sym.Loc.Line)
	}
	// Only the unqualified pattern refers to the local Dog.
	require.Equal(t, []int{7}, lines)
}
