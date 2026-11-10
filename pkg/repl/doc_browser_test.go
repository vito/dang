package repl

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
)

func TestDocItemShowsBounds(t *testing.T) {
	typeScope, _ := dang.BuildScopesFromImports("", nil)
	list, ok := typeScope.NamedType("List")
	require.True(t, ok)

	sum, ok := DocItemFromTypeScope(list, "sum")
	require.True(t, ok)
	require.Equal(t, "[a: Numeric] -> a", sum.TypeStr)

	sortBy, ok := DocItemFromTypeScope(list, "sortBy")
	require.True(t, ok)
	require.Equal(t, "[a, k: Ordered] -> [a]!", sortBy.TypeStr)

	// Unbounded type variables are generic implicitly.
	reduce, ok := DocItemFromTypeScope(list, "reduce")
	require.True(t, ok)
	require.Equal(t, "-> b", reduce.TypeStr)

	// The browser's List column (`:doc`, then List) lists the same.
	col := BuildColumn("List", "", list)
	for _, item := range col.Items {
		if item.Name == "sum" {
			require.Equal(t, "[a: Numeric] -> a", item.TypeStr)
			return
		}
	}
	t.Fatal("no sum in the List column")
}
